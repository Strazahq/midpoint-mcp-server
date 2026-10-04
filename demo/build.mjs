#!/usr/bin/env node
// Builds demo/index.html: one self-contained page that plays the four MCP
// Apps views (views/*.html, bundled byte for byte) inside a simulated chat
// host, answering every call from the recorded test fixtures in
// test/views/fixtures. Node 18 or later, no dependencies. Run from anywhere:
//
//   node demo/build.mjs
//
// The page's own markup, style and host code live in demo/src; the
// scenarios (which fixture answers which call) are below.

import { readFileSync, writeFileSync } from 'node:fs';

const repo = new URL('../', import.meta.url);
const read = (path) => readFileSync(new URL(path, repo), 'utf8');

// --- ids used in the recordings ---
const CASE_DB_ADMIN = '40000000-0000-0000-0000-000000000001'; // Bob Stone asks for Database admin
const CASE_FINANCE = '40000000-0000-0000-0000-000000000002'; // Bob Stone asks for Finance reports (managers' step)
const CASE_OFFERED = '40000000-0000-0000-0000-000000000004'; // Finance reports, offered to Access approvers
const BOB = '10000000-0000-0000-0000-0000000000b1';
const MIA = '10000000-0000-0000-0000-0000000000a2';
const JANE = '10000000-0000-0000-0000-0000000000c1';
const DB_ADMIN = '20000000-0000-0000-0000-0000000000f1';

// Tools that change midPoint. A call to one of these makes the host ask
// first; every other call is read-only and answered straight away.
const WRITE_TOOLS = ['decide_work_item', 'claim_work_item', 'release_work_item', 'request_role', 'cancel_request', 'unassign_role'];

// title: the heading each view shows, which the permission prompt names.
const VIEWS = {
  'approval-inbox': { file: 'views/approval-inbox.html', uri: 'ui://midpoint/approval-inbox', title: 'Requests to approve' },
  'request-access': { file: 'views/request-access.html', uri: 'ui://midpoint/request-access', title: 'Get access' },
  'my-requests': { file: 'views/my-requests.html', uri: 'ui://midpoint/my-requests', title: 'My requests' },
  'access-review': { file: 'views/access-review.html', uri: 'ui://midpoint/access-review', title: "My team's access" },
};

