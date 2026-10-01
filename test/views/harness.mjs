// Harness plumbing: finding Playwright, opening a view in the fake host, and
// the helpers checks use to look at it the way a person or a screen reader
// would (roles, accessible names, visible text), never by CSS class or id.

import { createRequire } from 'node:module';
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { hostZone } from './derive.mjs';

export const HOST_ORIGIN = 'https://host.test';
export const HOST_URL = `${HOST_ORIGIN}/host.html`;
export const VIEW_URL = 'https://view.sandbox.test/view.html';
// The CSP a host must apply when a view declares none (contract 3.2).
export const DEFAULT_CSP =
  "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; media-src 'self' data:; connect-src 'none';";

const here = new URL('.', import.meta.url);
const read = (name) => readFileSync(new URL(name, here), 'utf8');

// --- Playwright ---

// loadPlaywright returns the Playwright module: PLAYWRIGHT_MODULE when set
// (a path to node_modules/playwright or playwright-core), else the newest
// copy in the npx cache whose Chromium is installed, else a regular install.
export function loadPlaywright() {
  const require = createRequire(import.meta.url);
  const usable = (m) => {
    try {
      return m?.chromium && existsSync(m.chromium.executablePath());
    } catch {
      return false;
    }
  };
  if (process.env.PLAYWRIGHT_MODULE) {
    const m = require(process.env.PLAYWRIGHT_MODULE);
    if (!m.chromium) throw new Error(`PLAYWRIGHT_MODULE=${process.env.PLAYWRIGHT_MODULE} has no chromium`);
    return { pw: m, from: process.env.PLAYWRIGHT_MODULE };
  }
  const roots = [process.env.npm_config_cache, join(homedir(), '.npm')].filter(Boolean).map((r) => join(r, '_npx'));
  const found = [];
  for (const root of roots) {
    if (!existsSync(root)) continue;
    for (const d of readdirSync(root)) {
      for (const pkg of ['playwright', 'playwright-core']) {
        const dir = join(root, d, 'node_modules', pkg);
        try {
          const { version } = JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8'));
          found.push({ dir, version });
        } catch {
          // not there
        }
      }
    }
  }
  const key = (v) => v.split(/[.-]/).map((n) => n.padStart(6, '0')).join('.');
  found.sort((a, b) => (key(a.version) < key(b.version) ? 1 : -1));
  for (const f of found) {
    try {
      const m = require(f.dir);
      if (usable(m)) return { pw: m, from: `${f.dir} (${f.version})` };
    } catch {
      // try the next one
    }
  }
  for (const name of ['playwright', 'playwright-core']) {
    try {
      const m = require(name);
      if (usable(m)) return { pw: m, from: name };
    } catch {
      // not installed
    }
  }
  throw new Error(
    'Playwright not found. Set PLAYWRIGHT_MODULE=/path/to/node_modules/playwright, or run `npx playwright install chromium` once so the npx cache has it.',
  );
}

// --- what a host offers ---

export const ALL_CAPS = { serverTools: {}, openLinks: {}, message: {}, updateModelContext: {} };

// The inbox's tool allowlist and arguments (contract 7.1).
export const INBOX_TOOLS = {
  list_work_items: ['limit'],
  decide_work_item: ['caseOid', 'workItemId', 'decision', 'comment'],
  get_case: ['oid'],
  whoami: [],
};

export function hostContext(entryTool, zone, over = {}) {
  return {
    theme: 'light',
    displayMode: 'inline',
    availableDisplayModes: ['inline', 'fullscreen'],
    containerDimensions: { maxHeight: 1600, width: 720 },
    locale: 'en-GB',
    timeZone: zone.timeZone,
    platform: 'web',
    toolInfo: { id: 'call-1', tool: { name: entryTool, inputSchema: { type: 'object' } } },
    ...over,
  };
}

// --- one opened view ---

// Diagnostics of every view opened in this run, for the run-wide checks.
export const diagnostics = [];

