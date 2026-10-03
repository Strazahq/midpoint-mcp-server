// The checks: each acceptance criterion of contract 7.1 (and the shared
// criteria, the owner decision D37, and the parts of chapters 3, 5 and 6 the
// inbox relies on) as one or more named checks. A check opens the view in
// the fake host with a scenario, acts like a person would, and reports what
// doesn't hold. Elements are found by role, accessible name and visible text
// from the string catalog (strings.mjs), never by CSS class or id.

import { S, isBodyFinal } from './strings.mjs';
import {
  fixture, derive, mutations as M, items, expectItem, loginNames, sortFixture, sortedTitles,
  localParts, clockMatches, hostZone, HOUR, DAY,
} from './derive.mjs';
import { VIEW_URL, defaults, diagnostics, inLiveRegion, lines, norm, sleep } from './harness.mjs';

// A view may call whoami whenever a result has no acting identity (6.2);
// every scenario answers it, so such a call is never "unexpected".
defaults.tools = { whoami: [{ result: fixture('whoami.approver').result }] };

export const checks = [];
// Run-wide checks look at the diagnostics of every view opened in the run.
export const runWide = [];

const check = (id, criterion, title, run) => checks.push({ id, criterion, title, run });
const across = (id, criterion, title, run) => runWide.push({ id, criterion, title, run });

const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;
const RGB = {
  danger: ['rgb(220, 53, 69)', 'rgb(248, 215, 218)'],
  warning: ['rgb(255, 193, 7)', 'rgb(133, 100, 4)', 'rgb(255, 243, 205)'],
  secondary: ['rgb(98, 115, 131)'],
  focus: 'rgb(255, 168, 54)',
};
const NO_TOOLS = { openLinks: {}, message: {}, updateModelContext: {} };
const decideTools = (name, extra = {}) => ({ decide_work_item: [{ result: fixture(name).result, ...extra }] });
const caseTools = (name = 'case.approver', extra = {}) => ({ get_case: [{ result: fixture(name).result, ...extra }] });

// --- helpers ---

// inbox opens a view on an inbox result and waits for the item's card.
async function inbox(t, entry, s = {}, which = 0) {
  const fx = typeof entry === 'string' ? fixture(entry) : entry;
  const v = await t.open({ entry: fx, ...s });
  const wi = items(fx.result)[which];
  const e = expectItem(wi);
  const card = await v.card(e);
  if (!card) t.fail(`no card titled "${e.title}" (spoken "${e.label}")`);
  return { v, fx, e, card, wi };
}

// colorsOf returns the colours an element and its nearest ancestors paint.
function colorsOf(locator) {
  return locator.first().evaluate((el) => {
    const out = [];
    for (let n = el, i = 0; n && i < 4; n = n.parentElement, i++) {
      const cs = getComputedStyle(n);
      out.push(cs.color, cs.backgroundColor, cs.borderTopColor, cs.borderLeftColor);
    }
    return out;
  });
}

// openDialog clicks a card's button and returns the dialog.
async function openDialog(t, v, card, name) {
  const btn = card.getByRole('button', { name, exact: true });
  if (!(await v.visible(btn))) {
    t.fail(`no button named "${name}"`);
    return null;
  }
  await btn.first().click();
  const dlg = v.dialog();
  if (!(await v.waitFor(dlg))) {
    t.fail(`"${name}" opened no dialog (role=dialog)`);
    return null;
  }
  return { dlg, btn: btn.first() };
}

// decideFrom opens a card's dialog, fills the comment and submits.
async function decideFrom(t, v, card, buttonName, { comment, submit } = {}) {
  const d = await openDialog(t, v, card, buttonName);
  if (!d) return null;
  if (comment !== undefined) {
    const box = d.dlg.getByRole('textbox');
    if (!(await v.visible(box))) return t.fail('the dialog has no comment field'), null;
    await box.first().fill(comment);
  }
  const sub = d.dlg.getByRole('button', { name: submit, exact: true });
  if (!(await v.visible(sub))) return t.fail(`the dialog has no "${submit}" button`), null;
  await sub.first().click();
  return d;
}

const approve = (t, v, card, e, opts = {}) => decideFrom(t, v, card, e.approveLabel, { submit: S.confirm.approveSubmit, ...opts });
const reject = (t, v, card, e, opts = {}) =>
  decideFrom(t, v, card, e.rejectLabel, { submit: S.confirm.rejectSubmit, comment: 'Not needed for this project.', ...opts });

async function waitInit(v, timeout = 3000) {
  for (let i = 0; i < timeout / 50; i++) {
    if ((await v.state()).initialized) return true;
    await sleep(50);
  }
  return false;
}

// callsSettle waits a little for calls in flight to be recorded.
const settle = (ms = 300) => sleep(ms);

// stripData removes the data a result carries (names, descriptions, step
// names) from text, so a vocabulary check sees only the view's own words.
function stripData(text, ...results) {
  const strings = new Set();
  const visit = (v) => {
    if (typeof v === 'string' && v.length > 1) strings.add(v);
    else if (Array.isArray(v)) v.forEach(visit);
    else if (v && typeof v === 'object') Object.values(v).forEach(visit);
  };
  for (const r of results) visit(r?.structuredContent);
  let out = text;
  for (const s of [...strings].sort((a, b) => b.length - a.length)) out = out.split(s).join(' ');
  return out;
}

const VOCABULARY = [
  /\bwork ?items?\b/i, /\bstages?\b/i, /\bassignees?\b/i, /\brequestor\b/i, /\brequestee\b/i,
  /\bassignments?\b/i, /\bdeltas?\b/i, /\bfocus\b/i, /\borg:/i, /\brelation\b/i, /\bfirstDecides\b/,
  /\ballMust(Agree|Approve)\b/, /\boutcome\b/i, /\bcase\b/i, /\bOIDs?\b/i, /\barchetypes?\b/i,
  /\bapprovalContext\b/, /#(approve|reject)\b/, /\bresource-server\b/, /\bpersonal mode\b/i,
];
const KIND_LABELS = /\b(User|Role|Org|Service|Resource|Archetype|Case)\b/;

// ===================================================================
// Bridge and lifecycle (contract 3.4 to 3.6)
// ===================================================================

check('bridge.handshake', '3.4, 3.5', 'ui/initialize with the contract parameters, then initialized', async (t) => {
  const v = await t.open({ entry: fixture('inbox.approver') });
  t.ok(await waitInit(v), 'the view never sent ui/notifications/initialized');
  const log = await v.log();
  const first = log.find((e) => e.dir === 'view>host');
  if (!t.ok(first?.msg?.method === 'ui/initialize' && first.msg.id !== undefined, `first message is ${first?.msg?.method ?? 'nothing'}, want the ui/initialize request`)) return;
  const p = first.msg.params ?? {};
  t.ok(p.protocolVersion === '2026-01-26', `protocolVersion ${p.protocolVersion}, want 2026-01-26`);
  t.ok(/^midpoint-/.test(p.appInfo?.name ?? '') && typeof p.appInfo?.version === 'string', `appInfo ${JSON.stringify(p.appInfo)}, want name midpoint-<view> and a version`);
  t.ok(JSON.stringify(p.appCapabilities?.availableDisplayModes) === '["inline","fullscreen"]',
    `appCapabilities.availableDisplayModes ${JSON.stringify(p.appCapabilities?.availableDisplayModes)}, want ["inline","fullscreen"]`);
});

check('bridge.nothing-before-initialized', '3.5 step 3', 'the view needs no host message before it sends initialized', async (t) => {
  const v = await t.open({ entry: fixture('inbox.approver') });
  if (!t.ok(await waitInit(v), 'the view never sent initialized (it may be waiting for something the host may not send yet)')) return;
  const log = await v.log();
  const init = log.find((e) => e.dir === 'view>host' && e.msg.method === 'ui/notifications/initialized');
  const before = log.filter((e) => e.dir === 'host>view' && e.seq < init.seq && e.msg.method);
  t.ok(before.length === 0, `host sent ${before.map((e) => e.msg.method)} before initialized (harness bug)`);
});

check('bridge.size-changed', '3.4, 6.13', 'reports its content size, only on change', async (t) => {
  const { v } = await inbox(t, 'inbox.approver');
  await settle(500);
  const sizes = (await v.sent('ui/notifications/size-changed')).map((m) => m.params);
  if (!t.ok(sizes.length > 0, 'no ui/notifications/size-changed')) return;
  t.ok(sizes.every((s) => s.height > 0 && s.width > 0), `size without a positive width and height: ${JSON.stringify(sizes)}`);
  for (let i = 1; i < sizes.length; i++) {
    if (sizes[i].width === sizes[i - 1].width && sizes[i].height === sizes[i - 1].height) {
      t.fail(`the same size reported twice in a row: ${JSON.stringify(sizes[i])}`);
      break;
    }
  }
});

check('bridge.teardown', '3.4', 'ui/resource-teardown is answered with {}', async (t) => {
  const { v } = await inbox(t, 'inbox.approver');
  const res = await v.request('ui/resource-teardown', {});
  t.ok(!res.timeout, 'no answer to ui/resource-teardown within 2 s');
  t.ok(res.result && JSON.stringify(res.result) === '{}', `answer ${JSON.stringify(res)}, want result {}`);
});

check('bridge.expand', '6.13', 'Expand requests fullscreen and Exit full screen returns; no Expand without fullscreen', async (t) => {
  const { v } = await inbox(t, 'inbox.approver');
  const expand = v.button(S.expand);
  if (t.ok(await v.visible(expand), `no "${S.expand}" button`)) {
    await expand.first().click();
    await settle();
    const req = await v.sent('ui/request-display-mode');
    t.ok(req[0]?.params?.mode === 'fullscreen', `request-display-mode ${JSON.stringify(req[0]?.params)}, want fullscreen`);
    const collapse = v.button(S.collapse);
    if (t.ok(await v.waitFor(collapse), `no "${S.collapse}" after the host granted fullscreen`)) {
      await collapse.first().click();
      await settle();
      const again = await v.sent('ui/request-display-mode');
      t.ok(again[1]?.params?.mode === 'inline', `second request ${JSON.stringify(again[1]?.params)}, want inline`);
    }
  }
  const { v: v2 } = await inbox(t, 'inbox.approver', { context: { availableDisplayModes: ['inline'] } });
  t.ok(!(await v2.visible(v2.button(S.expand))), `"${S.expand}" offered although the host has no fullscreen`);
});

check('lifecycle.result-idempotent', '3.5', 'the same tool-result delivered twice changes nothing', async (t) => {
  const { v, fx, e, card } = await inbox(t, 'inbox.manager');
  if (!card) return;
  const before = norm(await v.text());
  await v.notify('ui/notifications/tool-result', fx.result);
  await settle(400);
  const after = norm(await v.text());
  t.ok(before === after, 'the view changed when the same result arrived again');
  const n = await v.frame.getByText(e.title, { exact: true }).count();
  t.ok(n === 1, `${n} titles "${e.title}" after the repeat, want 1`);
});

check('lifecycle.ignores-foreign-result', '3.5', 'a result of a tool the view neither called nor entered with is ignored', async (t) => {
  const { v, card } = await inbox(t, 'inbox.approver');
  if (!card) return;
  const foreign = derive('inbox.empty', 'foreign-tool', (r) => { r.structuredContent.tool = 'list_my_requests'; });
  await v.notify('ui/notifications/tool-result', foreign.result);
  await settle(400);
  t.ok(await v.visible(card), 'the card disappeared after a foreign result');
  t.ok(!(await v.hasText(S.inbox.emptyTitle)), 'a foreign result emptied the inbox');
});

check('lifecycle.echoed-view-call', '3.5', 'a host that echoes a view call as tool-result does not apply it twice', async (t) => {
  const { v, e, card } = await inbox(t, 'inbox.approver', { tools: decideTools('decide.approve-next'), echo: true });
  if (!card) return;
  await approve(t, v, card, e);
  const want = S.inbox.outcome.approvedOpen;
  if (!t.ok(await v.waitFor(want), `no outcome "${want}"`)) return;
  await settle(500);
  // Count on screen only: a live region repeats the outcome for screen readers (6.12), and the
  // view's string catalog sits in its script.
  const n = await v.frame.evaluate((w) => {
    const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT, {
      acceptNode: (node) => (node.parentElement?.closest('[aria-live], script, style') ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT),
    });
    let text = '';
    for (let node = walk.nextNode(); node; node = walk.nextNode()) text += node.data;
    return text.split(w).length - 1;
  }, want);
  t.ok(n === 1, `outcome shown ${n} times`);
  t.ok((await v.sent('ui/update-model-context')).length <= 1, 'model context updated more than once for one decision');
  t.ok((await v.calls()).length === 1, `calls after an echoed result: ${JSON.stringify(await v.calls())}`);
});

// ===================================================================
// States (6.7, 7.1 States)
// ===================================================================

check('states.loading', '6.7 Loading', 'tool-input shows the inbox loading text and aria-busy', async (t) => {
  const fx = fixture('inbox.approver');
  const v = await t.open({ entry: fx, deliver: 'input-only' });
  t.ok(await v.waitFor(S.inbox.loading), `no "${S.inbox.loading}" after tool-input`);
  t.ok((await v.frame.locator('[aria-busy="true"]').count()) > 0, 'nothing is aria-busy while loading');
  await v.notify('ui/notifications/tool-result', fx.result);
  const e = expectItem(items(fx.result)[0]);
  t.ok(await v.card(e), 'the card did not appear after tool-result');
  t.ok(!(await v.hasText(S.inbox.loading)), 'the loading text stayed after the result');
});

