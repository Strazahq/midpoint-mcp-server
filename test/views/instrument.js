// Injected into every frame before its scripts run; active only in the view's
// frame. It records what contract 3.6 and 6.6 forbid or limit, without
// changing behaviour: timers (only the 8 s slow notice may wait long),
// network APIs, browser storage, popups, CSP violations and uncaught errors.
// run.mjs reads window.__harness.
(() => {
  if (location.hostname !== 'view.sandbox.test') return;
  const rec = {
    timeouts: [], // { ms, at, stack }
    intervals: [],
    frames: 0, // requestAnimationFrame calls
    network: [], // { api, target }
    storage: [], // { api }
    popups: [],
    csp: [], // { directive, blocked }
    errors: [],
  };
  Object.defineProperty(window, '__harness', { value: rec });
  // The callers of the wrapped API: skip "Error", this function and the
  // wrapper itself.
  const stack = () => (new Error().stack || '').split('\n').slice(3, 6).map((s) => s.trim().replace(/^at /, '')).join(' < ');

  const setTimeout0 = window.setTimeout;
  const setInterval0 = window.setInterval;
  const raf0 = window.requestAnimationFrame;
  window.setTimeout = function setTimeout(fn, ms, ...rest) {
    rec.timeouts.push({ ms: Number(ms) || 0, at: performance.now(), stack: stack() });
    return setTimeout0.call(this, fn, ms, ...rest);
  };
  window.setInterval = function setInterval(fn, ms, ...rest) {
    rec.intervals.push({ ms: Number(ms) || 0, at: performance.now(), stack: stack() });
    return setInterval0.call(this, fn, ms, ...rest);
  };
  window.requestAnimationFrame = function requestAnimationFrame(fn) {
    rec.frames++;
    return raf0.call(this, fn);
  };

  const note = (api, target) => rec.network.push({ api, target: String(target ?? ''), stack: stack() });
  const fetch0 = window.fetch;
  window.fetch = function fetch(input, init) {
    note('fetch', input && input.url ? input.url : input);
    return fetch0.call(this, input, init);
  };
  const open0 = XMLHttpRequest.prototype.open;
  XMLHttpRequest.prototype.open = function open(method, url, ...rest) {
    note('XMLHttpRequest', url);
    return open0.call(this, method, url, ...rest);
  };
  for (const name of ['WebSocket', 'EventSource']) {
    const Orig = window[name];
    if (!Orig) continue;
    window[name] = new Proxy(Orig, {
      construct(target, args) {
        note(name, args[0]);
        return Reflect.construct(target, args);
      },
    });
  }
  if (navigator.sendBeacon) {
    const beacon0 = navigator.sendBeacon.bind(navigator);
    navigator.sendBeacon = (url, data) => {
      note('sendBeacon', url);
      return beacon0(url, data);
    };
  }
  const winOpen0 = window.open;
  window.open = function open(url, ...rest) {
    rec.popups.push(String(url));
    return winOpen0.call(this, url, ...rest);
  };

  // Browser storage (contract 3.6: none). In an opaque origin these throw;
  // the attempt is what counts.
  const watch = (owner, prop, api) => {
    let proto = owner;
    while (proto && !Object.getOwnPropertyDescriptor(proto, prop)) proto = Object.getPrototypeOf(proto);
    const d = proto && Object.getOwnPropertyDescriptor(proto, prop);
    if (!d || !d.configurable) return;
    Object.defineProperty(owner, prop, {
      configurable: true,
      enumerable: d.enumerable,
      get() {
        rec.storage.push({ api, stack: stack() });
        return d.get ? d.get.call(this) : d.value;
      },
      set(v) {
        rec.storage.push({ api: `${api} (write)`, stack: stack() });
        if (d.set) d.set.call(this, v);
      },
    });
  };
  watch(window, 'localStorage', 'localStorage');
  watch(window, 'sessionStorage', 'sessionStorage');
  watch(window, 'indexedDB', 'indexedDB');
  watch(document, 'cookie', 'document.cookie');

  document.addEventListener('securitypolicyviolation', (e) =>
    rec.csp.push({ directive: e.violatedDirective, blocked: e.blockedURI }),
  );
  window.addEventListener('error', (e) => rec.errors.push(String(e.message || e.type)));
  window.addEventListener('unhandledrejection', (e) => rec.errors.push(`unhandled rejection: ${String(e.reason)}`));
})();