// Answers every scenario has unless it says otherwise (checks.mjs sets them).
// A suite sets its view's tool allowlist here (allowedTools); the inbox's is the fallback.
export const defaults = { tools: {}, allowedTools: null };

// inLiveRegion reports whether text is announced: it sits in a live region
// of the given politeness, or such a region anywhere in the view holds it.
export function inLiveRegion(frame, text, politeness) {
  return frame.evaluate(([t, p]) => {
    const sel = p === 'assertive' ? '[aria-live="assertive"],[role="alert"]' : '[aria-live="polite"],[role="status"],[role="log"],output';
    const norm = (s) => s.replace(/\s+/g, ' ').trim();
    return [...document.querySelectorAll(sel)].some((n) => norm(n.textContent).includes(norm(t)));
  }, [text, politeness]);
}

export class View {
  constructor({ page, frame, context, routed, console, errors, check, config }) {
    Object.assign(this, { page, frame, context, routed, console, errors, check, config });
  }

  // --- host side ---
  log() {
    return this.page.evaluate(() => window.__host.log());
  }
  state() {
    return this.page.evaluate(() => window.__host.state());
  }
  async calls(name) {
    return (await this.log())
      .filter((e) => e.dir === 'view>host' && e.msg.method === 'tools/call' && (!name || e.msg.params?.name === name))
      .map((e) => ({ name: e.msg.params?.name, args: e.msg.params?.arguments, seq: e.seq, t: e.t }));
  }
  async sent(method) {
    return (await this.log()).filter((e) => e.dir === 'view>host' && e.msg.method === method).map((e) => e.msg);
  }
  notify(method, params) {
    return this.page.evaluate(([m, p]) => window.__host.notify(m, p), [method, params]);
  }
  request(method, params) {
    return this.page.evaluate(([m, p]) => window.__host.request(m, p), [method, params]);
  }
  release() {
    return this.page.evaluate(() => window.__host.release());
  }
  heldCount() {
    return this.page.evaluate(() => window.__host.heldCount());
  }
  setTools(name, answers) {
    return this.page.evaluate(([n, a]) => window.__host.setTools(n, a), [name, answers]);
  }
  setTheme(theme) {
    return this.page.evaluate((t) => window.__host.setTheme(t), theme);
  }
  instrumented() {
    return this.frame.evaluate(() => JSON.parse(JSON.stringify(window.__harness ?? null)));
  }

  // --- view side ---
  text(locator) {
    return (locator ?? this.frame.locator('body')).innerText();
  }
  button(name, exact = true) {
    return this.frame.getByRole('button', { name, exact });
  }
  async visible(locator) {
    try {
      return await locator.first().isVisible();
    } catch {
      return false;
    }
  }
  // waitFor waits until a locator (or text) is visible; false on timeout.
  async waitFor(target, timeout = 2500) {
    const loc = typeof target === 'string' || target instanceof RegExp ? this.frame.getByText(target, typeof target === 'string' ? { exact: true } : undefined) : target;
    try {
      await loc.first().waitFor({ state: 'visible', timeout });
      return true;
    } catch {
      return false;
    }
  }
  async waitGone(locator, timeout = 2500) {
    try {
      await locator.first().waitFor({ state: 'hidden', timeout });
      return true;
    } catch {
      return false;
    }
  }
  // hasText reports whether the visible text has a line equal to s (or
  // matching a regex), or contains s when loose.
  async hasText(s, { within, loose = false } = {}) {
    const text = await this.text(within);
    if (s instanceof RegExp) return s.test(text);
    if (loose) return norm(text).includes(norm(s));
    return lines(text).includes(norm(s)) || norm(text).includes(norm(s));
  }

