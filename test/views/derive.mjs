// Fixtures and what the harness derives from them.
//
// Real fixtures (fixtures/*.json) are tool results this server produced
// (view_fixtures_test.go). Edge cases the recordings can't produce are
// derived here by mutating a copy of a real fixture; each mutation is named
// and says what it changes. Expectations (titles, labels) are computed from
// the data with the naming rules of contract 4.5, so a check never hard-codes
// a name the data doesn't give.

import { readFileSync } from 'node:fs';
import { S } from './strings.mjs';

const fixtureDir = new URL('./fixtures/', import.meta.url);

// fixture returns a fresh copy of a real fixture: { call, about, result }.
export function fixture(name) {
  const fx = JSON.parse(readFileSync(new URL(`${name}.json`, fixtureDir), 'utf8'));
  fx.name = name;
  return fx;
}

// derive copies a fixture and applies a named mutation to its result.
export function derive(base, mutation, change) {
  const fx = typeof base === 'string' ? fixture(base) : structuredClone(base);
  change(fx.result, fx);
  fx.name = `${fx.name}+${mutation}`;
  fx.about = `${fx.about} [derived: ${mutation}]`;
  return fx;
}

export const items = (result) => result.structuredContent.workItems;

// --- the clock and time zone ---

// hostZone picks a fixed-offset zone in which it is late morning now, so
// "in 2 hours" is still today and "in 26 hours" is tomorrow whenever the
// harness runs. Etc/GMT-N is UTC+N.
export function hostZone(now = new Date()) {
  let h = 11 - now.getUTCHours();
  if (h < -12) h += 24;
  if (h > 14) h -= 24;
  const timeZone = h === 0 ? 'Etc/UTC' : h > 0 ? `Etc/GMT-${h}` : `Etc/GMT+${-h}`;
  return { timeZone, offsetHours: h };
}

const HOUR = 3600 * 1000;
const DAY = 24 * HOUR;

// localParts returns a date's wall-clock parts in the zone.
export function localParts(date, zone) {
  const f = new Intl.DateTimeFormat('en-GB', {
    timeZone: zone.timeZone, year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
  });
  const p = Object.fromEntries(f.formatToParts(date).map((x) => [x.type, x.value]));
  return { y: +p.year, m: +p.month, d: +p.day, hh: +p.hour, mm: +p.minute };
}

// endOfDayIn returns the RFC 3339 end (23:59:59) of the day `days` after
// today in the zone, with the zone's offset.
export function endOfDayIn(days, zone, now = new Date()) {
  const p = localParts(new Date(now.getTime() + days * DAY), zone);
  const off = zone.offsetHours;
  const sign = off < 0 ? '-' : '+';
  const pad = (n) => String(Math.abs(n)).padStart(2, '0');
  return `${p.y}-${pad(p.m)}-${pad(p.d)}T23:59:59${sign}${pad(off)}:00`;
}

// clockMatches reports whether text says hh:mm of date in the zone, after a
// prefix ("Decide by today, ").
export function clockMatches(text, prefix, date, zone) {
  if (!text.startsWith(prefix)) return false;
  const m = /^(\d{1,2}):(\d{2})/.exec(text.slice(prefix.length));
  if (!m) return false;
  const p = localParts(date, zone);
  return +m[1] === p.hh && +m[2] === p.mm;
}

// --- naming (contract 4.5) ---

export function personName(ref, start = false) {
  if (!ref || ref.readable === false || (!ref.displayName && !ref.name)) return start ? S.personHiddenStart : S.personHidden;
  return ref.displayName || ref.name;
}

export function objectName(ref) {
  return ref?.displayName || ref?.name || S.itemHidden;
}

// expectItem is what a work item's card should say about it.
export function expectItem(wi) {
  const c = wi.context;
  const requesteeStart = personName(c.requestee, true);
  const requestee = personName(c.requestee);
  const target = objectName(c.target);
  const change = S.inbox.change[c.change] ? c.change : 'unknown';
  const selfRequest = c.requester?.oid && c.requester.oid === c.requestee?.oid;
  const requesterHidden = personName(c.requester) === S.personHidden;
  return {
    wi,
    change,
    requestee,
    target,
    title: S.inbox.change[change](requesteeStart, target),
    // The spoken title is defined for the arrow form only (D33).
    label: change === 'add' ? S.inbox.changeAddLabel(requesteeStart, target) : S.inbox.change[change](requesteeStart, target),
    approveLabel: S.inbox.approveLabel(target, requestee),
    rejectLabel: S.inbox.rejectLabel(target, requestee),
    previewApproveLabel: S.inbox.previewApproveLabel(target, requestee),
    previewRejectLabel: S.inbox.previewRejectLabel(target, requestee),
    approveTitle: S.confirm.approveTitle(change, target, requestee),
    rejectTitle: S.confirm.rejectTitle(change, target, requestee),
    rejectBody: requesterHidden ? S.confirm.rejectBodyHidden : S.confirm.rejectBody(personName(c.requester, true)),
    requestedBy: selfRequest ? S.inbox.selfRequested(requestee) : S.inbox.requestedBy(personName(c.requester)),
    readable: c.requestee?.readable !== false,
  };
}