check('states.slow-notice', '6.7 Slow, 6.5 (D27)', 'after 8 s without a result: state.slow, never an error', async (t) => {
  const v = await t.open({ entry: fixture('inbox.approver'), deliver: 'input-only' });
  await sleep(6000);
  t.ok(!(await v.hasText(S.slow)), `"${S.slow}" shown before 8 s`);
  await sleep(3000);
  t.ok(await v.hasText(S.slow), `no "${S.slow}" 9 s after tool-input`);
  const text = await v.text();
  for (const msg of Object.values(S.errorByCode)) t.ok(!text.includes(msg), `slowness shown as an error: "${msg}"`);
  t.ok((await v.calls()).length === 0, 'the view called a tool while waiting');
});

check('states.cancelled', '6.7 Cancelled', 'tool-cancelled shows state.cancelled / cancelledReason', async (t) => {
  const v = await t.open({ entry: fixture('inbox.approver'), deliver: 'input-only' });
  await sleep(200);
  await v.notify('ui/notifications/tool-cancelled', {});
  t.ok(await v.waitFor(S.cancelled), `no "${S.cancelled}"`);
  const v2 = await t.open({ entry: fixture('inbox.approver'), deliver: 'input-only' });
  await sleep(200);
  await v2.notify('ui/notifications/tool-cancelled', { reason: 'Stopped by the person' });
  t.ok(await v2.waitFor(S.cancelledReason('Stopped by the person')), 'no state.cancelledReason with the reason');
});

check('states.summary', '7.1 Layout 3', 'inbox.summary counts the requests', async (t) => {
  const { v } = await inbox(t, 'inbox.approver');
  t.ok(await v.hasText(S.inbox.summary(1)), `no "${S.inbox.summary(1)}"`);
  const { v: v2 } = await inbox(t, 'inbox.manager');
  t.ok(await v2.hasText(S.inbox.summary(2)), `no "${S.inbox.summary(2)}"`);
});

check('states.empty', '7.1 States Empty', 'empty inbox says so; in personal mode whose inbox it is', async (t) => {
  const v = await t.open({ entry: fixture('inbox.empty') });
  t.ok(await v.waitFor(S.inbox.emptyTitle), `no "${S.inbox.emptyTitle}"`);
  t.ok(!(await v.hasText(S.inbox.emptyPersonal('Dana Lee'))), 'the personal note shown in resource-server mode');
  const v2 = await t.open({ entry: fixture('inbox.empty-personal') });
  t.ok(await v2.waitFor(S.inbox.emptyTitle), `personal: no "${S.inbox.emptyTitle}"`);
  t.ok(await v2.hasText(S.inbox.emptyPersonal('Dana Lee')), `personal: no "${S.inbox.emptyPersonal('Dana Lee')}"`);
});

check('states.version-mismatch', '6.7', 'a different contract MAJOR shows state.versionMismatch and the text', async (t) => {
  const fx = derive('inbox.approver', 'version-mismatch', M.versionMismatch);
  const v = await t.open({ entry: fx });
  t.ok(await v.waitFor(S.versionMismatch), `no "${S.versionMismatch}"`);
  t.ok(await v.hasText(fx.result.content[0].text.split('\n')[0], { loose: true }), 'the text content is not shown');
});

check('states.text-only', '6.7 Unknown shape', 'no structuredContent and no error: state.textOnly and the text', async (t) => {
  const fx = derive('inbox.approver', 'text-only', M.textOnly);
  const v = await t.open({ entry: fx });
  t.ok(await v.waitFor(S.textOnly), `no "${S.textOnly}"`);
  t.ok(await v.hasText(fx.result.content[0].text.split('\n')[0], { loose: true }), 'the text content is not shown');
});

check('states.footer', '6.1, 6.6', 'the footer says As of …', async (t) => {
  const { v } = await inbox(t, 'inbox.approver');
  t.ok(await v.waitFor(S.asOf), 'no "As of …"');
  t.ok((await v.frame.locator('time[datetime]').count()) > 0, 'no <time datetime> element (6.11)');
});

check('states.read-only-host', '6.6 Read-only host', 'no serverTools: one banner, no controls that call tools, Details still open', async (t) => {
  const { v, e, card } = await inbox(t, 'inbox.approver', { caps: NO_TOOLS });
  if (!card) return;
  t.ok(await v.hasText(S.readOnlyHost), `no "${S.readOnlyHost}"`);
  t.ok(!(await v.visible(v.button(S.refresh))), 'Refresh shown on a read-only host');
  for (const name of [e.approveLabel, e.rejectLabel, e.previewApproveLabel, e.previewRejectLabel]) {
    t.ok(!(await v.visible(v.button(name))), `"${name}" shown on a read-only host`);
  }
  t.ok(await v.hasText(S.askAssistantToRefresh), `no "${S.askAssistantToRefresh}" in the footer`);
  const d = await v.details(card);
  if (t.ok(d, 'no Details toggle on a read-only host')) {
    t.ok(await v.waitFor(card.getByText(S.inbox.requestedAt)), 'Details show no requested time on a read-only host');
  }
});

check('states.read-only-wins-over-dry-run', '6.1', 'read-only and dry run: only the read-only banner', async (t) => {
  const { v, card } = await inbox(t, 'inbox.approver-dry-run', { caps: NO_TOOLS });
  if (!card) return;
  t.ok(await v.hasText(S.readOnlyHost), 'no read-only banner');
  t.ok(!(await v.hasText(S.dryrun.bannerTitle)), 'the dry-run banner shown with the read-only one');
});

// ===================================================================
// 7.1 acceptance criteria
// ===================================================================

// AC1
check('ac01.first-paint', '7.1 AC1', 'first paint from the entry result alone; no call before or after the list shows', async (t) => {
  const { v, card } = await inbox(t, 'inbox.approver');
  if (!card) return;
  t.ok((await v.calls()).length === 0, `calls before the list showed: ${JSON.stringify(await v.calls())}`);
  await sleep(1000);
  t.ok((await v.calls()).length === 0, `calls without any click: ${JSON.stringify(await v.calls())}`);
});

// AC2
check('ac02.header.resource-server', '7.1 AC2, 6.2', 'resource-server mode: title, no identity line', async (t) => {
  const { v, fx, card } = await inbox(t, 'inbox.approver');
  if (!card) return;
  t.ok(await v.visible(v.frame.getByRole('heading', { name: S.title, exact: true })), `no heading "${S.title}"`);
  const text = await v.text();
  t.ok(!text.includes('midPoint sees everything here as'), 'personal-mode line shown in resource-server mode');
  const a = fx.result.structuredContent.acting;
  t.ok(!text.includes(a.fullName) && !new RegExp(`\\b${a.name}\\b`).test(text), `the acting identity (${a.fullName}) is named on the surface`);
});

check('ac02.header.personal', '7.1 AC2, 6.2', "personal mode: header.mode.personal with What's this?", async (t) => {
  const { v, card } = await inbox(t, 'inbox.approver-personal');
  if (!card) return;
  t.ok(await v.hasText(S.headerPersonal('Dana Lee')), `no "${S.headerPersonal('Dana Lee')}"`);
  const btn = v.button(S.whatsThis);
  if (t.ok(await v.visible(btn), `no "${S.whatsThis}" button`)) {
    t.ok(!(await v.hasText(S.headerPersonalHelp)), 'the help shows before the disclosure is opened');
    await btn.first().click();
    t.ok(await v.waitFor(S.headerPersonalHelp), 'the disclosure reveals no header.mode.personal.help');
    t.ok((await btn.first().getAttribute('aria-expanded')) === 'true', "What's this? has no aria-expanded=true when open");
  }
});

// AC3: title
check('ac03.title.add', '7.1 AC3 (D33)', 'title "Bob Stone → Database admin", spoken "Bob Stone, access to Database admin"', async (t) => {
  const { v, e, card } = await inbox(t, 'inbox.approver');
  if (!card) return;
  t.ok(await v.visible(card.getByText(e.title, { exact: true })), `no visible title "${e.title}"`);
  const named = card.getByRole('heading', { name: e.label, exact: true }).or(card.getByLabel(e.label, { exact: true }));
  t.ok((await named.count()) > 0, `nothing in the card has the accessible name "${e.label}"`);
});

check('ac03.title.delete', '7.1 AC3 (D33)', 'a removal reads "Bob Stone: remove Database admin"', async (t) => {
  const { v, e, card } = await inbox(t, 'inbox.removal');
  t.ok(card && (await v.visible(card.getByText(e.title, { exact: true }))), `no title "${e.title}"`);
});

check('ac03.title.unreadable', '7.1 AC3 (D16, D33)', 'an unreadable requestee starts the title as common.personHiddenStart', async (t) => {
  const { e, card } = await inbox(t, 'inbox.approver-unreadable');
  t.ok(card && e.title.startsWith(S.personHiddenStart), `no card titled "${e.title}"`);
});

check('ac03.unreadable-named-once', '7.1 AC3 (D30)', 'an unreadable person is named once on the surface', async (t) => {
  const { v, card } = await inbox(t, 'inbox.approver-unreadable');
  if (!card) return;
  const text = (await v.text(card)).toLowerCase();
  const n = text.split(S.personHidden.toLowerCase()).length - 1;
  t.ok(n === 1, `"${S.personHidden}" appears ${n} times on the card surface, want 1 (the title)`);
  t.ok(await v.visible(card.getByText(S.inbox.justificationHidden, { exact: true })), `the reason is not titled "${S.inbox.justificationHidden}"`);
});

// AC3: risk
check('ac03.risk.high', '7.1 AC3, 6.4 (D10)', 'riskLevel high: "Risk: high" in danger fill', async (t) => {
  const { v, card } = await inbox(t, 'inbox.approver');
  if (!card) return;
  const chip = card.getByText(S.inbox.risk('high'), { exact: true });
  if (!t.ok(await v.visible(chip), `no "${S.inbox.risk('high')}"`)) return;
  t.ok((await colorsOf(chip)).some((c) => RGB.danger.includes(c)), 'the high risk chip is not painted with --mp-danger-fill');
});

check('ac03.risk.medium', '7.1 AC3, 6.4 (D10, D19)', 'riskLevel medium: the chip, not in warning or danger colours', async (t) => {
  // Without a deadline, so no clock colour sits near the chip.
  const { v, card } = await inbox(t, derive('inbox.approver', 'risk-medium+no-deadline', (r) => { M.riskMedium(r); M.noDeadline(r); }));
  if (!card) return;
  const chip = card.getByText(S.inbox.risk('medium'), { exact: true });
  if (!t.ok(await v.visible(chip), `no "${S.inbox.risk('medium')}"`)) return;
  const colors = await colorsOf(chip);
  t.ok(!colors.some((c) => RGB.danger.includes(c) || RGB.warning.includes(c)), 'a medium risk chip uses warning or danger colours');
});

check('ac03.risk.absent', '7.1 AC3', 'no riskLevel: no risk chip', async (t) => {
  const { v, card } = await inbox(t, derive('inbox.approver', 'no-risk', M.noRisk));
  if (!card) return;
  t.ok(!/\bRisk:/.test(await v.text(card)), 'a risk chip without riskLevel');
  const { v: v2, card: c2 } = await inbox(t, 'inbox.manager', {}, 1);
  if (c2) t.ok(!/\bRisk:/.test(await v2.text(c2)), 'Finance reports (no riskLevel) shows a risk chip');
});

// AC3: the decision clock
async function clockCard(t, ms, mutation) {
  // Without a risk level, so no risk colour sits near the clock.
  const fx = derive('inbox.approver', `${mutation}+no-risk`, (r) => { M.deadlineIn(ms)(r); M.noRisk(r); });
  const r = await inbox(t, fx);
  return { ...r, deadline: new Date(items(fx.result)[0].context.deadline) };
}

check('ac03.clock.later', '7.1 AC3, 6.11 (D19)', 'deadline in 3 days: absolute "Decide by …", neutral, in <time datetime>', async (t) => {
  const { v, card, deadline } = await clockCard(t, 3 * DAY, 'deadline-in-3-days');
  if (!card) return;
  const chip = card.getByText(/^Decide by /);
  if (!t.ok(await v.visible(chip), 'no "Decide by …"')) return;
  const text = norm(await chip.first().innerText());
  const m = /^Decide by (\d{1,2}) (\D+) (\d{4}), (\d{1,2}):(\d{2})$/.exec(text);
  const p = localParts(deadline, v.config.zone);
  t.ok(m && +m[1] === p.d && +m[3] === p.y && +m[4] === p.hh && +m[5] === p.mm, `"${text}", want the absolute form of ${p.d}.${p.m}.${p.y} ${p.hh}:${String(p.mm).padStart(2, '0')}`);
  const colors = await colorsOf(chip);
  t.ok(!colors.some((c) => RGB.warning.includes(c) || RGB.danger.includes(c)), 'a deadline 3 days away is styled as warning or danger');
  const times = await card.locator('time[datetime]').evaluateAll((els) => els.map((el) => el.getAttribute('datetime')));
  t.ok(times.some((d) => Math.abs(new Date(d).getTime() - deadline.getTime()) < 60000), `no <time datetime> for the deadline (found ${JSON.stringify(times)})`);
});

