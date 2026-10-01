// The fake MCP Apps host (MCP Apps 2026-01-26, docs/ui-contract.md 3.4 to
// 3.6). It loads the view in a sandboxed iframe on another origin, speaks the
// host side of JSON-RPC over postMessage, answers the view's tools/call from
// fixtures, and records every message both ways. run.mjs drives it through
// window.__host.
(() => {
  'use strict';

  const PROTOCOL = '2026-01-26';
  const log = []; // { seq, t, dir: 'view>host' | 'host>view', msg }
  const violations = []; // protocol rules the view broke
  const unanswered = []; // view calls no fixture answered
  const held = []; // calls waiting for release()
  const pending = new Map(); // host-initiated requests by id
  let seq = 0;
  let nextId = 1;
  let cfg = null;
  let frame = null;
  let initializeAnswered = false;
  let initialized = false;
  let displayMode = 'inline';
  let lastSize = null;
  const seenIds = new Set();

  const now = () => performance.now();
  const clone = (v) => (v === undefined ? undefined : JSON.parse(JSON.stringify(v)));
  const record = (dir, msg) => log.push({ seq: ++seq, t: now(), dir, msg: clone(msg) });

  function post(msg) {
    record('host>view', msg);
    frame.contentWindow.postMessage(msg, '*'); // the view's origin is opaque
  }

  function respond(id, result) {
    post({ jsonrpc: '2.0', id, result });
  }

  function respondError(id, code, message) {
    post({ jsonrpc: '2.0', id, error: { code, message } });
  }

  // notify sends a notification. The specification forbids any request or
  // notification before the view's initialized notification.
  function notify(method, params) {
    if (!initialized) {
      violations.push(`harness: ${method} requested before initialized (not sent)`);
      return false;
    }
    post({ jsonrpc: '2.0', method, params: params ?? {} });
    return true;
  }

  function request(method, params) {
    if (!initialized) {
      violations.push(`harness: ${method} requested before initialized (not sent)`);
      return Promise.resolve({ error: 'not initialized' });
    }
    const id = `host-${nextId++}`;
    return new Promise((resolve) => {
      const timer = setTimeout(() => {
        pending.delete(id);
        resolve({ timeout: true });
      }, 2000);
      pending.set(id, (msg) => {
        clearTimeout(timer);
        resolve(msg);
      });
      post({ jsonrpc: '2.0', id, method, params: params ?? {} });
    });
  }

  // subset reports whether every key of want has an equal value in got.
  function subset(want, got) {
    if (want === undefined) return true;
    for (const [k, v] of Object.entries(want)) {
      if (JSON.stringify(v) !== JSON.stringify(got?.[k])) return false;
    }
    return true;
  }

  function answerToolCall(msg) {
    const name = msg.params?.name;
    const args = msg.params?.arguments ?? {};
    const caps = cfg.hostCapabilities ?? {};
    if (!caps.serverTools) {
      violations.push(`tools/call ${name} without hostCapabilities.serverTools`);
      respondError(msg.id, -32601, 'This host does not offer server tools');
      return;
    }
    if (!initialized) violations.push(`tools/call ${name} before ui/notifications/initialized`);
    const allowed = cfg.allowedTools?.[name];
    if (cfg.allowedTools && !allowed) {
      violations.push(`tools/call ${name}: not in the view's tool allowlist`);
    } else if (allowed) {
      for (const k of Object.keys(args)) {
        if (!allowed.includes(k)) violations.push(`tools/call ${name}: argument ${k} not allowed`);
      }
    }
    const answers = cfg.tools?.[name] ?? [];
    const a = answers.find((x) => (x.times === undefined || x.times > 0) && subset(x.when, args));
    if (!a) {
      unanswered.push({ name, args: clone(args) });
      respondError(msg.id, -32603, `harness: no answer configured for ${name}`);
      return;
    }
    if (a.times !== undefined) a.times--;
    const send = () => {
      if (a.rpcError) {
        respondError(msg.id, a.rpcError.code ?? -32000, a.rpcError.message ?? 'refused');
        return;
      }
      respond(msg.id, clone(a.result));
      // Some hosts also deliver a view-initiated call's result as a
      // tool-result notification (the specification's interactive phase).
      if (cfg.echo) notify('ui/notifications/tool-result', clone(a.result));
    };
    if (a.hold) held.push({ name, send });
    else if (a.delayMs) setTimeout(send, a.delayMs);
    else send();
  }

  function onRequest(msg) {
    const caps = cfg.hostCapabilities ?? {};
    const ctx = cfg.hostContext ?? {};
    switch (msg.method) {
      case 'ui/initialize':
        if (initializeAnswered) violations.push('ui/initialize sent twice');
        initializeAnswered = true;
        respond(msg.id, {
          protocolVersion: PROTOCOL,
          hostInfo: { name: 'view-harness', version: '1.0.0' },
          hostCapabilities: clone(caps),
          hostContext: clone(ctx),
        });
        return;
      case 'tools/call':
        answerToolCall(msg);
        return;
      case 'ui/open-link':
        if (!caps.openLinks) violations.push('ui/open-link without hostCapabilities.openLinks');
        respond(msg.id, {});
        return;
      case 'ui/message':
        if (!caps.message) violations.push('ui/message without hostCapabilities.message');
        respond(msg.id, {});
        return;
      case 'ui/update-model-context':
        if (!caps.updateModelContext) violations.push('ui/update-model-context without hostCapabilities.updateModelContext');
        respond(msg.id, {});
        return;
      case 'ui/request-display-mode': {
        const want = msg.params?.mode;
        const available = ctx.availableDisplayModes ?? [];
        if (!available.includes(want)) violations.push(`ui/request-display-mode ${want}: not in availableDisplayModes`);
        else displayMode = want;
        respond(msg.id, { mode: displayMode });
        return;
      }
      default:
        violations.push(`unknown request ${msg.method}`);
        respondError(msg.id, -32601, `Method not found: ${msg.method}`);
    }
  }

  function onNotification(msg) {
    switch (msg.method) {
      case 'ui/notifications/initialized':
        if (!initializeAnswered) violations.push('initialized before ui/initialize was answered');
        if (initialized) violations.push('initialized sent twice');
        initialized = true;
        if (cfg.deliver === 'auto' || cfg.deliver === 'input-only') {
          setTimeout(() => {
            notify('ui/notifications/tool-input', { arguments: clone(cfg.entry?.arguments ?? {}) });
            if (cfg.deliver === 'auto') {
              const deliver = () => notify('ui/notifications/tool-result', clone(cfg.entry.result));
              if (cfg.resultDelayMs) setTimeout(deliver, cfg.resultDelayMs);
              else deliver();
            }
          }, 0);
        }
        return;
      case 'ui/notifications/size-changed':
        lastSize = clone(msg.params);
        if (cfg.followSize && typeof msg.params?.height === 'number') {
          frame.style.height = `${Math.max(96, Math.ceil(msg.params.height))}px`;
        }
        return;
      default:
        // Recorded; nothing else to do.
    }
  }

  window.addEventListener('message', (ev) => {
    if (!frame || ev.source !== frame.contentWindow) return;
    const msg = ev.data;
    record('view>host', msg);
    if (!msg || typeof msg !== 'object' || msg.jsonrpc !== '2.0') {
      violations.push(`not a JSON-RPC 2.0 message: ${JSON.stringify(msg)?.slice(0, 120)}`);
      return;
    }
    if (msg.method === undefined) {
      const done = pending.get(msg.id);
      if (done) {
        pending.delete(msg.id);
        done(msg);
      } else {
        violations.push(`response to unknown request ${JSON.stringify(msg.id)}`);
      }
      return;
    }
    if (msg.id !== undefined) {
      const key = JSON.stringify(msg.id);
      if (seenIds.has(key)) violations.push(`request id ${key} reused`);
      seenIds.add(key);
      if (!initializeAnswered && msg.method !== 'ui/initialize') violations.push(`${msg.method} before ui/initialize`);
      onRequest(msg);
    } else {
      onNotification(msg);
    }
  });

  window.__host = {
    start(config) {
      cfg = config;
      displayMode = config.hostContext?.displayMode ?? 'inline';
      document.body.classList.toggle('dark', config.hostContext?.theme === 'dark');
      frame = document.createElement('iframe');
      frame.setAttribute('sandbox', 'allow-scripts');
      frame.setAttribute('title', 'View');
      frame.style.width = `${config.width ?? 720}px`;
      frame.style.height = `${config.height ?? 1600}px`;
      frame.src = config.viewUrl;
      document.getElementById('frame').appendChild(frame);
      return true;
    },
    notify,
    request,
    // setTools replaces the answers for one tool.
    setTools(name, answers) {
      cfg.tools = cfg.tools ?? {};
      cfg.tools[name] = answers;
    },
    // release answers every held call.
    release() {
      const n = held.length;
      while (held.length) held.shift().send();
      return n;
    },
    heldCount: () => held.length,
    setTheme(theme) {
      document.body.classList.toggle('dark', theme === 'dark');
      cfg.hostContext = { ...cfg.hostContext, theme };
      return notify('ui/notifications/host-context-changed', { theme });
    },
    state: () => clone({ initializeAnswered, initialized, displayMode, lastSize, violations, unanswered, held: held.length }),
    log: () => clone(log),
  };
})();