// A scenario is a short chat: the user's message, the assistant's line, the
// tool call that opened the view (entry), and the answers the host gives to
// the calls the view makes. `try` and `recorded` are the stage directions
// above the chat (`backticks` mark tool names); `next` is the scenario a
// message from the view (ui/message) leads on to. An answer is
// { fx, when, state, set }:
//   fx     the fixture whose result is the answer
//   when   arguments that must match (a subset, compared as JSON)
//   state  only when the scenario is in this state ('' at the start)
//   set    the state after this answer (writes only)
// The first matching answer wins.
const SCENARIOS = [
  {
    id: 'approve',
    title: 'Approve or reject a request',
    persona: 'Dana Lee, an approver of the Database admin role',
    view: 'approval-inbox',
    user: "What's waiting for my approval?",
    assistant: 'One request is waiting for you: Bob Stone asks for Database admin. You can decide on it here.',
    entry: 'inbox.approver',
    try: 'Press Approve, or Reject with a reason. The view asks to run `decide_work_item`, and the host asks you first. Deny once to see what the view shows when the host refuses.',
    recorded: 'approving or rejecting Bob Stone’s Database admin request',
    answers: {
      whoami: [{ fx: 'whoami.approver' }],
      list_work_items: [{ fx: 'inbox.approver' }],
      get_case: [{ fx: 'case.approver' }],
      decide_work_item: [
        { when: { caseOid: CASE_DB_ADMIN, workItemId: '6', decision: 'approve' }, fx: 'decide.approve-next' },
        { when: { caseOid: CASE_DB_ADMIN, workItemId: '6', decision: 'reject' }, fx: 'decide.reject-open' },
      ],
    },
  },
  {
    id: 'manager',
    title: "A manager's inbox",
    persona: 'Jane Doe, manager of dev-ops and a delegate of another approver',
    view: 'approval-inbox',
    user: 'Anything I need to sign off today?',
    assistant: "Two requests wait for you: one delegated to you, and one as Bob Stone's manager.",
    entry: 'inbox.manager',
    try: 'Open Details on either request (the view reads `get_case`), then approve Finance reports.',
    recorded: 'approving the Finance reports request (the other decisions were not recorded for Jane Doe)',
    answers: {
      whoami: [{ fx: 'access-review.whoami' }],
      list_work_items: [{ fx: 'inbox.manager' }],
      get_case: [
        { when: { oid: CASE_DB_ADMIN }, fx: 'case.manager-delegated' },
        { when: { oid: CASE_FINANCE }, fx: 'case.manager-step' },
      ],
      decide_work_item: [{ when: { caseOid: CASE_FINANCE, workItemId: '5', decision: 'approve' }, fx: 'decide.approve-open' }],
    },
  },
  {
    id: 'group',
    title: 'Claim a request offered to a group',
    persona: 'Dana Lee, a member of the Access approvers group',
    view: 'approval-inbox',
    user: 'Is anything waiting for the access approvers?',
    assistant: 'One request is offered to Access approvers. Claim it to make it yours; then you can decide on it or give it back.',
    entry: 'inbox.offered',
    try: 'Press Claim: the host asks before `claim_work_item` runs, then the view reads the list again (`list_work_items`) and the card shows the request as yours. Release gives it back to the group.',
    recorded: 'claiming and releasing the Finance reports request',
    answers: {
      whoami: [{ fx: 'whoami.approver' }],
      list_work_items: [
        { state: 'claimed', fx: 'inbox.claimed' },
        { fx: 'inbox.offered' },
      ],
      get_case: [{ fx: 'case.offered' }],
      claim_work_item: [{ when: { caseOid: CASE_OFFERED, workItemId: '5' }, fx: 'claim.claimed', set: 'claimed' }],
      release_work_item: [{ when: { caseOid: CASE_OFFERED, workItemId: '5' }, fx: 'release.released', set: '' }],
    },
  },
  {
    id: 'request',
    title: 'Ask for a role',
    persona: 'Bob Stone, a member of dev-ops',
    view: 'request-access',
    user: 'I need admin access to the production databases.',
    assistant: 'Here are the roles you can request. Database administrator fits; request it here and it goes to approval.',
    entry: 'request-access.catalog',
    try: 'Press Request on Database administrator, then Send request. The host asks before `request_role` runs. Afterwards, Track this request hands a message back to the chat.',
    recorded: 'requesting Database administrator',
    next: 'withdraw',
    answers: {
      whoami: [{ fx: 'request-access.identity' }],
      list_my_managers: [{ fx: 'request-access.managers' }],
      list_my_team: [{ fx: 'request-access.team' }],
      list_requestable_roles: [{ fx: 'request-access.catalog' }],
      get_case: [{ fx: 'request-access.case' }],
      request_role: [{ when: { roleOid: DB_ADMIN }, fx: 'request-access.pending', set: 'requested' }],
    },
  },
  {
    id: 'withdraw',
    title: 'Check on and withdraw a request',
    persona: 'Bob Stone, who asked for Database admin',
    view: 'my-requests',
    user: 'What happened to my request for Database admin?',
    assistant: "It's still waiting for approval. Details shows who has decided so far.",
    entry: 'my-requests.list',
    try: 'Open Details (the view reads the case with `get_case`), then Withdraw. The host asks before `cancel_request` runs.',
    recorded: 'withdrawing the Database admin request',
    answers: {
      whoami: [{ fx: 'my-requests.whoami' }],
      list_my_requests: [
        { state: 'withdrawn', fx: 'my-requests.closed' },
        { fx: 'my-requests.list' },
      ],
      get_case: [{ when: { oid: CASE_DB_ADMIN }, fx: 'my-requests.case' }],
      cancel_request: [{ when: { caseOid: CASE_DB_ADMIN }, fx: 'my-requests.withdrawn', set: 'withdrawn' }],
    },
  },
  {
    id: 'review',
    title: "Review a team member's access",
    persona: 'Jane Doe, manager of Bob Stone and Mia Kovac',
    view: 'access-review',
    user: 'Show me what my team can access.',
    assistant: 'You manage two people in dev-ops. Pick one to see their access.',
    entry: 'access-review.team',
    try: 'The view opens Bob Stone first (it reads `get_user_assignments`). Press Remove on Database admin: the host asks before `unassign_role` runs. Pick Mia Kovac to read her access.',
    recorded: 'removing Database admin from Bob Stone',
    answers: {
      whoami: [{ fx: 'access-review.whoami' }],
      list_my_team: [{ fx: 'access-review.team' }],
      get_user_assignments: [
        { when: { oid: BOB }, state: 'removed', fx: 'access-review.after' },
        { when: { oid: BOB }, fx: 'access-review.person' },
        { when: { oid: MIA }, fx: 'access-review.second-person' },
        { when: { oid: JANE }, fx: 'access-review.self' },
      ],
      unassign_role: [{ when: { userOid: BOB, roleOid: DB_ADMIN }, fx: 'access-review.removed', set: 'removed' }],
    },
  },
];

// --- load what the scenarios use ---

const fixtures = {};
const need = (name) => {
  if (fixtures[name]) return;
  let fx;
  try {
    fx = JSON.parse(read(`test/views/fixtures/${name}.json`));
  } catch (e) {
    throw new Error(`fixture ${name}: ${e.message}`);
  }
  if (!fx.call || !fx.result) throw new Error(`fixture ${name}: no call or result`);
  fixtures[name] = { call: fx.call, about: fx.about ?? '', result: fx.result };
};
for (const s of SCENARIOS) {
  if (!VIEWS[s.view]) throw new Error(`scenario ${s.id}: unknown view ${s.view}`);
  need(s.entry);
  for (const [tool, answers] of Object.entries(s.answers)) {
    for (const a of answers) {
      need(a.fx);
      const got = fixtures[a.fx].call.name;
      if (got !== tool) throw new Error(`scenario ${s.id}: ${a.fx} answers ${got}, not ${tool}`);
    }
  }
}

const views = {};
for (const [id, v] of Object.entries(VIEWS)) views[id] = { uri: v.uri, title: v.title, html: read(v.file) };

const data = { writeTools: WRITE_TOOLS, views, fixtures, scenarios: SCENARIOS };
// '<' as < keeps the JSON inert inside a <script> element.
const json = JSON.stringify(data).replace(/</g, '\\u003c');

const fill = (template, values) => {
  let out = template;
  for (const [k, v] of Object.entries(values)) {
    const marker = `/*@${k}@*/`;
    if (!out.includes(marker)) throw new Error(`shell has no ${marker}`);
    out = out.split(marker).join(v);
  }
  return out;
};

const page = fill(read('demo/src/shell.html'), {
  CSS: read('demo/src/host.css'),
  DATA: json,
  JS: read('demo/src/host.js'),
});

writeFileSync(new URL('demo/index.html', repo), page);
console.log(`demo/index.html: ${(Buffer.byteLength(page) / 1024).toFixed(0)} KB, ${SCENARIOS.length} scenarios, ${Object.keys(fixtures).length} fixtures`);