check('ac03.clock.today-warning', '7.1 AC3, 6.11 (D19, D33)', 'deadline in 2 hours: "Decide by today, HH:MM" in warning style', async (t) => {
  const { v, card, deadline } = await clockCard(t, 2 * HOUR, 'deadline-in-2-hours');
  if (!card) return;
  const chip = card.getByText(/^Decide by /);
  if (!t.ok(await v.visible(chip), 'no "Decide by …"')) return;
  const text = norm(await chip.first().innerText());
  t.ok(clockMatches(text, S.inbox.deadline(S.time.todayAt('')), deadline, v.config.zone), `"${text}", want "Decide by today, HH:MM"`);
  t.ok((await colorsOf(chip)).some((c) => RGB.warning.includes(c)), 'under 4 hours left, but the chip is not in warning style');
});

check('ac03.clock.tomorrow', '7.1 AC3, 6.11 (D33)', 'deadline in 26 hours: "Decide by tomorrow, HH:MM", neutral', async (t) => {
  const { v, card, deadline } = await clockCard(t, 26 * HOUR, 'deadline-in-26-hours');
  if (!card) return;
  const chip = card.getByText(/^Decide by /);
  if (!t.ok(await v.visible(chip), 'no "Decide by …"')) return;
  const text = norm(await chip.first().innerText());
  t.ok(clockMatches(text, S.inbox.deadline(S.time.tomorrowAt('')), deadline, v.config.zone), `"${text}", want "Decide by tomorrow, HH:MM"`);
  t.ok(!(await colorsOf(chip)).some((c) => RGB.warning.includes(c) || RGB.danger.includes(c)), 'a deadline 26 hours away is styled as warning or danger');
});

check('ac03.clock.overdue', '7.1 AC3, 6.11 (D19)', 'deadline passed: "Decision overdue since …" in danger style', async (t) => {
  const { v, card } = await clockCard(t, -HOUR, 'deadline-1-hour-ago');
  if (!card) return;
  const chip = card.getByText(new RegExp(`^${S.inbox.overduePrefix}`));
  if (!t.ok(await v.visible(chip), `no "${S.inbox.overduePrefix}…"`)) return;
  t.ok((await colorsOf(chip)).some((c) => RGB.danger.includes(c)), 'the overdue chip is not in danger style');
  t.ok(!/Decide by /.test(await v.text(card)), 'an overdue item still says "Decide by"');
});

check('ac03.clock.none', '7.1 AC3', 'no deadline: no clock chip', async (t) => {
  const { v, card } = await inbox(t, derive('inbox.approver', 'no-deadline', M.noDeadline));
  if (!card) return;
  const text = await v.text(card);
  t.ok(!/Decide by |Decision overdue/.test(text), 'a clock chip without a deadline');
});

// AC3: what the role allows
check('ac03.description', '7.1 AC3', 'the role description as plain text; short text has no Show more', async (t) => {
  const { v, fx, card } = await inbox(t, 'inbox.approver');
  if (!card) return;
  const d = items(fx.result)[0].context.target.description;
  t.ok(await v.hasText(d, { within: card }), `no description "${d}"`);
  t.ok(!(await v.visible(card.getByRole('button', { name: S.showMore, exact: true }))), 'Show more on text that fits');
});

async function clampCheck(t, fx, full, maxLines) {
  const { v, card } = await inbox(t, fx);
  if (!card) return;
  const head = full.slice(0, 40);
  const el = card.getByText(head, { exact: false }).first();
  if (!t.ok(await v.visible(el), `the text starting "${head}" is not shown`)) return;
  const box = await el.evaluate((n) => {
    const cs = getComputedStyle(n);
    const lh = parseFloat(cs.lineHeight) || parseFloat(cs.fontSize) * 1.5;
    return { h: n.getBoundingClientRect().height, lh, text: n.innerText };
  });
  t.ok(box.h <= box.lh * maxLines + 4, `clamped text is ${Math.round(box.h)} px high, more than ${maxLines} lines of ${box.lh} px`);
  if (box.text.includes('…')) {
    t.ok(box.text.split('…').length === 2 && !box.text.includes('….') && !/[.,;:]…/.test(box.text), `clamp punctuation wrong (D33): "${box.text.slice(-30)}"`);
  }
  const more = card.getByRole('button', { name: S.showMore, exact: true });
  if (!t.ok(await v.visible(more), 'clamped text has no Show more')) return;
  await more.first().click();
  t.ok(await v.hasText(full, { within: card, loose: true }), 'Show more does not reveal the full text');
  t.ok(await v.visible(card.getByRole('button', { name: S.showLess, exact: true })), 'no Show less after Show more');
}

check('ac03.description-clamp', '7.1 AC3, 6.12 (D33)', 'a long description is clamped to 2 lines with Show more / Show less', async (t) => {
  const fx = derive('inbox.approver', 'long-description', M.longDescription);
  await clampCheck(t, fx, items(fx.result)[0].context.target.description, 2);
});

check('ac03.reason-clamp', '7.1 AC3, 6.12 (D33)', 'a long reason is clamped to 3 lines with Show more / Show less', async (t) => {
  const fx = derive('inbox.approver', 'long-justification', M.longJustification);
  await clampCheck(t, fx, items(fx.result)[0].context.justification, 3);
});

// AC3: who asked
check('ac03.who-asked.self', '7.1 AC3', 'a self-request: "Requested by Bob Stone for themselves"', async (t) => {
  const { v, e, card } = await inbox(t, 'inbox.approver');
  if (card) t.ok(await v.hasText(e.requestedBy, { within: card }), `no "${e.requestedBy}"`);
});

check('ac03.who-asked.other', '7.1 AC3', 'someone else asked: "Requested by Mia Kovac"', async (t) => {
  const { v, card } = await inbox(t, derive('inbox.approver', 'other-requester', M.otherRequester));
  if (card) t.ok(await v.hasText(S.inbox.requestedBy('Mia Kovac'), { within: card }), `no "${S.inbox.requestedBy('Mia Kovac')}"`);
});

check('ac03.who-asked.hidden', '7.1 AC3 (D16)', 'an unreadable requester: "Requested by a person you can\'t see in midPoint", no login', async (t) => {
  const { v, card } = await inbox(t, 'inbox.removal');
  if (!card) return;
  t.ok(await v.hasText(S.inbox.requestedBy(S.personHidden), { within: card }), `no "${S.inbox.requestedBy(S.personHidden)}"`);
  t.ok(!/\badministrator\b/.test(await v.text(card)), 'the unreadable requester is named by login');
});

// AC3: how long
check('ac03.how-long', '7.1 AC3, 4.5 (D19)', 'how long is always said: the requested phrase, "No end date", "Access for 30 days"', async (t) => {
  const { v, card } = await inbox(t, 'inbox.approver');
  if (card) t.ok(lines(await v.text(card)).some((l) => S.validity.anyRequest.test(l)), 'no requested-access phrase for a validity with dates');
  const { v: v2, card: c2 } = await inbox(t, 'inbox.manager', {}, 1);
  if (c2) t.ok(await v2.hasText(S.validity.permanent, { within: c2 }), `no "${S.validity.permanent}" without a validity`);
  const fx = derive('inbox.approver', 'validity-30-days', M.validityDays(30, hostZone()));
  const { v: v3, card: c3 } = await inbox(t, fx);
  if (c3) t.ok(lines(await v3.text(c3)).some((l) => S.validity.days(30).test(l)), 'no "Access for 30 days (ends …)" for an end 30 days ahead');
});

check('draft9.removal-card', 'draft.9 owner decision 5', 'a removal (change delete): no how-long line and no "No reason given"', async (t) => {
  const { v, card } = await inbox(t, 'inbox.removal');
  if (!card) return;
  const ls = lines(await v.text(card));
  t.ok(!ls.some((l) => S.validity.anyRequest.test(l)), `a how-long line on a removal: ${JSON.stringify(ls.filter((l) => S.validity.anyRequest.test(l)))}`);
  t.ok(!ls.includes(S.inbox.noReason), `"${S.inbox.noReason}" on a removal`);
});

// AC3: the reason
check('ac03.reason', '7.1 AC3', 'the reason, titled "Reason given by Bob Stone", as text', async (t) => {
  const { v, fx, card } = await inbox(t, 'inbox.approver');
  if (!card) return;
  t.ok(await v.hasText(S.inbox.justification('Bob Stone'), { within: card }), `no "${S.inbox.justification('Bob Stone')}"`);
  t.ok(await v.hasText(items(fx.result)[0].context.justification, { within: card, loose: true }), 'the reason text is not shown');
});

check('ac03.no-reason.field', '7.1 AC3 (D30)', 'reason field configured but empty: "No reason given"', async (t) => {
  const { v, card } = await inbox(t, 'inbox.manager', {}, 1);
  if (card) t.ok(await v.hasText(S.inbox.noReason, { within: card }), `no "${S.inbox.noReason}" on the item without a reason`);
  const { v: v2, card: c2 } = await inbox(t, 'inbox.manager', {}, 0);
  if (c2) t.ok(!(await v2.hasText(S.inbox.noReason, { within: c2 })), `"${S.inbox.noReason}" on an item with a reason`);
});

check('ac03.no-reason.no-field', '7.1 AC3 (D5, D30)', 'no reason field: nothing about a reason', async (t) => {
  const { v, card } = await inbox(t, 'inbox.approver-no-reason-field');
  if (!card) return;
  const text = await v.text();
  t.ok(!text.includes(S.inbox.noReason), `"${S.inbox.noReason}" without a reason field`);
  t.ok(!text.includes('Reason given by'), 'a reason block without a reason field');
});

check('security.untrusted-text', '4.1 rule 7, 9', 'untrusted free text is shown as text, never as HTML', async (t) => {
  const fx = derive('inbox.approver', 'markup-justification', M.markupJustification);
  const { v, card } = await inbox(t, fx);
  if (!card) return;
  t.ok(await v.hasText('<b>urgent</b>', { within: card, loose: true }), 'the markup is not shown literally');
  const injected = await card.evaluate((c) => ({
    bold: [...c.querySelectorAll('b, strong')].some((b) => b.textContent.includes('urgent')),
    img: [...c.querySelectorAll('img')].some((i) => i.getAttribute('src') === 'x'),
  }));
  t.ok(!injected.bold && !injected.img, `the reason was parsed as HTML: ${JSON.stringify(injected)}`);
  t.ok((await v.frame.title()) !== 'owned', 'an injected handler ran');
});

// AC3: why you
async function whyCheck(t, fx, which, want) {
  const { v, card } = await inbox(t, fx, {}, which);
  if (card) t.ok(await v.hasText(want, { within: card }), `no "${want}"`);
}
check('ac03.why.role-approver', '7.1 AC3 (D29)', 'approver of the role, from the approver\'s own memberships', (t) =>
  whyCheck(t, 'inbox.approver', 0, S.inbox.why.roleApprover('Database admin')));
check('ac03.why.manager', '7.1 AC3 (D14)', 'the requestee\'s manager', (t) => whyCheck(t, 'inbox.manager', 1, S.inbox.why.manager('Bob Stone')));
check('ac03.why.step', '7.1 AC3 (D14)', 'assigned, with the step name', (t) => whyCheck(t, 'inbox.removal', 0, S.inbox.why.step('Removal check')));
check('ac03.why.owner', '7.1 AC3 (D29)', 'owner of the role', (t) =>
  whyCheck(t, derive('inbox.approver', 'reason-owner', M.reasonOwner), 0, S.inbox.why.roleOwner('Database admin')));
check('ac03.why.assigned', '7.1 AC3 (D14)', 'assigned, no step name', (t) =>
  whyCheck(t, derive('inbox.approver', 'reason-assigned-no-step-name', M.reasonAssignedNoName), 0, S.inbox.why.assigned));

// D37: the step line instead of "who else decides"
check('d37.step-line', 'D37 (overrides 7.1 AC3 "who else decides")', 'each card says "Step {number} of {count}"', async (t) => {
  const { v, card } = await inbox(t, 'inbox.approver');
  if (card) t.ok(await v.hasText(S.d37.step(1, 2), { within: card }), `no "${S.d37.step(1, 2)}"`);
  const { v: v2, card: c2 } = await inbox(t, 'inbox.manager', {}, 1);
  if (c2) t.ok(await v2.hasText(S.d37.step(1, 1), { within: c2 }), `no "${S.d37.step(1, 1)}" on the one-step item`);
});

check('d37.step-line.no-count', 'D37', 'no stage.count: no step line', async (t) => {
  const { v, card } = await inbox(t, derive('inbox.approver', 'missing-stage-count', M.missingStageCount));
  if (card) t.ok(!/\bStep \d/.test(await v.text(card)), 'a step line without stage.count');
});

