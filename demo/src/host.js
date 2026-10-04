// The simulated MCP Apps host (MCP Apps 2026-01-26). The bridge is adapted
// from the view harness's fake host, test/views/host.js: the view runs in a
// sandboxed iframe with an opaque origin (srcdoc, sandbox="allow-scripts"),
// the host answers ui/initialize with hostContext and hostCapabilities, sends
// nothing before ui/notifications/initialized, then delivers the entry call's
// tool-input and tool-result, answers the view's tools/call from recorded
// fixtures, and answers ui/open-link, ui/message, ui/update-model-context and
// ui/request-display-mode. What this host adds: a chat around the view, and a
// permission prompt before any tool that changes midPoint runs.
(() => {
  'use strict';

  const DATA = JSON.parse(document.getElementById('demo-data').textContent);
  const PROTOCOL = '2026-01-26';
  const CAPS = { serverTools: {}, openLinks: {}, message: {}, updateModelContext: {} };
  const WRITE = new Set(DATA.writeTools);
  const WIDTHS = [
    { id: 'phone', label: 'Phone', px: 420 },
    { id: 'chat', label: 'Chat', px: 640 },
    { id: 'expanded', label: 'Expanded', px: 860 },
  ];
  const FULL_PX = 860; // the width a view gets in fullscreen
  const THEMES = [
    { id: 'auto', label: 'Auto' },
    { id: 'light', label: 'Light' },
    { id: 'dark', label: 'Dark' },
  ];
  const HOST_FONT = 'system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif';
  // Small pauses, so the views' own loading and working states show.
  const RESULT_DELAY = 300;
  const READ_DELAY = 220;
  const WRITE_DELAY = 350;
  const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

  const $ = (id) => document.getElementById(id);
  const clone = (v) => (v === undefined ? undefined : JSON.parse(JSON.stringify(v)));
  let uid = 0;
  const nextId = (p) => `${p}-${++uid}`;

  function h(tag, attrs, ...kids) {
    const n = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v === undefined || v === null || v === false) continue;
      if (k === 'class') n.className = v;
      else if (k === 'text') n.textContent = v;
      else if (k.startsWith('on')) n.addEventListener(k.slice(2), v);
      else n.setAttribute(k, v === true ? '' : String(v));
    }
    for (const c of kids.flat(Infinity)) {
      if (c === null || c === undefined || c === false) continue;
      n.append(c instanceof Node ? c : String(c));
    }
    return n;
  }
  const code = (t) => h('code', null, t);
  // ticks turns `tool_name` in stage directions into code.
  const ticks = (text) => String(text).split(/`([^`]+)`/).map((part, i) => (i % 2 ? code(part) : part));
  const fxTag = (name) => h('span', { class: 'fx' }, name);
  const svg = (paths, cls) => {
    const s = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    s.setAttribute('viewBox', '0 0 16 16');
    s.setAttribute('width', '14');
    s.setAttribute('height', '14');
    s.setAttribute('aria-hidden', 'true');
    if (cls) s.setAttribute('class', cls);
    s.innerHTML = paths;
    return s;
  };
  const ICON_TICK = '<path d="M3.5 8.5l3 3 6-7" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>';
  const ICON_CHEV = '<path d="M6 3.5L10.5 8 6 12.5" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>';

  function announce(text) {
    const n = $('announce');
    n.textContent = '';
    setTimeout(() => { n.textContent = text; }, 30);
  }

  // --- preferences (per viewer; the page works without storage) ---
  const prefs = { width: 'chat', theme: 'auto', model: false };
  try {
    const saved = JSON.parse(localStorage.getItem('midpoint-views-demo') || '{}');
    if (WIDTHS.some((w) => w.id === saved.width)) prefs.width = saved.width;
    if (THEMES.some((t) => t.id === saved.theme)) prefs.theme = saved.theme;
    if (typeof saved.model === 'boolean') prefs.model = saved.model;
  } catch (e) {
    // storage blocked: defaults
  }
  const savePrefs = () => {
    try {
      localStorage.setItem('midpoint-views-demo', JSON.stringify(prefs));
    } catch (e) {
      // not kept
    }
  };

  const mq = window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null;
  const resolvedTheme = () => (prefs.theme === 'auto' ? (mq && mq.matches ? 'dark' : 'light') : prefs.theme);
  const widthPx = () => WIDTHS.find((w) => w.id === prefs.width).px;
  const scenarioById = (id) => DATA.scenarios.find((s) => s.id === id);

  // --- one scenario: the chat and its view ---
  let session = null;

  class Session {
    constructor(scn) {
      this.scn = scn;
      this.view = DATA.views[scn.view];
      this.entry = DATA.fixtures[scn.entry];
      this.state = '';
      this.initializeAnswered = false;
      this.initialized = false;
      this.displayMode = 'inline';
      this.sentTheme = null;
      this.seenIds = new Set();
      this.asks = []; // write calls waiting for the person's answer
      this.asking = null;
      this.timers = new Set();
      this.dead = false;
      this.build();
    }

    later(fn, ms) {
      const t = setTimeout(() => {
        this.timers.delete(t);
        if (!this.dead) fn();
      }, ms);
      this.timers.add(t);
    }

    dispose() {
      this.dead = true;
      for (const t of this.timers) clearTimeout(t);
      this.timers.clear();
      $('perm-slot').textContent = '';
    }

    // --- the transcript ---
    add(node) {
      $('transcript').append(node);
      return node;
    }

    build() {
      const { scn, view, entry } = this;
      $('transcript').textContent = '';
      $('perm-slot').textContent = '';
      renderIntro(scn);

      this.add(h('li', { class: 'msg-user' }, h('span', { class: 'who' }, 'You'), h('div', { class: 'bubble' }, scn.user)));

      const panelId = nextId('call');
      const chip = h(
        'button',
        { type: 'button', class: 'chip', 'aria-expanded': 'false', 'aria-controls': panelId },
        svg(ICON_TICK, 'tick'),
        h('span', { class: 'srv' }, 'midpoint'),
        h('span', { 'aria-hidden': 'true', class: 'srv' }, '·'),
        code(entry.call.name),
        svg(ICON_CHEV, 'chev'),
      );
      const panel = h(
        'div',
        { class: 'chip-panel', id: panelId, hidden: true },
        h('span', { class: 'lab' }, 'Arguments'),
        h('pre', { class: 'json' }, JSON.stringify(entry.call.arguments ?? {}, null, 2)),
        h('span', { class: 'lab' }, 'Answered from'),
        h('p', { class: 'about' }, fxTag(this.scn.entry), ' ', entry.about),
        h('span', { class: 'lab', style: 'margin-top:10px' }, 'Shown as'),
        h('p', { class: 'about' }, code(view.uri)),
      );
      chip.addEventListener('click', () => {
        const open = chip.getAttribute('aria-expanded') === 'true';
        chip.setAttribute('aria-expanded', String(!open));
        panel.hidden = open;
      });
      this.add(
        h(
          'li',
          { class: 'msg-assistant' },
          h('span', { class: 'avatar', 'aria-hidden': 'true' }, 'A'),
          h('div', { class: 'body' }, h('span', { class: 'sr-only' }, 'Assistant: '), h('p', null, scn.assistant), h('div', { class: 'toolcall' }, chip, panel)),
        ),
      );

      // The view: its document as the server serves it, in a sandboxed frame.
      const frame = h('iframe', { sandbox: 'allow-scripts', title: `${view.title}, an MCP Apps view` });
      frame.srcdoc = view.html;
      this.frame = frame;
      this.modeTag = h('span', { class: 'mode-tag' }, 'inline');
      this.block = h('div', { class: 'viewblock' }, frame);
      this.add(
        h(
          'li',
          { class: 'view-li' },
          this.block,
          h('p', { class: 'viewcap' }, h('span', null, 'MCP Apps view'), code(view.uri), h('span', { 'aria-hidden': 'true' }, '·'), this.modeTag),
          modelPanel(['from the ', code(entry.call.name), ' result'], textOf(entry.result)),
        ),
      );
      this.layout();
    }

    // event adds a host note under the view.
    event(kind, ...kids) {
      return this.add(h('li', { class: `event ${kind}` }, ...kids));
    }

    // --- the bridge ---
    post(msg) {
      if (this.dead || !this.frame.contentWindow) return;
      this.frame.contentWindow.postMessage(msg, '*'); // the view's origin is opaque
    }
    respond(id, result) {
      this.post({ jsonrpc: '2.0', id, result });
    }
    respondError(id, code, message) {
      this.post({ jsonrpc: '2.0', id, error: { code, message } });
    }
    notify(method, params) {
      if (!this.initialized) return false; // nothing before initialized
      this.post({ jsonrpc: '2.0', method, params: params ?? {} });
      return true;
    }

    hostContext() {
      const lang = navigator.language || '';
      let timeZone;
      try {
        timeZone = Intl.DateTimeFormat().resolvedOptions().timeZone;
      } catch (e) {
        timeZone = undefined;
      }
      return {
        theme: resolvedTheme(),
        displayMode: this.displayMode,
        availableDisplayModes: ['inline', 'fullscreen'],
        containerDimensions: { width: this.frame.clientWidth || widthPx(), maxHeight: 4000 },
        locale: /^en(-|$)/i.test(lang) ? lang : 'en-GB',
        timeZone,
        platform: 'web',
        toolInfo: { id: 'call-1', tool: { name: this.entry.call.name, inputSchema: { type: 'object' } } },
        styles: { variables: { '--font-sans': HOST_FONT } },
      };
    }

    onMessage(msg) {
      if (!msg || typeof msg !== 'object' || msg.jsonrpc !== '2.0') return;
      if (msg.method === undefined) return; // this host sends no requests
      if (msg.id !== undefined && msg.id !== null) {
        const key = JSON.stringify(msg.id);
        if (this.seenIds.has(key)) return;
        this.seenIds.add(key);
        if (!this.initializeAnswered && msg.method !== 'ui/initialize') {
          this.respondError(msg.id, -32002, 'Not initialized');
          return;
        }
        this.onRequest(msg);
      } else {
        this.onNotification(msg);
      }
    }

    onRequest(msg) {
      const p = msg.params && typeof msg.params === 'object' ? msg.params : {};
      switch (msg.method) {
        case 'ui/initialize':
          this.initializeAnswered = true;
          this.respond(msg.id, {
            protocolVersion: PROTOCOL,
            hostInfo: { name: 'midpoint-views-demo-host', version: '1.0.0' },
            hostCapabilities: clone(CAPS),
            hostContext: this.hostContext(),
          });
          return;
        case 'tools/call':
          this.toolCall(msg.id, p);
          return;
        case 'ui/open-link':
          this.respond(msg.id, {});
          this.event(
            'plain',
            h('span', null, 'The view asked to open ', code(String(p.url ?? '')), '. A chat app would open it in a new tab; this demo does not.'),
          );
          return;
        case 'ui/message':
          this.respond(msg.id, {});
          this.viewMessage(p);
          return;
        case 'ui/update-model-context':
          this.respond(msg.id, {});
          this.contextUpdate(p);
          return;
        case 'ui/request-display-mode': {
          const want = p.mode;
          if (want === 'inline' || want === 'fullscreen') {
            if (want !== this.displayMode) {
              this.displayMode = want;
              this.layout();
              this.event(
                'plain',
                h('span', null, want === 'fullscreen' ? `The view asked for fullscreen: the host widened it to ${FULL_PX} px.` : 'The view went back inline.'),
              );
            }
          }
          this.respond(msg.id, { mode: this.displayMode });
          return;
        }
        default:
          this.respondError(msg.id, -32601, `Method not found: ${msg.method}`);
      }
    }

    onNotification(msg) {
      switch (msg.method) {
        case 'ui/notifications/initialized':
          if (this.initialized) return;
          this.initialized = true;
          this.sentTheme = resolvedTheme();
          this.later(() => {
            this.notify('ui/notifications/tool-input', { arguments: clone(this.entry.call.arguments ?? {}) });
            this.later(() => this.notify('ui/notifications/tool-result', clone(this.entry.result)), RESULT_DELAY);
          }, 0);
          return;
        case 'ui/notifications/size-changed': {
          const height = msg.params && msg.params.height;
          if (typeof height === 'number' && height > 0) this.frame.style.height = `${Math.max(120, Math.ceil(height))}px`;
          return;
        }
        default:
        // nothing else to do
      }
    }

    themeChanged() {
      const theme = resolvedTheme();
      if (!this.initialized || theme === this.sentTheme) return;
      this.sentTheme = theme;
      this.notify('ui/notifications/host-context-changed', { theme });
    }

    layout() {
      if (!this.block) return;
      if (this.displayMode === 'fullscreen') {
        const col = $('col');
        const avail = document.documentElement.clientWidth - 32;
        const w = Math.max(280, Math.min(FULL_PX, avail));
        this.block.style.width = `${w}px`;
        this.block.style.marginLeft = `${Math.round((col.clientWidth - w) / 2)}px`;
        this.block.classList.add('full');
      } else {
        this.block.style.width = '';
        this.block.style.marginLeft = '';
        this.block.classList.remove('full');
      }
      this.modeTag.textContent = this.displayMode;
    }

    // --- tool calls ---
    find(name, args) {
      const list = this.scn.answers[name] || [];
      return list.find((a) => (a.state === undefined || a.state === this.state) && subset(a.when, args)) || null;
    }

    toolCall(id, p) {
      const name = typeof p.name === 'string' ? p.name : '';
      const args = p.arguments && typeof p.arguments === 'object' ? p.arguments : {};
      if (WRITE.has(name)) {
        this.asks.push({ id, name, args });
        if (!this.asking) this.askNext();
        return;
      }
      const a = this.find(name, args);
      const body = h(
        'div',
        { class: 'ev-body' },
        h('span', { class: 'lab' }, 'Arguments'),
        h('pre', { class: 'json' }, JSON.stringify(args, null, 2)),
        a ? h('p', null, 'Answered from ', fxTag(a.fx), ' ', DATA.fixtures[a.fx].about) : h('p', null, 'No recorded answer: the host answered with an error.'),
      );
      this.event(
        'ev-read',
        h('details', null, h('summary', null, h('span', null, 'The view read ', code(name), ': read-only, so the host did not ask.')), body),
      );
      if (!a) {
        this.later(() => this.respondError(id, -32603, `No recorded answer for ${name} in this demo`), READ_DELAY);
        return;
      }
      this.later(() => this.respond(id, clone(DATA.fixtures[a.fx].result)), READ_DELAY);
    }

    // askNext shows the permission prompt for the next waiting write.
    askNext() {
      const call = this.asks.shift();
      this.asking = call || null;
      const slot = $('perm-slot');
      slot.textContent = '';
      if (!call) return;
      const titleId = nextId('perm-title');
      const descId = nextId('perm-desc');
      const names = Object.keys(call.args).filter((k) => isNameKey(k, call.args[k]));
      const deny = h('button', { type: 'button', class: 'btn' }, 'Deny');
      const allow = h('button', { type: 'button', class: 'btn btn-primary' }, 'Allow');
      const card = h(
        'section',
        { class: 'perm', role: 'alertdialog', 'aria-modal': 'false', 'aria-labelledby': titleId, 'aria-describedby': descId, tabindex: '-1' },
        this.asks.length ? h('p', { class: 'perm-queue' }, `${this.asks.length} more waiting after this one`) : null,
        h('h2', { id: titleId }, 'Allow the midpoint server to run ', code(call.name), '?'),
        h('p', { class: 'from', id: descId }, 'The ', h('b', null, this.view.title), ' view asks to run it. It changes data in midPoint.'),
        argList(call.args),
        names.length
          ? h(
              'p',
              { class: 'why' },
              names.map((k, i) => [i ? (i === names.length - 1 ? ' and ' : ', ') : '', code(k)]),
              names.length > 1 ? ' are midPoint names' : ' is a midPoint name',
              ', sent next to the IDs so you can see what you allow; the server checks them against the IDs before it writes.',
            )
          : null,
        h('details', null, h('summary', null, 'Arguments as JSON'), h('pre', { class: 'json' }, JSON.stringify(call.args, null, 2))),
        h('div', { class: 'perm-actions' }, deny, allow),
      );
      deny.addEventListener('click', () => this.answerAsk(call, false));
      allow.addEventListener('click', () => this.answerAsk(call, true));
      slot.append(card);
      card.focus({ preventScroll: true });
      announce(`Permission needed: allow the midpoint server to run ${call.name}?`);
    }

    answerAsk(call, allowed) {
      if (this.asking !== call) return;
      const details = (summary, ...rest) =>
        h('details', null, h('summary', null, summary), h('div', { class: 'ev-body' }, argList(call.args), ...rest));
      if (!allowed) {
        this.event(
          'ev-denied',
          details(h('span', null, h('b', null, 'Denied'), ' ', code(call.name), ': the host answered the view with an error.')),
        );
        this.later(() => this.respondError(call.id, -32000, 'The user denied this tool call.'), 120);
      } else {
        const a = this.find(call.name, call.args);
        if (a) {
          if (a.set !== undefined) this.state = a.set;
          const fx = DATA.fixtures[a.fx];
          this.event(
            'ev-allowed',
            details(
              h('span', null, h('b', null, 'Allowed'), ' ', code(call.name), ': answered from ', fxTag(a.fx)),
              h('p', null, fx.about),
              h('span', { class: 'lab' }, 'Result text'),
              h('pre', { class: 'json' }, textOf(fx.result)),
            ),
          );
          this.later(() => this.respond(call.id, clone(fx.result)), WRITE_DELAY);
        } else {
          this.event(
            'ev-unrecorded',
            details(
              h('span', null, h('b', null, 'Allowed'), ' ', code(call.name), ', but this demo has no recording for these arguments'),
              h('p', null, 'So the host answered with an error, which the view reads as a refusal. Recorded in this scenario: ', ticks(this.scn.recorded), '.'),
            ),
          );
          this.later(() => this.respondError(call.id, -32603, 'No recorded answer for this call in the demo.'), WRITE_DELAY);
        }
      }
      // Give focus back to the view, so the focus it moves to its outcome lands.
      this.frame.focus();
      this.askNext();
    }

    // --- what the view tells the chat ---
    viewMessage(p) {
      const text = arr(p.content)
        .filter((c) => c && c.type === 'text' && typeof c.text === 'string')
        .map((c) => c.text)
        .join('\n');
      this.add(h('li', { class: 'msg-user' }, h('span', { class: 'who' }, 'Sent by the view (ui/message)'), h('div', { class: 'bubble' }, text || '(no text)')));
      const next = this.scn.next ? scenarioById(this.scn.next) : null;
      this.event(
        'plain',
        h(
          'div',
          null,
          h('span', null, 'A chat app would now pass this to the assistant as your next message; this demo stops here.'),
          next
            ? h('div', { class: 'goto' }, h('button', { type: 'button', class: 'btn-link', onclick: () => go(next.id, true) }, `Continue with the next scenario: ${next.title}`))
            : null,
        ),
      );
    }

    contextUpdate(p) {
      const text = arr(p.content)
        .filter((c) => c && c.type === 'text' && typeof c.text === 'string')
        .map((c) => c.text)
        .join('\n');
      const shown = text || (p.structuredContent !== undefined ? JSON.stringify(p.structuredContent, null, 2) : '');
      this.event(
        'ev-context',
        h('div', null, h('span', null, 'The view updated what the assistant knows (', code('ui/update-model-context'), ').')),
        modelPanel('from this update', shown),
      );
    }
  }

  // --- helpers ---
  function arr(v) {
    return Array.isArray(v) ? v : [];
  }
  function subset(want, got) {
    if (!want) return true;
    for (const [k, v] of Object.entries(want)) {
      if (JSON.stringify(v) !== JSON.stringify(got ? got[k] : undefined)) return false;
    }
    return true;
  }
  function textOf(result) {
    return arr(result && result.content)
      .filter((c) => c && c.type === 'text' && typeof c.text === 'string')
      .map((c) => c.text)
      .join('\n\n');
  }
  function isNameKey(k, v) {
    return typeof v === 'string' && /Name$/.test(k);
  }
  function modelPanel(source, text) {
    return h(
      'div',
      { class: 'model' },
      h('p', { class: 'model-head' }, h('span', { class: 'lab' }, 'What the model reads'), h('span', { class: 'model-src' }, source)),
      h('pre', null, text || '(no text)'),
    );
  }
  // argList shows a call's arguments as the host's prompt does: one line
  // each, names marked, IDs in a fixed-width face.
  function argList(args) {
    const dl = h('dl', { class: 'args' });
    for (const [k, v] of Object.entries(args)) {
      const text = typeof v === 'string' ? v : JSON.stringify(v);
      const isName = isNameKey(k, v);
      const isId = typeof v === 'string' && (UUID.test(v) || /(Oid|Id)$/.test(k));
      dl.append(
        h('dt', null, k),
        h('dd', { class: isName ? 'name' : isId ? 'id' : '' }, text, isName ? h('span', { class: 'namepill' }, 'midPoint name') : null),
      );
    }
    if (!dl.childNodes.length) dl.append(h('dt', null, '(none)'), h('dd', null, ''));
    return dl;
  }

  // --- page controls ---
  function renderIntro(scn) {
    const i = DATA.scenarios.indexOf(scn) + 1;
    const intro = $('intro');
    intro.textContent = '';
    intro.append(
      h('h2', null, `Scenario ${i} of ${DATA.scenarios.length}: ${scn.title}`),
      h('p', null, h('b', null, 'You are '), scn.persona, '. ', h('b', null, 'Try: '), ticks(scn.try)),
      h('p', null, h('b', null, 'Recorded writes: '), ticks(scn.recorded), '. Read-only calls are answered without asking.'),
    );
  }

  function radioGroup(container, items, current, onPick) {
    container.textContent = '';
    const buttons = items.map((it) => {
      const b = h('button', { type: 'button', role: 'radio', 'aria-checked': String(it.id === current()), tabindex: it.id === current() ? '0' : '-1' }, it.label, it.px ? h('small', null, `${it.px}`) : null);
      b.addEventListener('click', () => pick(it.id, false));
      b.addEventListener('keydown', (e) => {
        const idx = items.indexOf(it);
        let to = -1;
        if (e.key === 'ArrowRight' || e.key === 'ArrowDown') to = (idx + 1) % items.length;
        if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') to = (idx - 1 + items.length) % items.length;
        if (to < 0) return;
        e.preventDefault();
        pick(items[to].id, true);
      });
      return b;
    });
    function pick(id, focus) {
      onPick(id);
      buttons.forEach((b, i) => {
        const on = items[i].id === id;
        b.setAttribute('aria-checked', String(on));
        b.tabIndex = on ? 0 : -1;
        if (on && focus) b.focus();
      });
    }
    container.append(...buttons);
  }

  function applyWidth() {
    document.documentElement.style.setProperty('--vw', `${widthPx()}px`);
    if (session) session.layout();
  }
  function applyTheme() {
    if (prefs.theme === 'auto') document.documentElement.removeAttribute('data-theme');
    else document.documentElement.setAttribute('data-theme', prefs.theme);
    if (session) session.themeChanged();
  }
  function applyModel() {
    document.body.classList.toggle('model-on', prefs.model);
    $('model-reads').setAttribute('aria-checked', String(prefs.model));
  }

  function go(id, focusChat) {
    const scn = scenarioById(id) || DATA.scenarios[0];
    if (session) session.dispose();
    $('scenario').value = scn.id;
    try {
      history.replaceState(null, '', `#${scn.id}`);
    } catch (e) {
      // a page opened from disk may refuse; the scenario still runs
    }
    session = new Session(scn);
    if (focusChat) {
      $('chat').focus({ preventScroll: true });
      window.scrollTo({ top: 0 });
    }
  }

  window.addEventListener('message', (ev) => {
    if (session && session.frame && ev.source === session.frame.contentWindow) session.onMessage(ev.data);
  });
  window.addEventListener('resize', () => session && session.layout());
  if (mq) {
    const onScheme = () => prefs.theme === 'auto' && applyTheme();
    if (mq.addEventListener) mq.addEventListener('change', onScheme);
    else if (mq.addListener) mq.addListener(onScheme);
  }

  const select = $('scenario');
  DATA.scenarios.forEach((s, i) => select.append(h('option', { value: s.id }, `${i + 1}. ${s.title}`)));
  select.addEventListener('change', () => go(select.value, false));
  radioGroup($('width'), WIDTHS, () => prefs.width, (id) => {
    prefs.width = id;
    savePrefs();
    applyWidth();
  });
  radioGroup($('theme'), THEMES, () => prefs.theme, (id) => {
    prefs.theme = id;
    savePrefs();
    applyTheme();
  });
  $('model-reads').addEventListener('click', () => {
    prefs.model = !prefs.model;
    savePrefs();
    applyModel();
  });
  $('restart').addEventListener('click', () => session && go(session.scn.id, false));

  applyWidth();
  applyTheme();
  applyModel();
  go((location.hash || '').slice(1), false);
})();