// loginNames are the login names a result carries for people who have a
// display name or can't be read: none of them may reach the surface (D16).
export function loginNames(result) {
  const out = new Set();
  const visit = (v) => {
    if (Array.isArray(v)) return v.forEach(visit);
    if (!v || typeof v !== 'object') return;
    if (v.type === 'User' && v.name && (v.displayName || v.readable === false)) out.add(v.name);
    Object.values(v).forEach(visit);
  };
  visit(result?.structuredContent);
  const sc = result?.structuredContent ?? {};
  for (const n of [sc.acting?.name, sc.subject?.name]) if (n && sc.acting?.fullName) out.add(n);
  for (const wi of sc.workItems ?? []) for (const k of ['assignee', 'object', 'requestor']) if (wi[k]) out.add(wi[k]);
  return [...out];
}

// --- named mutations ---

const mia = { oid: '10000000-0000-0000-0000-0000000000a2', type: 'User', name: 'mkovac', displayName: 'Mia Kovac' };

// onItem applies change to the first work item's context.
const onItem = (change) => (r) => change(items(r)[0].context, items(r)[0], r);

export const mutations = {
  // Decision clocks relative to now (the recorded deadline is fixed).
  deadlineIn: (ms) => onItem((c) => { c.deadline = new Date(Date.now() + ms).toISOString(); }),
  noDeadline: onItem((c) => { delete c.deadline; }),
  // midPoint didn't say how many steps there are.
  missingStageCount: onItem((c) => { delete c.stage.count; }),
  // The last step, with someone else still asked in it (all must agree):
  // under D37 the dialog still says this is the last step.
  lastStepOthersAsked: onItem((c) => { c.stage = { number: 1, count: 1, name: 'Team leads', strategy: 'allMustAgree' }; c.stageApprovers = [mia]; }),
  // More steps follow and nobody else is asked in this one.
  moreStepsAlone: onItem((c) => { c.stage = { number: 1, count: 3, name: 'Team leads', strategy: 'firstDecides' }; c.stageApprovers = []; c.coAssignees = []; }),
  // Someone else asked for the access.
  otherRequester: onItem((c) => { c.requester = { ...mia }; }),
  // The requester left the reason empty while the deployment has a field.
  noReason: onItem((c) => { delete c.justification; }),
  riskMedium: onItem((c) => { c.target.riskLevel = 'medium'; }),
  noRisk: onItem((c) => { delete c.target.riskLevel; }),
  reasonOwner: onItem((c) => { c.reason = 'roleOwner'; }),
  reasonAssignedStep: onItem((c) => { c.reason = 'assigned'; }),
  reasonAssignedNoName: onItem((c) => { c.reason = 'assigned'; delete c.stage.name; }),
  noValidity: onItem((c) => { delete c.validity; }),
  validityDays: (days, zone) => onItem((c) => { c.validity = { validTo: endOfDayIn(days, zone) }; }),
  noCurrentRoles: onItem((c) => { c.requesteeAccess = { visible: true, roles: [] }; }),
  // Free text long enough to clamp.
  longDescription: onItem((c) => {
    c.target.description = 'Full access to the production databases, including schema changes, user management, backups and restores, '.repeat(6).trim();
  }),
  longJustification: onItem((c) => {
    c.justification = 'I am on call for the database migration next week and need to run the cut-over scripts, check replication, and roll back if needed. '.repeat(5).trim();
  }),
  // Untrusted text that looks like markup (contract 4.1 rule 7, section 9).
  markupJustification: onItem((c) => {
    c.justification = '<b>urgent</b> <img src=x onerror="document.title=\'owned\'"> please';
  }),
  // A server whose contract MAJOR differs.
  versionMismatch: (r) => { r.structuredContent.server.uiContract = '2.0'; },
  // A result without structuredContent and without an error.
  textOnly: (r) => { delete r.structuredContent; },
  // "Open in midPoint" links on (contract 4.9, S15 not yet in the server).
  linksOn: (r) => {
    r.structuredContent.server.gui = {
      baseUrl: 'https://idm.example.test/midpoint',
      paths: { User: '/admin/user/{oid}', Role: '/admin/role/{oid}', Org: '/admin/org/{oid}', Service: '/admin/service/{oid}', Case: '/admin/case/{oid}', self: '/self/profile/user' },
    };
  },
  // A recorded outcome word midPoint might use that isn't approve or reject.
  rawOutcomeWord: (r) => { r.structuredContent.recordedOutcome = 'skip'; },
  // Intermediary slots (contract 5): the intermediary answered the call
  // itself, so there is no structuredContent from this server.
  held: ({ approver = 'Access governance team', source = 'Policy gateway', reason = 'Approving access needs a second person.', ref = 'a41c-77f0' } = {}) => (r) => {
    delete r.structuredContent;
    r.isError = false;
    r.content = [{ type: 'text', text: 'The call is waiting for approval by the policy gateway.' }];
    const slot = { v: 1, decision: 'held', audited: true, reason, ref, expiresAt: new Date(Date.now() + 2 * DAY).toISOString() };
    if (approver) slot.approver = { name: approver, kind: 'group' };
    if (source) slot.source = source;
    r._meta = { ...(r._meta ?? {}), 'intermediary/decision': slot };
  },
  denied: ({ source = 'Policy gateway', reason = 'Approvals from this assistant are turned off.' } = {}) => (r) => {
    delete r.structuredContent;
    r.isError = true;
    r.content = [{ type: 'text', text: 'The call was blocked by the policy gateway.' }];
    const slot = { v: 1, decision: 'denied', audited: true, reason, ref: 'b77d-0021' };
    if (source) slot.source = source;
    r._meta = { ...(r._meta ?? {}), 'intermediary/decision': slot };
  },
  allowed: (r) => {
    r._meta = { ...(r._meta ?? {}), 'intermediary/decision': { v: 1, decision: 'allowed', audited: true, source: 'Policy gateway', actor: { name: 'Team assistant', kind: 'assistant' } } };
  },
  // A slot of an unknown version must be ignored as a whole.
  slotUnknownVersion: (r) => {
    r._meta = { ...(r._meta ?? {}), 'intermediary/decision': { v: 2, decision: 'denied', audited: true, source: 'Policy gateway' } };
  },
  // Error text with an address in it (redacted in the details, 6.8).
  errorWithAddress: (r) => {
    r.content = [{ type: 'text', text: `${r.content[0].text} (see https://idm.example.test/midpoint/admin/case/x for more)` }];
  },
  // An error from an older server: no stable code, classified by its text.
  errorWithoutCode: (r) => { delete r._meta; },
};