check('d37.no-who-else', 'D37', 'no "who else decides" line (none of inbox.approvers.* / inbox.steps.more)', async (t) => {
  for (const fx of [fixture('inbox.approver'), fixture('inbox.manager'),
    derive('inbox.approver', 'more-steps-alone', M.moreStepsAlone), derive('inbox.approver', 'last-step-others-asked', M.lastStepOthersAsked)]) {
    const { v, card } = await inbox(t, fx);
    if (!card) continue;
    const text = await v.text();
    for (const re of S.d37.forbidden) t.ok(!re.test(text), `${fx.name}: "${re.source}" shown`);
    // Nobody else asked to decide is named anywhere on the surface.
    for (const wi of items(fx.result)) {
      for (const p of [...wi.context.coAssignees, ...wi.context.stageApprovers]) {
        const n = p.displayName || p.name;
        if (n) t.ok(!text.includes(n), `${fx.name}: other approver "${n}" named`);
      }
    }
  }
});

// AC4: surface hygiene
async function openAllDetails(t, v, fx) {
  for (const wi of items(fx.result)) {
    const card = await v.card(expectItem(wi));
    if (card) await v.details(card);
  }
  await settle(400);
}
const hygieneScenarios = () => [
  { fx: fixture('inbox.approver'), tools: caseTools('case.approver') },
  { fx: fixture('inbox.manager'), tools: { get_case: [{ when: { oid: '40000000-0000-0000-0000-000000000001' }, result: fixture('case.manager-delegated').result }, { result: fixture('case.manager-step').result }] } },
  { fx: fixture('inbox.removal'), tools: caseTools('case.removal') },
  { fx: fixture('inbox.approver-unreadable'), tools: caseTools('case.approver') },
];

check('ac04.no-oids', '7.1 AC4 (D16)', 'no OID on the surface or in Details (text and accessible names)', async (t) => {
  for (const s of hygieneScenarios()) {
    const v = await t.open({ entry: s.fx, tools: s.tools });
    if (!(await v.card(expectItem(items(s.fx.result)[0])))) { t.fail(`${s.fx.name}: no card`); continue; }
    const closed = await v.surfaceStrings();
    t.ok(!UUID.test(closed), `${s.fx.name}: an OID on the surface: ${closed.match(UUID)?.[0]}`);
    await openAllDetails(t, v, s.fx);
    const open = await v.surfaceStrings();
    t.ok(!UUID.test(open), `${s.fx.name}: an OID with Details open: ${open.match(UUID)?.[0]}`);
  }
});

check('ac04.no-login-names', '7.1 AC4 (D16)', 'no login name on the surface or in Details', async (t) => {
  for (const s of hygieneScenarios()) {
    const v = await t.open({ entry: s.fx, tools: s.tools });
    if (!(await v.card(expectItem(items(s.fx.result)[0])))) { t.fail(`${s.fx.name}: no card`); continue; }
    await openAllDetails(t, v, s.fx);
    const text = await v.surfaceStrings();
    for (const login of loginNames(s.fx.result)) {
      t.ok(!new RegExp(`(^|[^\\w-])${login}([^\\w-]|$)`).test(text), `${s.fx.name}: login name "${login}" shown`);
    }
  }
});

check('ac04.no-vocabulary', '7.1 AC4 (D15, D28)', 'no midPoint vocabulary, kind label, relation or stage number on the surface', async (t) => {
  for (const s of hygieneScenarios()) {
    const v = await t.open({ entry: s.fx, tools: s.tools });
    if (!(await v.card(expectItem(items(s.fx.result)[0])))) { t.fail(`${s.fx.name}: no card`); continue; }
    const text = stripData(await v.surfaceStrings(), s.fx.result);
    for (const re of VOCABULARY) t.ok(!re.test(text), `${s.fx.name}: "${text.match(re)?.[0]}" on the surface`);
    t.ok(!KIND_LABELS.test(text), `${s.fx.name}: kind label "${text.match(KIND_LABELS)?.[0]}" on the surface`);
    const steps = text.match(/\bStep \d+[^\n]*/g) ?? [];
    t.ok(steps.every((l) => /^Step \d+ of \d+$/.test(norm(l))), `${s.fx.name}: a step number outside the step line: ${JSON.stringify(steps)}`);
  }
});

check('ac04.sort-order', '7.1 AC4, Layout 4', 'cards sorted by deadline (soonest first), then oldest request', async (t) => {
  const fx = sortFixture();
  const v = await t.open({ entry: fx });
  const first = expectItem(items(fx.result).find((w) => w.context.target.displayName === 'Sort first'));
  if (!(await v.card(first))) return t.fail('the cards did not render');
  const text = await v.text();
  const pos = sortedTitles.map((n) => text.indexOf(`Bob Stone → ${n}`));
  t.ok(pos.every((p) => p >= 0), `missing titles: ${sortedTitles.filter((_, i) => pos[i] < 0)}`);
  const order = sortedTitles.map((n, i) => [n, pos[i]]).sort((a, b) => a[1] - b[1]).map((x) => x[0]);
  t.ok(JSON.stringify(order) === JSON.stringify(sortedTitles), `order ${order.join(', ')}`);
});

// AC5: Details
check('ac05.details.closed-by-default', '7.1 AC5, Layout 4', 'Details closed by default, a disclosure with aria-expanded and aria-controls', async (t) => {
  const { v, card } = await inbox(t, 'inbox.approver');
  if (!card) return;
  const toggle = card.getByRole('button', { name: S.inbox.details, exact: true });
  if (!t.ok(await v.visible(toggle), `no "${S.inbox.details}" button`)) return;
  t.ok((await toggle.first().getAttribute('aria-expanded')) === 'false', 'Details toggle without aria-expanded=false');
  t.ok(!!(await toggle.first().getAttribute('aria-controls')), 'Details toggle without aria-controls');
  t.ok(!(await v.visible(card.getByText(S.inbox.requestedAt))), 'the requested time shows before Details open');
});

check('ac05.details.content', '7.1 AC5 (D28, D32)', 'Details: requested time, roles in effect with "Comes with …", approval steps', async (t) => {
  const { v, fx, card } = await inbox(t, 'inbox.approver', { tools: caseTools() });
  if (!card) return;
  const d = await v.details(card);
  if (!t.ok(d, 'no Details toggle')) return;
  t.ok(await v.visible(card.getByRole('button', { name: S.inbox.hideDetails, exact: true })), `the open toggle does not read "${S.inbox.hideDetails}"`);
  t.ok((await d.toggle.getAttribute('aria-expanded')) === 'true', 'aria-expanded is not true when open');
  const region = d.region ?? card;
  t.ok(await v.waitFor(region.getByText(S.inbox.requestedAt)), 'no "Requested … (…)" line');
  const roles = items(fx.result)[0].context.requesteeAccess.roles;
  t.ok(await v.hasText(S.inbox.currentRoles('Bob Stone', roles.length), { within: region }), `no "${S.inbox.currentRoles('Bob Stone', roles.length)}"`);
  for (const r of roles) t.ok(await v.hasText(r.displayName || r.name, { within: region }), `role "${r.displayName || r.name}" not listed`);
  t.ok(await v.hasText(S.inheritedVia('build-runner'), { within: region }), `no "${S.inheritedVia('build-runner')}"`);
  t.ok(await v.waitFor(region.getByText(S.inbox.history, { exact: true })), `no "${S.inbox.history}"`);
});

check('ac05.details.only-new', '7.1 AC5 (D28)', 'Details repeat nothing the card shows and have no kind labels', async (t) => {
  const { v, fx, card } = await inbox(t, 'inbox.approver', { tools: caseTools() });
  if (!card) return;
  const d = await v.details(card);
  if (!t.ok(d?.region, 'Details have no region named by aria-controls')) return;
  await settle(400);
  const text = await v.text(d.region);
  for (const s of ['Requested by', 'Decide by', 'Risk:', S.d37.step(1, 2), 'Reason given by', "You're asked", S.validity.permanent, 'Access for', 'Access from']) {
    t.ok(!text.includes(s), `Details repeat "${s}"`);
  }
  t.ok(!KIND_LABELS.test(stripData(text, fx.result, fixture('case.approver').result)), `kind label "${stripData(text, fx.result).match(KIND_LABELS)?.[0]}" in Details`);
});

// The "Approval steps" subsection of a card's Details, split into one block
// per step line: { label, rest } where rest is what follows the label up to
// the next step.
async function approvalSteps(t, v, card) {
  const d = await v.details(card);
  if (!d) return t.fail('no Details'), null;
  const region = d.region ?? card;
  if (!(await v.waitFor(region.getByText(/^Step 1\b/), 3000))) return t.fail('no step line in Details'), null;
  const ls = lines(await v.text(region));
  const start = ls.indexOf(S.inbox.history);
  if (start < 0) return t.fail(`no "${S.inbox.history}" heading`), null;
  let section = ls.slice(start + 1);
  const end = section.findIndex((l) => l.startsWith(S.openInMidpoint));
  if (end >= 0) section = section.slice(0, end);
  const blocks = [];
  const states = S.timeline.states.join('|');
  for (const l of section) {
    // A state chip may come before the step's label on the same line.
    const m = new RegExp(`^(?:(${states})\\s+)?(Step \\d+.*)$`).exec(l);
    if (m) blocks.push({ line: m[2], text: m[1] ? `${m[2]}\n${m[1]}` : m[2] });
    else if (blocks.length) blocks[blocks.length - 1].text += `\n${l}`;
  }
  return { blocks, section: section.join('\n') };
}

// caseAtStep2 is the two-step case once its first step is approved: the
// reader's step 1 item approved and closed, a step 2 item open for them.
const caseAtStep2 = () => derive('case.approver', 'at-step-2', (r) => {
  const sc = r.structuredContent;
  sc.stage = { ...sc.stages[1] };
  const mine = sc.workItems[0];
  Object.assign(mine, { outcome: 'approve', closedAt: '2026-10-01T10:57:35.330Z', performer: mine.assignees[0], comment: 'Fine by me.' });
  sc.workItems.push({ caseOid: mine.caseOid, id: '10', assignee: mine.assignee, stage: 2, assignees: [mine.assignees[0]], createdAt: '2026-10-01T10:57:48.012Z' });
});
const inboxAtStep2 = () => derive('inbox.approver', 'at-step-2', (r) => {
  const wi = items(r)[0];
  wi.id = '10';
  wi.stage = 2;
  wi.context.stage = { number: 2, count: 2, name: 'Role approvers', strategy: 'firstDecides' };
  wi.context.stageApprovers = [];
});
// caseBothAsked adds a second open item in step 1, for Mia Kovac.
const caseBothAsked = () => derive('case.approver', 'both-asked', (r) => {
  const mine = r.structuredContent.workItems[0];
  r.structuredContent.workItems.push({ ...structuredClone(mine), id: '7', assignee: 'mkovac',
    assignees: [{ oid: '10000000-0000-0000-0000-0000000000a2', type: 'User', name: 'mkovac', displayName: 'Mia Kovac' }] });
});

check('draft9.steps.no-names', '7.1 AC5, 6.15; draft.9 owner decision 3', 'Approval steps: one line per step ("Step 1, Team leads"), no people, comments or times', async (t) => {
  for (const [entry, cs] of [[fixture('inbox.approver'), fixture('case.approver')], [fixture('inbox.approver'), caseBothAsked()], [inboxAtStep2(), caseAtStep2()]]) {
    const { v, card } = await inbox(t, entry, { tools: { get_case: [{ result: cs.result }] } });
    if (!card) continue;
    const s = await approvalSteps(t, v, card);
    if (!s) continue;
    const labels = s.blocks.map((b) => b.line);
    t.ok(s.blocks.length === 2, `${cs.name}: ${s.blocks.length} step lines, want 2 (one per step): ${JSON.stringify(labels)}`);
    t.ok(labels[0]?.startsWith(S.timeline.stageNamed(1, 'Team leads')) && labels[1]?.startsWith(S.timeline.stageNamed(2, 'Role approvers')),
      `${cs.name}: step lines ${JSON.stringify(labels)}, want "${S.timeline.stageNamed(1, 'Team leads')}" and "${S.timeline.stageNamed(2, 'Role approvers')}"`);
    const people = new Set(['Bob Stone', 'Dana Lee', 'Mia Kovac', 'bstone', 'dlee', 'mkovac']);
    for (const wi of cs.result.structuredContent.workItems) for (const p of [...wi.assignees, wi.performer].filter(Boolean)) people.add(p.displayName || p.name);
    for (const p of people) t.ok(!s.section.includes(p), `${cs.name}: "${p}" named in the approval steps`);
    t.ok(!/\byou\b/i.test(s.section), `${cs.name}: "you" in the approval steps`);
    t.ok(!s.section.includes('Fine by me.'), `${cs.name}: a decision comment in the approval steps`);
    for (const re of S.timeline.forbidden) t.ok(!re.test(s.section), `${cs.name}: "${re.source}" in the approval steps`);
    t.ok(!/\d{1,2}:\d{2}/.test(s.section), `${cs.name}: a time in the approval steps`);
  }
});