  // card finds the card of a work item by its title: the innermost list
  // item or article that holds the title.
  async card(e, timeout = 2500) {
    const f = this.frame;
    const title = f
      .getByRole('heading', { name: e.label, exact: true })
      .or(f.getByLabel(e.label, { exact: true }))
      .or(f.getByText(e.title, { exact: true }));
    if (!(await this.waitFor(title, timeout))) return null;
    for (const role of ['listitem', 'article']) {
      const c = f.getByRole(role).filter({ has: title });
      const n = await c.count();
      if (n > 0) return c.nth(n - 1);
    }
    // No list item or article: tag the nearest ancestor with a button.
    const tag = `c${Math.random().toString(36).slice(2, 8)}`;
    const ok = await title.first().evaluate((el, t) => {
      let n = el;
      while (n && n !== document.body) {
        if (n.querySelector('button')) {
          n.setAttribute('data-harness-card', t);
          return true;
        }
        n = n.parentElement;
      }
      return false;
    }, tag);
    return ok ? f.locator(`[data-harness-card="${tag}"]`) : null;
  }

  // details opens a card's Details and returns the toggle and its region.
  async details(card) {
    const toggle = card.getByRole('button', { name: /^(Details|Hide details)\b/ });
    if (!(await this.visible(toggle))) return null;
    const expanded = await toggle.first().getAttribute('aria-expanded');
    if (expanded !== 'true') await toggle.first().click();
    const id = await toggle.first().getAttribute('aria-controls');
    const region = id ? this.frame.locator(`[id="${id.replace(/"/g, '\\"')}"]`) : null;
    return { toggle: toggle.first(), region };
  }

  dialog() {
    return this.frame.getByRole('dialog');
  }

  // accessibleName returns an element's accessible name from Playwright's
  // aria snapshot (`- button "Name"`).
  async accessibleName(locator) {
    const snap = await locator.first().ariaSnapshot();
    const m = /^- [\w-]+ "((?:[^"\\]|\\.)*)"/.exec(snap.trim());
    return m ? m[1].replace(/\\"/g, '"') : '';
  }

  async focused() {
    return this.frame.evaluate(() => {
      const a = document.activeElement;
      return a ? { tag: a.tagName, text: (a.innerText || a.value || '').trim(), label: a.getAttribute('aria-label') } : null;
    });
  }
  async isFocused(locator) {
    try {
      return await locator.first().evaluate((el) => el === document.activeElement);
    } catch {
      return false;
    }
  }

  // surfaceStrings is everything a person can read or hear: the visible text
  // and the names and descriptions given in attributes.
  async surfaceStrings(within) {
    const text = await this.text(within);
    const attrs = await (within ?? this.frame.locator('body')).evaluate((root) => {
      const out = [];
      for (const el of root.querySelectorAll('*')) {
        const cs = getComputedStyle(el);
        if (cs.display === 'none' || cs.visibility === 'hidden') continue;
        for (const a of ['aria-label', 'title', 'alt', 'placeholder', 'aria-description', 'aria-roledescription', 'aria-valuetext']) {
          const v = el.getAttribute(a);
          if (v) out.push(v);
        }
      }
      return out;
    });
    return [text, ...attrs].join('\n');
  }

  async close() {
    try {
      const st = await this.state();
      const inst = await this.instrumented().catch(() => null);
      diagnostics.push({ check: this.check, state: st, inst, routed: this.routed, console: this.console, errors: this.errors });
    } catch {
      // the page may be gone
    }
    await this.context.close();
  }
}

export const norm = (s) => String(s).replace(/\s+/g, ' ').trim();
export const lines = (text) => String(text).split('\n').map(norm).filter(Boolean);