// sortFixture is an inbox whose items, listed in scrambled order, have a known
// sorted order: deadlines first, soonest first; then oldest request first,
// falling back to when the item arrived (7.1). Each item names its role so
// the order can be read from the titles.
export function sortFixture() {
  return derive('inbox.approver', 'sort-order', (r) => {
    const base = items(r)[0];
    const iso = (ms) => new Date(Date.now() + ms).toISOString();
    const make = (i, label, times) => {
      const wi = structuredClone(base);
      wi.id = String(100 + i);
      wi.context.target.displayName = label;
      wi.context.target.name = label.toLowerCase().replace(/\W/g, '-');
      for (const k of ['deadline', 'requestedAt', 'createdAt']) delete wi.context[k];
      for (const [k, ms] of Object.entries(times)) wi.context[k] = iso(ms);
      return wi;
    };
    const list = [
      make(1, 'Sort third', { requestedAt: -10 * DAY, createdAt: -10 * DAY }),
      make(2, 'Sort second', { deadline: 3 * DAY, requestedAt: -1 * DAY }),
      make(3, 'Sort fifth', { requestedAt: -1 * DAY, createdAt: -1 * DAY }),
      make(4, 'Sort first', { deadline: 2 * HOUR, requestedAt: -HOUR }),
      make(5, 'Sort fourth', { createdAt: -5 * DAY }), // no requestedAt: falls back to createdAt
    ];
    r.structuredContent.workItems = list;
    r.structuredContent.count = list.length;
  });
}

export const sortedTitles = ['Sort first', 'Sort second', 'Sort third', 'Sort fourth', 'Sort fifth'];
export { HOUR, DAY };