check('draft9.steps.states', '6.15; draft.9 owner decision 3 (refined)', 'step states follow the current step: earlier Approved, current Waiting, later none', async (t) => {
  const stateOf = (b, label) => {
    const rest = b.text.slice(label.length);
    return S.timeline.states.filter((w) => new RegExp(`\\b${w}\\b`).test(rest));
  };
  const scenarios = [
    { entry: fixture('inbox.approver'), cs: fixture('case.approver'), want: [['Waiting'], []] },
    { entry: inboxAtStep2(), cs: caseAtStep2(), want: [['Approved'], ['Waiting']] },
  ];
  for (const { entry, cs, want } of scenarios) {
    const { v, card } = await inbox(t, entry, { tools: { get_case: [{ result: cs.result }] } });
    if (!card) continue;
    const s = await approvalSteps(t, v, card);
    if (!s || s.blocks.length !== 2) { t.fail(`${cs.name}: want two step lines`); continue; }
    const labels = [S.timeline.stageNamed(1, 'Team leads'), S.timeline.stageNamed(2, 'Role approvers')];
    s.blocks.forEach((b, i) => {
      const got = stateOf(b, labels[i]);
      t.ok(JSON.stringify(got) === JSON.stringify(want[i]), `${cs.name}: step ${i + 1} shows ${JSON.stringify(got)}, want ${JSON.stringify(want[i])}`);
    });
  }
});

check('ac05.get_case.once', '7.1 AC5', 'get_case is called once, the first time Details open, with the case OID', async (t) => {
  const { v, wi, card } = await inbox(t, 'inbox.approver', { tools: caseTools() });
  if (!card) return;
  t.ok((await v.calls('get_case')).length === 0, 'get_case before Details opened');
  const d = await v.details(card);
  if (!d) return t.fail('no Details');
  await settle(400);
  await d.toggle.click();
  await settle(200);
  await d.toggle.click();
  await settle(400);
  const calls = await v.calls('get_case');
  t.ok(calls.length === 1, `${calls.length} get_case calls after opening Details twice, want 1`);
  t.ok(calls[0] && JSON.stringify(calls[0].args) === JSON.stringify({ oid: wi.caseOid }), `get_case arguments ${JSON.stringify(calls[0]?.args)}, want {"oid":"${wi.caseOid}"}`);
});

check('ac05.get_case.read-only', '7.1 AC5, 6.6', 'a read-only host: no get_case and no approval steps', async (t) => {
  const { v, card } = await inbox(t, 'inbox.approver', { caps: NO_TOOLS });
  if (!card) return;
  await v.details(card);
  await settle(400);
  t.ok((await v.calls()).length === 0, 'a tool call on a read-only host');
  t.ok(!(await v.hasText(S.inbox.history)), `"${S.inbox.history}" on a read-only host`);
});

check('ac05.get_case.error', '7.1 States Details open', 'a failed get_case shows its error in the steps subsection only', async (t) => {
  const { v, e, card } = await inbox(t, 'inbox.approver', { tools: caseTools('error.case.not-authorized') });
  if (!card) return;
  const d = await v.details(card);
  if (!d) return t.fail('no Details');
  t.ok(await v.waitFor((d.region ?? card).getByText(S.errorByCode['not-authorized'])), 'no error.notAuthorized in Details');
  t.ok(await v.visible(card.getByText(S.inbox.requestedAt)), 'the rest of Details is gone');
  t.ok(await v.visible(card.getByRole('button', { name: e.approveLabel, exact: true })), 'the card lost its actions');
});

check('ac05.current-roles.hidden-and-none', '7.1 AC5 (D30)', 'currentRolesHiddenPerson for an unreadable requestee; currentRolesNone for no roles', async (t) => {
  const { v, card } = await inbox(t, 'inbox.approver-unreadable', { tools: caseTools() });
  if (card) {
    const d = await v.details(card);
    t.ok(await v.waitFor((d?.region ?? card).getByText(S.inbox.currentRolesHiddenPerson, { exact: true })), `no "${S.inbox.currentRolesHiddenPerson}"`);
  }
  const { v: v2, card: c2 } = await inbox(t, derive('inbox.approver', 'no-current-roles', M.noCurrentRoles), { tools: caseTools() });
  if (c2) {
    const d2 = await v2.details(c2);
    t.ok(await v2.waitFor((d2?.region ?? c2).getByText(S.inbox.currentRolesNone('Bob Stone'), { exact: true })), `no "${S.inbox.currentRolesNone('Bob Stone')}"`);
  }
});

check('ac05.links', '7.1 AC5, 4.9', 'links on: one "Open in midPoint" line in Details, ui/open-link, the URL never shown', async (t) => {
  const fx = derive('inbox.approver', 'links-on', M.linksOn);
  const { v, wi, card } = await inbox(t, fx, { tools: caseTools() });
  if (!card) return;
  const d = await v.details(card);
  const region = d?.region ?? card;
  t.ok(await v.waitFor(region.getByText(S.openInMidpoint, { exact: false })), `no "${S.openInMidpoint}" in Details`);
  for (const name of ['Bob Stone', 'Database admin', S.request]) {
    t.ok(await v.visible(region.getByRole('button', { name: S.openInMidpointLabel(name), exact: true }).or(region.getByRole('link', { name: S.openInMidpointLabel(name), exact: true }))),
      `no link named "${S.openInMidpointLabel(name)}"`);
  }
  const bob = region.getByRole('button', { name: S.openInMidpointLabel('Bob Stone'), exact: true }).or(region.getByRole('link', { name: S.openInMidpointLabel('Bob Stone'), exact: true }));
  if (await v.visible(bob)) {
    await bob.first().click();
    await settle();
    const sent = await v.sent('ui/open-link');
    const want = `https://idm.example.test/midpoint/admin/user/${wi.context.requestee.oid}`;
    t.ok(sent[0]?.params?.url === want, `ui/open-link ${JSON.stringify(sent[0]?.params)}, want ${want}`);
  }
  t.ok(!(await v.text()).includes('idm.example.test'), 'the URL is shown as text');
  t.ok(await v.hasText(S.openInMidpointNote), 'no common.openInMidpointNote in the footer');
  const { v: v2, card: c2 } = await inbox(t, fx, { tools: caseTools(), caps: { serverTools: {}, message: {}, updateModelContext: {} } });
  if (c2) {
    await v2.details(c2);
    t.ok(!(await v2.hasText(S.openInMidpoint, { loose: true })), 'links without hostCapabilities.openLinks');
  }
});

// AC6
check('ac06.cant-approve', '7.1 AC6 (D36)', 'unreadable requestee: Reject but no Approve, and inbox.item.cantApprove', async (t) => {
  const { v, e, card } = await inbox(t, 'inbox.approver-unreadable');
  if (!card) return;
  t.ok(!(await v.visible(card.getByRole('button', { name: /^(Approve|Preview approval)/ }))), 'Approve offered for a requestee the approver can\'t see');
  t.ok(await v.visible(card.getByRole('button', { name: e.rejectLabel, exact: true })), `no "${e.rejectLabel}"`);
  t.ok(await v.hasText(S.inbox.cantApprove, { within: card }), 'no inbox.item.cantApprove');
  const dry = derive('inbox.approver-unreadable', 'dry-run', (r) => { r.structuredContent.server.writesEnabled = false; });
  const { v: v2, e: e2, card: c2 } = await inbox(t, dry);
  if (!c2) return;
  t.ok(!(await v2.visible(c2.getByRole('button', { name: /^(Approve|Preview approval)/ }))), 'dry run: the approval preview offered for an unreadable requestee');
  t.ok(await v2.visible(c2.getByRole('button', { name: e2.previewRejectLabel, exact: true })), `dry run: no "${e2.previewRejectLabel}"`);
});

// AC7
check('ac07.approve-dialog', '7.1 AC7, 6.5 (D21)', 'Approve: modal dialog titled with role and person, one sentence, optional comment focused, no rows or OIDs', async (t) => {
  const { v, fx, e, card } = await inbox(t, 'inbox.approver');
  if (!card) return;
  const d = await openDialog(t, v, card, e.approveLabel);
  if (!d) return;
  t.ok((await d.dlg.first().getAttribute('aria-modal')) === 'true', 'dialog without aria-modal="true"');
  const name = await v.accessibleName(d.dlg);
  t.ok(name === e.approveTitle, `dialog named "${name}", want "${e.approveTitle}"`);
  const desc = await d.dlg.first().evaluate((el) => (el.getAttribute('aria-describedby') || '').split(/\s+/).filter(Boolean).map((id) => document.getElementById(id)?.innerText ?? '').join(' ').trim());
  t.ok(desc === S.confirm.approveBodyMore, `dialog described by "${desc}", want "${S.confirm.approveBodyMore}"`);
  const box = d.dlg.getByRole('textbox', { name: S.confirm.commentOptional, exact: true });
  t.ok(await v.visible(box), `no textbox "${S.confirm.commentOptional}"`);
  t.ok(await v.isFocused(box), 'initial focus is not on the comment field');
  const text = await v.surfaceStrings(d.dlg.first());
  t.ok(!UUID.test(text), 'an OID in the dialog');
  for (const login of loginNames(fx.result)) t.ok(!new RegExp(`\\b${login}\\b`).test(text), `login "${login}" in the dialog`);
  for (const row of S.confirm.rows) t.ok(!lines(text).includes(row), `a summary row "${row}" in the dialog`);
  t.ok(await v.visible(d.dlg.getByRole('button', { name: S.cancel, exact: true })), 'no Cancel');
  t.ok(await v.visible(d.dlg.getByRole('button', { name: S.confirm.approveSubmit, exact: true })), `no "${S.confirm.approveSubmit}" submit`);
});

async function sentenceCheck(t, fx, which, test, want) {
  const { v, e, card } = await inbox(t, fx, {}, which);
  if (!card) return;
  const d = await openDialog(t, v, card, e.approveLabel);
  if (!d) return;
  const ls = lines(await v.text(d.dlg.first()));
  t.ok(ls.some(test), `no "${want}" in the dialog: ${JSON.stringify(ls)}`);
}
check('ac07.sentence.final', '7.1 AC7, D37', 'last step (count == number): the last-step sentence', (t) =>
  sentenceCheck(t, 'inbox.manager', 1, isBodyFinal, S.confirm.approveBodyFinal));
check('ac07.sentence.more', '7.1 AC7, D37', 'more steps follow: confirm.approve.bodyMore', (t) =>
  sentenceCheck(t, 'inbox.approver', 0, (l) => l === S.confirm.approveBodyMore, S.confirm.approveBodyMore));
check('ac07.sentence.unknown', '7.1 AC7, D37', 'step count unknown: confirm.approve.bodyUnknown', (t) =>
  sentenceCheck(t, derive('inbox.approver', 'missing-stage-count', M.missingStageCount), 0, (l) => l === S.confirm.approveBodyUnknown, S.confirm.approveBodyUnknown));
check('d37.sentence.by-steps-only', 'D37', 'last step with others still asked: still the last-step sentence; more steps alone: bodyMore', async (t) => {
  await sentenceCheck(t, derive('inbox.approver', 'last-step-others-asked', M.lastStepOthersAsked), 0, isBodyFinal, S.confirm.approveBodyFinal);
  await sentenceCheck(t, derive('inbox.approver', 'more-steps-alone', M.moreStepsAlone), 0, (l) => l === S.confirm.approveBodyMore, S.confirm.approveBodyMore);
});

check('ac07.focus-return', '7.1 AC7, 6.5', 'Esc and Cancel close the dialog and return focus to Approve', async (t) => {
  const { v, e, card } = await inbox(t, 'inbox.approver');
  if (!card) return;
  let d = await openDialog(t, v, card, e.approveLabel);
  if (!d) return;
  await v.page.keyboard.press('Escape');
  t.ok(await v.waitGone(d.dlg), 'Esc does not close the dialog');
  t.ok(await v.isFocused(d.btn), 'after Esc, focus is not on the Approve button');
  d = await openDialog(t, v, card, e.approveLabel);
  if (!d) return;
  await d.dlg.getByRole('button', { name: S.cancel, exact: true }).first().click();
  t.ok(await v.waitGone(d.dlg), 'Cancel does not close the dialog');
  t.ok(await v.isFocused(d.btn), 'after Cancel, focus is not on the Approve button');
  t.ok((await v.calls('decide_work_item')).length === 0, 'closing the dialog sent a decision');
});

check('ac07.focus-trap', '6.5', 'Tab and Shift+Tab stay inside the dialog', async (t) => {
  const { v, e, card } = await inbox(t, 'inbox.approver');
  if (!card) return;
  const d = await openDialog(t, v, card, e.approveLabel);
  if (!d) return;
  for (const key of ['Tab', 'Tab', 'Tab', 'Tab', 'Tab', 'Shift+Tab', 'Shift+Tab', 'Shift+Tab', 'Shift+Tab', 'Shift+Tab']) {
    await v.page.keyboard.press(key);
    const inside = await d.dlg.first().evaluate((el) => el.contains(document.activeElement));
    if (!inside) return t.fail(`focus left the dialog after ${key}`);
  }
});

// AC8
check('ac08.reject-required', '7.1 AC8, 6.5', 'Reject with an empty or blank reason: error, focus on the field, nothing sent', async (t) => {
  const { v, e, card } = await inbox(t, 'inbox.approver');
  if (!card) return;
  const d = await openDialog(t, v, card, e.rejectLabel);
  if (!d) return;
  const name = await v.accessibleName(d.dlg);
  t.ok(name === e.rejectTitle, `dialog named "${name}", want "${e.rejectTitle}"`);
  t.ok(await v.hasText(e.rejectBody, { within: d.dlg.first() }), `no "${e.rejectBody}"`);
  const box = d.dlg.getByRole('textbox', { name: S.confirm.commentRequired, exact: true });
  if (!t.ok(await v.visible(box), `no textbox "${S.confirm.commentRequired}"`)) return;
  const submit = d.dlg.getByRole('button', { name: S.confirm.rejectSubmit, exact: true });
  t.ok(await submit.first().isEnabled(), 'the Reject submit is disabled (it must stay enabled, 6.5)');
  for (const value of ['', '   \n  ']) {
    await box.first().fill(value);
    await submit.first().click();
    t.ok(await v.waitFor(d.dlg.getByText(S.confirm.requiredError, { exact: true })), `no "${S.confirm.requiredError}" for ${JSON.stringify(value)}`);
    t.ok(await v.isFocused(box), `focus not on the reason field after submitting ${JSON.stringify(value)}`);
  }
  await settle();
  t.ok((await v.calls('decide_work_item')).length === 0, 'a blank reason was sent');
});