// openView loads the view document in the fake host with a scenario:
//   entry          fixture whose call and result the host delivers
//   inputArgs      tool-input arguments (default: the fixture's)
//   deliver        'auto' (input then result), 'input-only', 'none'
//   resultDelayMs  delay between tool-input and tool-result
//   caps           hostCapabilities (default ALL_CAPS)
//   context        hostContext overrides
//   tools          { name: [{ when, result, rpcError, delayMs, hold, times }] }
//   echo           also deliver each answered view call as tool-result
//   width          iframe width
export async function openView(browser, viewDoc, check, s = {}) {
  const zone = s.zone ?? hostZone();
  const entry = s.entry;
  const config = {
    viewUrl: VIEW_URL,
    width: s.width ?? 720,
    height: s.height ?? 1600,
    followSize: s.followSize ?? false,
    hostCapabilities: s.caps ?? ALL_CAPS,
    hostContext: hostContext(entry?.call?.name ?? 'list_work_items', zone, s.context),
    entry: entry ? { arguments: s.inputArgs ?? entry.call.arguments, result: entry.result } : null,
    deliver: s.deliver ?? (entry ? 'auto' : 'none'),
    resultDelayMs: s.resultDelayMs ?? 0,
    tools: { ...defaults.tools, ...(s.tools ?? {}) },
    echo: s.echo ?? false,
    allowedTools: s.allowedTools ?? defaults.allowedTools ?? INBOX_TOOLS,
  };
  const context = await browser.newContext({
    locale: 'en-GB',
    timezoneId: zone.timeZone,
    viewport: { width: (s.width ?? 720) + 64, height: 1000 },
    colorScheme: config.hostContext.theme === 'dark' ? 'dark' : 'light',
    reducedMotion: 'reduce',
  });
  await context.addInitScript({ content: read('instrument.js') });
  const routed = [];
  await context.route('**/*', (route) => {
    const req = route.request();
    const url = req.url();
    if (url === HOST_URL) return route.fulfill({ contentType: 'text/html; charset=utf-8', body: read('host.html') });
    if (url === `${HOST_ORIGIN}/host.js`) return route.fulfill({ contentType: 'text/javascript', body: read('host.js') });
    if (url === VIEW_URL) {
      return route.fulfill({
        contentType: 'text/html; charset=utf-8',
        headers: { 'content-security-policy': DEFAULT_CSP },
        body: viewDoc,
      });
    }
    let frameUrl = '';
    try {
      frameUrl = req.frame().url();
    } catch {
      // no frame (e.g. a service worker)
    }
    routed.push({ url, frameUrl, type: req.resourceType() });
    return route.abort('blockedbyclient');
  });
  const page = await context.newPage();
  const consoleMsgs = [];
  const errors = [];
  page.on('console', (m) => {
    if (m.type() === 'error' || m.type() === 'warning') {
      consoleMsgs.push({ type: m.type(), text: m.text(), url: m.location()?.url ?? '' });
    }
  });
  page.on('pageerror', (e) => errors.push(String(e.message ?? e)));
  await page.goto(HOST_URL);
  await page.evaluate((c) => window.__host.start(c), config);
  let frame = null;
  for (let i = 0; i < 100 && !frame; i++) {
    frame = page.frames().find((f) => f.url() === VIEW_URL) ?? null;
    if (!frame) await page.waitForTimeout(20);
  }
  if (!frame) throw new Error('the view frame never loaded');
  await frame.waitForLoadState('domcontentloaded');
  return new View({ page, frame, context, routed, console: consoleMsgs, errors, check, config: { ...config, zone } });
}

// --- one check ---

export class T {
  constructor(id, browser, viewDoc, screenshots) {
    Object.assign(this, { id, browser, viewDoc, screenshots });
    this.failures = [];
    this.views = [];
  }
  ok(cond, reason) {
    if (!cond) this.failures.push(reason);
    return !!cond;
  }
  fail(reason) {
    this.failures.push(reason);
    return false;
  }
  async open(s) {
    const v = await openView(this.browser, this.viewDoc, this.id, s);
    this.views.push(v);
    return v;
  }
  async shot(view, name) {
    if (!this.screenshots) return;
    await view.page.screenshot({ path: join(this.screenshots, `${this.id}.${name}.png`), fullPage: true });
  }
  async done() {
    for (const v of this.views) await v.close();
  }
}

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