// AC9
check('ac09.one-call', '7.1 AC9, 6.5', 'one confirmation, one decide_work_item, even on a double click', async (t) => {
  const { v, e, card } = await inbox(t, 'inbox.approver', { tools: decideTools('decide.approve-next', { hold: true }) });
  if (!card) return;
  const d = await openDialog(t, v, card, e.approveLabel);
  if (!d) return;
  await d.dlg.getByRole('button', { name: S.confirm.approveSubmit, exact: true }).first().dblclick();
  await settle(500);
  t.ok((await v.calls('decide_work_item')).length === 1, `${(await v.calls('decide_work_item')).length} decide calls on a double click`);
  await v.release();
  await v.waitFor(S.inbox.outcome.approvedOpen);
  await settle(300);
  t.ok((await v.calls('decide_work_item')).length === 1, 'a second decide_work_item after the answer');
});

check('ac09.working-state', '6.5', 'while the call runs: Working…, both buttons disabled, Esc does nothing', async (t) => {
  const { v, e, card } = await inbox(t, 'inbox.approver', { tools: decideTools('decide.approve-next', { hold: true }) });
  if (!card) return;
  const d = await openDialog(t, v, card, e.approveLabel);
  if (!d) return;
  await d.dlg.getByRole('button', { name: S.confirm.approveSubmit, exact: true }).first().click();
  const working = d.dlg.getByRole('button', { name: S.confirm.working, exact: true });
  t.ok(await v.waitFor(working), `no "${S.confirm.working}" button while the call runs`);
  if (await v.visible(working)) t.ok(await working.first().isDisabled(), `"${S.confirm.working}" is not disabled`);
  const cancel = d.dlg.getByRole('button', { name: S.cancel, exact: true });
  if (await v.visible(cancel)) t.ok(await cancel.first().isDisabled(), 'Cancel is enabled while the call runs');
  await v.page.keyboard.press('Escape');
  await settle(200);
  t.ok(await v.visible(d.dlg), 'Esc closed the dialog while the call runs');
  await v.release();
  t.ok(await v.waitGone(d.dlg), 'the dialog did not close after the answer');
});

// AC10
check('ac10.comment-omitted', '7.1 AC10, 6.5', 'an empty optional comment is omitted; caseOid, workItemId and the midPoint names verbatim', async (t) => {
  const { v, wi, e, card } = await inbox(t, 'inbox.approver', { tools: decideTools('decide.approve-next') });
  if (!card) return;
  await approve(t, v, card, e);
  await settle();
  const c = (await v.calls('decide_work_item'))[0];
  const want = { caseOid: wi.caseOid, userName: wi.context.requestee.name, roleName: wi.context.target.name, workItemId: wi.id, decision: 'approve' };
  t.ok(c && JSON.stringify(Object.keys(c.args).sort()) === JSON.stringify(Object.keys(want).sort()) && Object.entries(want).every(([k, x]) => c.args[k] === x),
    `arguments ${JSON.stringify(c?.args)}, want exactly ${JSON.stringify(want)}`);
});

check('ac10.comment-trimmed', '6.5', 'comments are sent trimmed; reject carries its reason', async (t) => {
  const { v, e, card } = await inbox(t, 'inbox.approver', { tools: decideTools('decide.approve-next') });
  if (card) {
    await approve(t, v, card, e, { comment: '  Fine by me.  ' });
    await settle();
    const c = (await v.calls('decide_work_item'))[0];
    t.ok(c?.args?.comment === 'Fine by me.', `approve comment ${JSON.stringify(c?.args?.comment)}, want "Fine by me."`);
  }
  const r = await inbox(t, 'inbox.approver', { tools: decideTools('decide.reject-closed') });
  if (r.card) {
    await reject(t, r.v, r.card, r.e, { comment: ' Not needed for this project. ' });
    await settle();
    const c = (await r.v.calls('decide_work_item'))[0];
    t.ok(c?.args?.decision === 'reject' && c?.args?.comment === 'Not needed for this project.', `reject arguments ${JSON.stringify(c?.args)}`);
  }
});

// AC11: outcomes
async function outcomeCheck(t, { entry = 'inbox.approver', which = 0, decide, rejectIt = false, want, extra }) {
  const { v, e, card } = await inbox(t, entry, { tools: decideTools(decide) }, which);
  if (!card) return {};
  const d = rejectIt ? await reject(t, v, card, e) : await approve(t, v, card, e);
  if (!d) return {};
  const ok = t.ok(await v.waitFor(card.getByText(want), 3000), `no outcome "${want}" in the card`);
  if (ok) t.ok(await inLiveRegion(v.frame, want, 'polite'), 'the outcome is not announced in a polite live region');
  t.ok(!(await v.visible(d.dlg)), 'the dialog stayed open after the answer');
  if (extra) await extra(v, card, e);
  return { v, card, e };
}
const decidedState = async (t, v, card, e) => {
  t.ok(!(await v.visible(card.getByRole('button', { name: /^(Approve|Reject|Preview)/ }))), 'the decided card still offers its action buttons');
  t.ok(await v.visible(card.getByText(e.title, { exact: true })), 'the decided card lost its title');
};

check('draft9.outcome.no-names', '7.1 AC11, States Decided; draft.9 owner decision 2', 'approved with others next: "continues in midPoint", no names; the card keeps its bands, loses its buttons', (t) =>
  outcomeCheck(t, {
    decide: 'decide.approve-next',
    want: S.inbox.outcome.approvedOpen,
    extra: async (v, card, e) => {
      await decidedState(t, v, card, e);
      const text = await v.text(card);
      for (const re of S.d37.forbiddenOutcome) t.ok(!re.test(text), `the outcome says "${text.match(re)?.[0]}"`);
      for (const n of fixture('decide.approve-next').result.structuredContent.nextApprovers) {
        t.ok(!text.includes(n.displayName || n.name), `the outcome names the next approver "${n.displayName || n.name}"`);
      }
    },
  }));
check('ac11.outcome.approved-closed', '7.1 AC11', 'approved, case closed', (t) =>
  outcomeCheck(t, { decide: 'decide.approve-closed', want: S.inbox.outcome.approvedClosed }));
check('ac11.outcome.approved-open', '7.1 AC11', 'approved, case open, nobody known next', (t) =>
  outcomeCheck(t, { entry: 'inbox.manager', which: 1, decide: 'decide.approve-open', want: S.inbox.outcome.approvedOpen }));
check('ac11.outcome.rejected-closed', '7.1 AC11', 'rejected, case closed', (t) =>
  outcomeCheck(t, { decide: 'decide.reject-closed', rejectIt: true, want: S.inbox.outcome.rejectedClosed }));
check('ac11.outcome.rejected-open', '7.1 AC11', 'rejected, case still open', (t) =>
  outcomeCheck(t, { decide: 'decide.reject-open', rejectIt: true, want: S.inbox.outcome.rejectedOpen }));
check('ac11.outcome.decided-by-other', '7.1 AC11', 'recorded outcome differs: someone else decided first (warning)', (t) =>
  outcomeCheck(t, { decide: 'decide.reject-by-other', rejectIt: true, want: S.inbox.outcome.decidedByOther('approve') }));
check('ac11.outcome.unconfirmed', '7.1 AC11', 'no recorded outcome yet: not confirmed (warning)', (t) =>
  outcomeCheck(t, { decide: 'decide.approve-unconfirmed', want: S.inbox.outcome.unconfirmed }));

const WARNING_OR_DANGER = [...RGB.warning, ...RGB.danger];
check('ac11.outcome.warning-style', '7.1 AC11', '"someone else decided first" and "not confirmed yet" are warnings, not success', async (t) => {
  for (const [decide, rejectIt, want] of [['decide.reject-by-other', true, S.inbox.outcome.decidedByOther('approve')], ['decide.approve-unconfirmed', false, S.inbox.outcome.unconfirmed]]) {
    const { v, card } = await outcomeCheck(t, { decide, rejectIt, want });
    if (!card) continue;
    const msg = card.getByText(want);
    if (await v.visible(msg)) t.ok((await colorsOf(msg)).some((c) => WARNING_OR_DANGER.includes(c)), `"${want}" is not in warning style`);
  }
});

check('ac11.outcome.no-raw-word', '7.1 AC11', 'an unknown recorded outcome reads "decided", never midPoint\'s word', async (t) => {
  const fx = derive('decide.reject-by-other', 'raw-outcome-word', M.rawOutcomeWord);
  const { v, e, card } = await inbox(t, 'inbox.approver', { tools: { decide_work_item: [{ result: fx.result }] } });
  if (!card) return;
  await reject(t, v, card, e);
  const want = S.inbox.outcome.decidedByOther('skip');
  t.ok(await v.waitFor(card.getByText(want)), `no "${want}"`);
  t.ok(!/\bskip\b/.test(await v.text(card)), 'midPoint\'s raw outcome word is shown');
});

check('ac11.model-context', '7.1 AC11, 6.14', 'after a decision: ui/update-model-context with the tool text only; none without the capability', async (t) => {
  const fx = fixture('decide.approve-next');
  const { v, e, card } = await inbox(t, 'inbox.approver', { tools: decideTools('decide.approve-next') });
  if (card) {
    await approve(t, v, card, e);
    await v.waitFor(S.inbox.outcome.approvedOpen);
    await settle();
    const sent = await v.sent('ui/update-model-context');
    t.ok(sent.length === 1, `${sent.length} ui/update-model-context after one decision, want 1`);
    const p = sent[0]?.params ?? {};
    t.ok(JSON.stringify(p.content) === JSON.stringify(fx.result.content), `model context ${JSON.stringify(p).slice(0, 200)}, want the decide result's content`);
    t.ok(p.structuredContent === undefined, 'model context carries structuredContent');
  }
  const r = await inbox(t, 'inbox.approver', { tools: decideTools('decide.approve-next'), caps: { serverTools: {}, openLinks: {}, message: {} } });
  if (r.card) {
    await approve(t, r.v, r.card, r.e);
    await r.v.waitFor(S.inbox.outcome.approvedOpen);
    await settle();
    t.ok((await r.v.sent('ui/update-model-context')).length === 0, 'ui/update-model-context without the capability');
  }
});

// AC11: write errors (6.8)
async function writeError(t, name, code, { disables }) {
  const { v, e, card } = await inbox(t, 'inbox.approver', { tools: decideTools(name) });
  if (!card) return;
  const d = await approve(t, v, card, e);
  if (!d) return;
  const msg = card.getByText(S.errorByCode[code]);
  if (!t.ok(await v.waitFor(msg, 3000), `no "${S.errorByCode[code]}" in the card`)) return;
  t.ok(await inLiveRegion(v.frame, S.errorByCode[code], 'assertive'), 'the write error is not announced in an assertive live region');
  const approveBtn = card.getByRole('button', { name: e.approveLabel, exact: true });
  const usable = (await v.visible(approveBtn)) && (await approveBtn.first().isEnabled());
  if (disables) {
    t.ok(!usable, 'the card stays actionable after an error that ends it');
    t.ok(await v.visible(v.button(S.refresh)), 'Refresh is not suggested');
  } else {
    t.ok(usable, 'the card lost its actions after an error that does not end it');
  }
  await settle(1500);
  t.ok((await v.calls('decide_work_item')).length === 1, 'the failed write was retried');
}
check('ac11.error.not-in-inbox', '7.1 Errors, 6.8', 'not-in-inbox: the sentence, actions disabled, Refresh suggested, no retry', (t) =>
  writeError(t, 'error.decide.not-in-inbox', 'not-in-inbox', { disables: true }));
check('ac11.error.already-decided', '7.1 Errors, 6.8', 'already-decided: actions disabled', (t) =>
  writeError(t, 'error.decide.already-decided', 'already-decided', { disables: true }));
check('ac11.error.request-closed', '7.1 Errors, 6.8', 'request-closed: actions disabled', (t) =>
  writeError(t, 'error.decide.request-closed', 'request-closed', { disables: true }));
check('ac11.error.not-authorized', '7.1 Errors, 6.8', 'not-authorized: the sentence, the card stays actionable', (t) =>
  writeError(t, 'error.decide.not-authorized', 'not-authorized', { disables: false }));
check('ac11.error.shared-credential', '7.1 Errors, 6.8', 'shared-credential on a decision', (t) =>
  writeError(t, 'error.decide.shared-credential', 'shared-credential', { disables: false }));

check('errors.details-redacted', '6.8', 'technical details: Tool line and the raw text with addresses removed', async (t) => {
  const fx = derive('error.decide.not-authorized', 'error-with-address', M.errorWithAddress);
  const { v, e, card } = await inbox(t, 'inbox.approver', { tools: { decide_work_item: [{ result: fx.result }] } });
  if (!card) return;
  await approve(t, v, card, e);
  const show = card.getByRole('button', { name: S.showDetails, exact: true }).or(v.button(S.showDetails));
  if (!t.ok(await v.waitFor(show, 3000), `no "${S.showDetails}"`)) return;
  t.ok(!(await v.text()).includes('unexpected status 403'), 'the raw error text shows before the disclosure opens');
  await show.first().click();
  t.ok(await v.waitFor(S.detailsTool('decide_work_item')), `no "${S.detailsTool('decide_work_item')}"`);
  const text = await v.text();
  t.ok(text.includes('unexpected status 403'), 'the raw error text is not in the details');
  t.ok(text.includes(S.redacted) && !text.includes('https://'), 'the address is not replaced by common.redacted');
  t.ok(await v.visible(v.button(S.hideDetails)), `the toggle does not read "${S.hideDetails}" when open`);
});

check('errors.fallback-by-text', '6.8', 'an error without a code is classified by its text', async (t) => {
  const fx = derive('error.decide.not-authorized', 'error-without-code', M.errorWithoutCode);
  const { v, e, card } = await inbox(t, 'inbox.approver', { tools: { decide_work_item: [{ result: fx.result }] } });
  if (!card) return;
  await approve(t, v, card, e);
  t.ok(await v.waitFor(card.getByText(S.errorByCode['not-authorized']), 3000), 'the text fallback did not find error.notAuthorized');
});

check('errors.host-refused', '6.8', 'a JSON-RPC error on the view\'s call: error.hostRefused', async (t) => {
  const { v, e, card } = await inbox(t, 'inbox.approver', { tools: { decide_work_item: [{ rpcError: { code: -32000, message: 'Refused by the host' } }] } });
  if (!card) return;
  await approve(t, v, card, e);
  t.ok(await v.waitFor(v.frame.getByText(S.hostRefused), 3000), `no "${S.hostRefused}"`);
});

// AC12: dry run
check('ac12.dry-run.banner-and-buttons', '7.1 AC12, 6.7 Dry run', 'writes off: the banner, "Preview…" buttons whose names start with their text', async (t) => {
  const { v, e, card } = await inbox(t, 'inbox.approver-dry-run');
  if (!card) return;
  t.ok(await v.hasText(S.dryrun.bannerTitle), `no "${S.dryrun.bannerTitle}"`);
  t.ok(await v.hasText(S.dryrun.bannerBody), 'no dryrun.banner.body');
  for (const [label, name] of [[S.inbox.previewApprove, e.previewApproveLabel], [S.inbox.previewReject, e.previewRejectLabel]]) {
    const b = card.getByRole('button', { name, exact: true });
    if (t.ok(await v.visible(b), `no button named "${name}"`)) {
      t.ok(norm(await b.first().innerText()) === label, `button "${name}" reads "${norm(await b.first().innerText())}", want "${label}"`);
    }
  }
  t.ok(!(await v.visible(card.getByRole('button', { name: e.approveLabel, exact: true }))), 'a plain Approve in a dry run');
});

check('ac12.dry-run.outcome', '7.1 AC12, 6.7 Dry run', 'the preview: "Show preview", the preview title, summary only in technical details, no success claim', async (t) => {
  const fx = fixture('decide.dry-run-approve');
  const { v, e, card } = await inbox(t, 'inbox.approver-dry-run', { tools: { decide_work_item: [{ result: fx.result }] } });
  if (!card) return;
  const d = await decideFrom(t, v, card, e.previewApproveLabel, { submit: S.dryrun.submit });
  if (!d) return;
  if (!t.ok(await v.waitFor(S.dryrun.resultTitle, 3000), `no "${S.dryrun.resultTitle}"`)) return;
  const text = await v.text();
  for (const s of [S.inbox.outcome.approvedClosed, S.inbox.outcome.approvedOpen, 'Approved.', 'Rejected.']) {
    t.ok(!text.includes(s), `a dry run claims "${s}"`);
  }
  const summary = fx.result.structuredContent.summary;
  t.ok(!text.includes(summary), 'the server summary shows outside the technical details');
  const show = v.button(S.showDetails);
  if (t.ok(await v.visible(show), `no "${S.showDetails}" on the preview`)) {
    await show.first().click();
    t.ok(await v.waitFor(S.dryrun.detailsRequest), `no "${S.dryrun.detailsRequest}"`);
    t.ok(await v.hasText(S.dryrun.detailsSummary(summary)), `no "${S.dryrun.detailsSummary(summary)}"`);
  }
});

// AC13: the intermediary slot
async function slotCheck(t, { result, wants, mustNot = [] }) {
  const { v, e, card } = await inbox(t, 'inbox.approver', { tools: { decide_work_item: [{ result }] } });
  if (!card) return;
  await approve(t, v, card, e);
  await settle(600);
  const text = await v.text();
  for (const w of wants) t.ok(w instanceof RegExp ? w.test(text) : lines(text).includes(w) || text.includes(w), `no "${w}"`);
  for (const s of [...mustNot, 'Approved.', 'Rejected.']) t.ok(!text.includes(s), `shows "${s}"`);
  return { v, e, card };
}
check('ac13.held', '7.1 AC13, 5.5', 'held: never success, names the approver and the source; the card returns to its pre-action state', async (t) => {
  const fx = derive('decide.approve-next', 'held', M.held());
  const r = await slotCheck(t, { result: fx.result, wants: [S.strip.heldTitle, S.strip.heldBody('Access governance team'), S.strip.reason('Approving access needs a second person.'), S.strip.source('Policy gateway'), S.strip.ref('a41c-77f0')] });
  if (r?.card) t.ok(await r.v.visible(r.card.getByRole('button', { name: r.e.approveLabel, exact: true })), 'after a held decision the card lost its Approve button');
});
check('ac13.held-unnamed', '7.1 AC13, 5.5', 'held without approver or source: the no-approver body, "Reported by the connection …"', (t) =>
  slotCheck(t, { result: derive('decide.approve-next', 'held-unnamed', M.held({ approver: null, source: null })).result, wants: [S.strip.heldTitle, S.strip.heldBodyNoApprover, S.strip.sourceUnnamed] }));
check('ac13.denied', '7.1 AC13, 5.5', 'denied: never success, names its source', (t) =>
  slotCheck(t, { result: derive('decide.approve-next', 'denied', M.denied()).result, wants: [S.strip.deniedTitle, S.strip.deniedBody, S.strip.reason('Approvals from this assistant are turned off.'), S.strip.source('Policy gateway')] }));
check('ac13.allowed', '7.1 AC13, 5.5 (D18)', 'allowed: the normal outcome and nothing more (no source, no actor)', async (t) => {
  const { v, e, card } = await inbox(t, 'inbox.approver', { tools: { decide_work_item: [{ result: derive('decide.approve-next', 'allowed', M.allowed).result }] } });
  if (!card) return;
  await approve(t, v, card, e);
  t.ok(await v.waitFor(S.inbox.outcome.approvedOpen, 3000), 'no normal outcome with an allowed slot');
  const text = await v.text();
  for (const s of ['Reported by', 'Policy gateway', 'Team assistant', S.strip.heldTitle, S.strip.deniedTitle]) t.ok(!text.includes(s), `an allowed slot shows "${s}"`);
});
check('ac13.unknown-version', '5.3', 'a slot with an unknown version is ignored', async (t) => {
  const { v, e, card } = await inbox(t, 'inbox.approver', { tools: { decide_work_item: [{ result: derive('decide.approve-next', 'slot-v2', M.slotUnknownVersion).result }] } });
  if (!card) return;
  await approve(t, v, card, e);
  t.ok(await v.waitFor(S.inbox.outcome.approvedOpen, 3000), 'the normal outcome is not shown');
  t.ok(!(await v.hasText(S.strip.deniedTitle)), 'a v2 slot was rendered');
});
check('ac13.entry-held', '5.5, 6.2', 'a held entry result: the panel in place of the content; whoami for the header', async (t) => {
  const fx = derive('inbox.approver', 'held', M.held());
  const v = await t.open({ entry: fx, tools: { whoami: [{ result: fixture('whoami.approver').result }] } });
  t.ok(await v.waitFor(S.strip.heldTitle, 3000), `no "${S.strip.heldTitle}"`);
  t.ok(await v.hasText(S.strip.source('Policy gateway')), 'the panel does not name its source');
  t.ok(!(await v.hasText(/requests? waiting for your decision/)), 'the summary shows with a held entry');
  await settle(400);
  const who = await v.calls('whoami');
  t.ok(who.length === 1 && JSON.stringify(who[0].args ?? {}) === '{}', `whoami calls ${JSON.stringify(who)}, want one with {}`);
});

// AC14
check('ac14.row-button-names', '7.1 AC14, 6.12', 'row buttons carry the catalog\'s names; every name starts with its visible text', async (t) => {
  for (const name of ['inbox.approver', 'inbox.manager', 'inbox.removal']) {
    const fx = fixture(name);
    const v = await t.open({ entry: fx });
    for (const wi of items(fx.result)) {
      const e = expectItem(wi);
      const card = await v.card(e);
      if (!card) { t.fail(`${name}: no card "${e.title}"`); continue; }
      for (const [label, visible] of [[e.approveLabel, S.inbox.approve], [e.rejectLabel, S.inbox.reject]]) {
        const b = card.getByRole('button', { name: label, exact: true });
        if (t.ok(await v.visible(b), `${name}: no button named "${label}"`)) {
          t.ok(norm(await b.first().innerText()) === visible, `${name}: "${label}" reads "${norm(await b.first().innerText())}", want "${visible}"`);
        }
      }
    }
    for (const b of await v.frame.getByRole('button').all()) {
      if (!(await b.isVisible())) continue;
      const shown = norm(await b.innerText());
      const acc = await v.accessibleName(b);
      if (shown && !acc.startsWith(shown)) t.fail(`${name}: button "${shown}" is named "${acc}" (label in name, WCAG 2.5.3)`);
    }
  }
});

// AC15
check('ac15.refresh', '7.1 AC15, 6.6', 'Refresh re-calls list_work_items with the entry arguments; refreshing state; As of', async (t) => {
  const { v, card } = await inbox(t, 'inbox.approver', { inputArgs: { limit: 20 }, tools: { list_work_items: [{ result: fixture('inbox.approver').result, hold: true }] } });
  if (!card) return;
  const btn = v.button(S.refresh);
  if (!t.ok(await v.visible(btn), `no "${S.refresh}"`)) return;
  await btn.first().click();
  await settle();
  const calls = await v.calls('list_work_items');
  t.ok(calls.length === 1 && JSON.stringify(calls[0].args) === '{"limit":20}', `Refresh calls ${JSON.stringify(calls)}, want one with {"limit":20}`);
  const busy = v.button(S.refreshing);
  t.ok(await v.visible(busy), `no "${S.refreshing}" while refreshing`);
  if (await v.visible(busy)) t.ok(await busy.first().isDisabled(), `"${S.refreshing}" is not disabled`);
  t.ok((await v.frame.locator('[aria-busy="true"]').count()) > 0, 'nothing is aria-busy while refreshing');
  t.ok(await v.visible(card), 'the previous content disappeared while refreshing');
  await v.release();
  t.ok(await v.waitFor(btn), `"${S.refresh}" does not come back`);
  t.ok(await v.waitFor(S.asOf), 'no "As of …" after the refresh');
});

check('ac15.refresh-failed', '6.8, 6.6', 'a failed Refresh keeps the snapshot and shows the error above it', async (t) => {
  const { v, card } = await inbox(t, 'inbox.approver', { tools: { list_work_items: [{ result: fixture('error.list.midpoint-unavailable').result }] } });
  if (!card) return;
  await v.button(S.refresh).first().click();
  t.ok(await v.waitFor(S.errorByCode['midpoint-unavailable'], 3000), 'no error.midpointUnavailable after a failed Refresh');
  t.ok(await v.visible(card), 'the snapshot is gone after a failed Refresh');
});

check('ac15.outcome-mode', '7.1 States Outcome mode, 6.6', 'entry decide_work_item: one outcome card, then one list_work_items {limit: 50}', async (t) => {
  const fx = fixture('decide.approve-next');
  const v = await t.open({ entry: fx, tools: { list_work_items: [{ result: fixture('inbox.empty').result }] } });
  t.ok(await v.waitFor(S.inbox.outcome.approvedOpen, 3000), 'no outcome from the decide entry result');
  await settle(600);
  const calls = await v.calls('list_work_items');
  t.ok(calls.length === 1 && JSON.stringify(calls[0].args) === '{"limit":50}', `list_work_items calls ${JSON.stringify(calls)}, want one with {"limit":50}`);
  t.ok((await v.calls('decide_work_item')).length === 0, 'the view repeated the agent\'s decision');
  t.ok(!(await v.visible(v.dialog())), 'a confirm dialog opened in outcome mode');
  t.ok(await v.waitFor(S.inbox.emptyTitle), 'the inbox did not follow the outcome');
});

check('ac15.no-polling', '7.1 AC15, 6.6', 'idle: no calls, no refresh on focus or visibility, no repeating timers', async (t) => {
  const { v, card } = await inbox(t, 'inbox.approver', { tools: { list_work_items: [{ result: fixture('inbox.approver').result }] } });
  if (!card) return;
  await settle(500);
  const before = await v.instrumented();
  await v.frame.evaluate(() => {
    window.dispatchEvent(new Event('focus'));
    document.dispatchEvent(new Event('visibilitychange'));
    window.dispatchEvent(new Event('online'));
  });
  await sleep(3000);
  const after = await v.instrumented();
  t.ok((await v.calls()).length === 0, `calls while idle: ${JSON.stringify(await v.calls())}`);
  if (before && after) {
    t.ok(after.timeouts.length - before.timeouts.length <= 2, `${after.timeouts.length - before.timeouts.length} timers scheduled while idle`);
    t.ok(after.intervals.length === 0, `setInterval used: ${JSON.stringify(after.intervals.slice(0, 2))}`);
    t.ok(after.frames - before.frames <= 5, `${after.frames - before.frames} animation frames requested while idle`);
  } else {
    t.fail('the instrumentation did not load in the view frame');
  }
});

// AC16
check('ac16.shared-credential', '7.1 AC16, 6.2', 'shared-credential refusal: error.sharedCredential, header from one whoami', async (t) => {
  const v = await t.open({ entry: fixture('error.list.shared-credential'), tools: { whoami: [{ result: fixture('whoami.shared').result }] } });
  t.ok(await v.waitFor(S.errorByCode['shared-credential'], 3000), 'no error.sharedCredential');
  await settle(500);
  const who = await v.calls('whoami');
  t.ok(who.length === 1 && JSON.stringify(who[0].args ?? {}) === '{}', `whoami calls ${JSON.stringify(who)}, want one with {}`);
  t.ok(await v.waitFor(S.headerShared('Access service')), `no "${S.headerShared('Access service')}"`);
  t.ok(await v.hasText(S.headerPersonal('Access service')), `no "${S.headerPersonal('Access service')}"`);
});

check('ac16.shared-credential.read-only', '6.2', 'without serverTools: no whoami, no identity line', async (t) => {
  const v = await t.open({ entry: fixture('error.list.shared-credential'), caps: NO_TOOLS });
  t.ok(await v.waitFor(S.errorByCode['shared-credential'], 3000), 'no error.sharedCredential');
  await settle(400);
  t.ok((await v.calls()).length === 0, 'a call on a read-only host');
  t.ok(!(await v.hasText('midPoint sees everything here as', { loose: true })), 'an identity line without whoami');
});

check('errors.entry', '6.8', 'an error entry result: the sentence from its code, details on demand', async (t) => {
  const v = await t.open({ entry: fixture('error.list.midpoint-unavailable'), tools: { whoami: [{ result: fixture('whoami.approver').result }] } });
  t.ok(await v.waitFor(S.errorByCode['midpoint-unavailable'], 3000), 'no error.midpointUnavailable');
  t.ok(await v.visible(v.button(S.showDetails)), `no "${S.showDetails}"`);
  t.ok(!(await v.text()).includes('unexpected status 503'), 'the raw error text shows outside the details');
});

// AC17: shared criteria
check('shared.keyboard', '7.1 AC17, 6.12', 'approve with the keyboard alone; no single-key shortcuts', async (t) => {
  const { v, e, card } = await inbox(t, 'inbox.approver', { tools: decideTools('decide.approve-next') });
  if (!card) return;
  for (const k of ['a', 'r', 'y', 'Enter']) await v.page.keyboard.press(k);
  await settle(200);
  t.ok(!(await v.visible(v.dialog())), 'a key press opened a dialog without focus on a button');
  const target = card.getByRole('button', { name: e.approveLabel, exact: true });
  let reached = false;
  await v.frame.locator('body').focus().catch(() => {});
  for (let i = 0; i < 60 && !reached; i++) {
    await v.page.keyboard.press('Tab');
    reached = await v.isFocused(target);
  }
  if (!t.ok(reached, `"${e.approveLabel}" not reachable with Tab`)) return;
  await v.page.keyboard.press('Enter');
  if (!t.ok(await v.waitFor(v.dialog()), 'Enter on Approve opened no dialog')) return;
  await v.page.keyboard.type('ok');
  const submit = v.dialog().getByRole('button', { name: S.confirm.approveSubmit, exact: true });
  let onSubmit = false;
  for (let i = 0; i < 6 && !onSubmit; i++) {
    await v.page.keyboard.press('Tab');
    onSubmit = await v.isFocused(submit);
  }
  if (!t.ok(onSubmit, 'the dialog\'s Approve not reachable with Tab')) return;
  await v.page.keyboard.press('Enter');
  await settle(500);
  const c = await v.calls('decide_work_item');
  t.ok(c.length === 1 && c[0].args.comment === 'ok', `keyboard decision sent ${JSON.stringify(c)}`);
});

check('shared.focus-indicator', '6.12', 'keyboard focus shows midPoint\'s #ffa836 outline', async (t) => {
  const { v, card } = await inbox(t, 'inbox.approver');
  if (!card) return;
  await v.page.keyboard.press('Tab');
  await v.page.keyboard.press('Tab');
  const st = await v.frame.evaluate(() => {
    const a = document.activeElement;
    if (!a || a === document.body) return null;
    const cs = getComputedStyle(a);
    return { outline: `${cs.outlineStyle} ${cs.outlineWidth} ${cs.outlineColor}`, shadow: cs.boxShadow, tag: a.tagName };
  });
  if (!t.ok(st, 'Tab focused nothing in the view')) return;
  t.ok(st.outline.includes(RGB.focus) || st.shadow.includes(RGB.focus), `focused ${st.tag} has outline "${st.outline}" / shadow "${st.shadow}", want ${RGB.focus}`);
});

async function contrastIssues(v) {
  return v.frame.evaluate(() => {
    const parse = (c) => {
      const m = /rgba?\(([\d.]+),\s*([\d.]+),\s*([\d.]+)(?:,\s*([\d.]+))?\)/.exec(c);
      return m ? { r: +m[1], g: +m[2], b: +m[3], a: m[4] === undefined ? 1 : +m[4] } : null;
    };
    const lum = ({ r, g, b }) => {
      const f = (x) => { x /= 255; return x <= 0.03928 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4; };
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
    };
    const bgOf = (el) => {
      for (let n = el; n; n = n.parentElement) {
        const c = parse(getComputedStyle(n).backgroundColor);
        if (c && c.a > 0.5) return c;
      }
      return null;
    };
    const out = [];
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    const seen = new Set();
    for (let tn = walker.nextNode(); tn; tn = walker.nextNode()) {
      const el = tn.parentElement;
      if (!el || seen.has(el) || !tn.textContent.trim()) continue;
      seen.add(el);
      const cs = getComputedStyle(el);
      if (cs.visibility === 'hidden' || el.getClientRects().length === 0 || el.closest('[disabled],[aria-disabled="true"],[aria-hidden="true"]')) continue;
      let opacity = 1;
      for (let n = el; n; n = n.parentElement) opacity *= +getComputedStyle(n).opacity;
      if (opacity < 0.99) continue; // dimmed while refreshing; judged when idle
      const fg = parse(cs.color);
      const bg = bgOf(el);
      if (!fg || !bg) {
        if (!bg) out.push({ text: tn.textContent.trim().slice(0, 40), why: 'no painted background' });
        continue;
      }
      const l1 = lum(fg); const l2 = lum(bg);
      const ratio = (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
      const size = parseFloat(cs.fontSize);
      const large = size >= 24 || (size >= 18.66 && +cs.fontWeight >= 700);
      if (ratio < (large ? 3 : 4.5)) out.push({ text: tn.textContent.trim().slice(0, 40), ratio: Math.round(ratio * 100) / 100, fg: cs.color, bg: `rgb(${bg.r}, ${bg.g}, ${bg.b})` });
    }
    return out;
  });
}

for (const theme of ['light', 'dark']) {
  check(`shared.contrast.${theme}`, '7.1 AC17, 6.9, 6.12', `text contrast at least 4.5:1 (3:1 large) in the ${theme} theme`, async (t) => {
    const { v, card } = await inbox(t, 'inbox.approver', { context: { theme }, tools: caseTools() });
    if (!card) return;
    await v.details(card);
    await settle(500);
    await t.shot(v, theme);
    const issues = await contrastIssues(v);
    t.ok(issues.length === 0, `${issues.length} low-contrast texts, e.g. ${JSON.stringify(issues.slice(0, 3))}`);
  });
}

check('shared.themes', '6.9', 'hostContext.theme sets data-theme; the dark palette differs; host-context-changed follows live', async (t) => {
  const bg = (v) => v.frame.evaluate(() => {
    const c = [document.body, document.documentElement].map((n) => getComputedStyle(n).backgroundColor).find((x) => x && x !== 'rgba(0, 0, 0, 0)');
    return { theme: document.documentElement.getAttribute('data-theme'), bg: c ?? null };
  });
  const { v: light, card } = await inbox(t, 'inbox.approver', { context: { theme: 'light' } });
  if (!card) return;
  const l = await bg(light);
  t.ok(l.theme === 'light', `light: data-theme ${l.theme}`);
  const { v: dark } = await inbox(t, 'inbox.approver', { context: { theme: 'dark' } });
  const d = await bg(dark);
  t.ok(d.theme === 'dark', `dark: data-theme ${d.theme}`);
  t.ok(l.bg && d.bg && l.bg !== d.bg, `light and dark paint the same background (${l.bg} / ${d.bg})`);
  await t.shot(light, 'light');
  await t.shot(dark, 'dark');
  await light.setTheme('dark');
  await settle(300);
  t.ok((await bg(light)).theme === 'dark', 'host-context-changed to dark did not switch the theme');
});

check('shared.narrow', '6.1', 'no horizontal scrolling at 320 px', async (t) => {
  const { v, card } = await inbox(t, 'inbox.approver', { width: 320, context: { containerDimensions: { maxHeight: 1600, width: 320 } }, tools: caseTools() });
  if (!card) return;
  await v.details(card);
  await settle(400);
  const m = await v.frame.evaluate(() => ({ sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth }));
  t.ok(m.sw <= m.cw + 1, `content is ${m.sw} px wide in a ${m.cw} px frame`);
  await t.shot(v, 'narrow');
});

// ===================================================================
// Run-wide checks over every view the run opened
// ===================================================================

const viewConsole = (d) => d.console.filter((m) => m.type === 'error' && (m.url === '' || m.url.startsWith(VIEW_URL)));

across('shared.no-network', '7.1 AC17, 3.2, 3.6', 'the view made no network request (routes, CSP, network APIs)', (t) => {
  for (const d of diagnostics) {
    const reqs = d.routed.filter((r) => r.frameUrl.startsWith(VIEW_URL));
    if (reqs.length) t.fail(`${d.check}: requests ${JSON.stringify(reqs.slice(0, 3))}`);
    if (d.inst?.network?.length) t.fail(`${d.check}: ${JSON.stringify(d.inst.network.slice(0, 3).map((n) => `${n.api} ${n.target}`))}`);
    if (d.inst?.csp?.length) t.fail(`${d.check}: CSP violations ${JSON.stringify(d.inst.csp.slice(0, 3))}`);
    if (d.inst?.popups?.length) t.fail(`${d.check}: window.open ${JSON.stringify(d.inst.popups)}`);
  }
});

across('shared.no-console-errors', '7.1 AC17', 'no console errors or uncaught exceptions in the view', (t) => {
  for (const d of diagnostics) {
    const errs = [...viewConsole(d).map((m) => m.text), ...(d.inst?.errors ?? [])];
    if (errs.length) t.fail(`${d.check}: ${JSON.stringify([...new Set(errs)].slice(0, 3))}`);
  }
});

across('shared.no-storage', '3.6', 'no browser storage or cookies', (t) => {
  for (const d of diagnostics) if (d.inst?.storage?.length) t.fail(`${d.check}: ${[...new Set(d.inst.storage.map((s) => s.api))].join(', ')}`);
});

// The slow notice may wake a little after the oldest wait reaches 8 s, or sooner when a wait
// began earlier, so any timeout up to SLOW_MAX_MS counts as that notice; nothing may run longer.
const SLOW_MAX_MS = 8100;
across('ac15.no-timers', '7.1 AC15, 6.6, 6.5 (D27)', 'no setInterval; no timer longer than the 8 s slow notice', (t) => {
  for (const d of diagnostics) {
    if (!d.inst) continue;
    if (d.inst.intervals.length) t.fail(`${d.check}: setInterval(${d.inst.intervals[0].ms}) at ${d.inst.intervals[0].stack}`);
    const long = d.inst.timeouts.filter((x) => x.ms > SLOW_MAX_MS);
    if (long.length) t.fail(`${d.check}: setTimeout(${long[0].ms}) at ${long[0].stack}`);
  }
  if (!diagnostics.some((d) => d.inst)) t.fail('the instrumentation never loaded in a view frame');
});

across('bridge.protocol', '3.3, 3.4, 7.1 Tools', 'only allowed tools and arguments, capabilities respected, every call anticipated', (t) => {
  for (const d of diagnostics) {
    for (const x of d.state?.violations ?? []) t.fail(`${d.check}: ${x}`);
    for (const u of d.state?.unanswered ?? []) t.fail(`${d.check}: unexpected call ${u.name} ${JSON.stringify(u.args)}`);
  }
});
