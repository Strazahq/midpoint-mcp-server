# midPoint MCP server — PLAN

Go MCP server for Evolveum midPoint. Public repo — keep README/docs
product-neutral (midPoint + MCP only; no downstream deployment stories).

## Stack

- Go, `github.com/modelcontextprotocol/go-sdk` (official SDK)
- midPoint REST API (`/ws/rest/...`), midPoint 4.10 (every live check on 4.10.3; 4.8,
  4.9 and 4.11 are untested and may not work, see README "midPoint versions")
- Auth: HTTP Basic (midPoint's native REST auth) via env
  `MIDPOINT_URL`, `MIDPOINT_USERNAME`, `MIDPOINT_PASSWORD`
- Transports: stdio (default), streamable HTTP via `--http :3001` (endpoint `/mcp`)
- Writes gated by `MIDPOINT_MCP_ALLOW_WRITES=true`; every write tool returns a
  dry-run preview unless the gate is on

## Milestones (one per session)

- **M0 — scaffold**: Go module, main with SDK stdio server + one `ping` tool
  hitting `/ws/rest/self`; README stays honest about status. AC: connects from
  an MCP client, `ping` returns the authenticated identity.
- **M1 — read tools**: search_users (name/email/oid), get_user, list_roles,
  get_role, list_resources, get_resource, get_user_assignments. Table-driven
  tests against recorded REST fixtures; integration test against a midPoint
  4.10 docker container (skip when docker absent). AC: an assistant can answer
  "who is X and what do they have?" end to end.
- **M2 — write tools + gate**: create_user, enable/disable_user,
  assign/unassign_role, recompute_user; `MIDPOINT_MCP_ALLOW_WRITES` gate with
  dry-run previews when off. AC: disable→enable round-trip visible in midPoint
  GUI; writes refused (with preview) when the gate is off.
- **M3 — requests & approvals (self-service)**: request_role (assignment-add
  delta → midPoint approval policy turns it into a Case instead of executing),
  list_my_requests, list_work_items (the caller's approval inbox),
  decide_work_item (approve or reject, behind the write gate), get_case.
  Exact case/work-item REST endpoints verified against midPoint 4.10 during
  implementation. AC against a live midPoint: request → case opens attributed
  to the correct requester → approve via work item → assignment appears.
  **Extended (Unreleased):** `list_requestable_roles` closes the loop's front
  door — searches `requestable = true` roles as the calling user (so midPoint's
  read authorization filters to the requestable-and-visible set). Exact `#assign`
  eligibility (`getAssignableRoleSpecification`) was rejected: it needs the
  script path, unavailable under OIDC impersonation (same limit as `search_audit`).
  **Changed (Unreleased):** `decide_work_item` (`decision` = approve | reject,
  optional comment) replaces the original separate approve and reject tools,
  which completed any work item they were pointed at. It refuses, before
  any write and with the gate closed too, a work item the caller's inbox would
  not list (same rule as `list_work_items`: open case, no `closeTimestamp`,
  caller among the multi-valued `assigneeRef`), runs as the caller, and reads
  the case back to report the outcome midPoint recorded. Needed because
  midPoint answers completion of an already-closed item with 204 plus a warning.
- **M4 — HTTP transport + packaging** (scoping decided 2026-07-15: transport
  and packaging ONLY — OIDC identity is deliberately NOT in this milestone,
  it is M4.5): `--http` streamable HTTP mode, Dockerfile (scratch, static),
  GitHub release with binaries, MCP client config snippets in README (Claude
  Desktop, VS Code). **Safety rails are part of the AC**: `--http` binds
  `127.0.0.1` by default; binding any non-loopback address REFUSES to start
  until resource-server auth exists (M4.5) — no flag to bypass. In M4, HTTP
  mode is therefore still personal mode (local client, the configured
  credentials' identity), just over a different transport. A release must
  never contain an unauthenticated network surface **that is reachable by
  default**. Amended in M6.5: the one permitted exception is anonymous
  *discovery* — the MCP handshake and tool listing, never a tool call — and it
  is opt-in, off unless the operator sets it. The invariant that survives is
  that no midPoint data is ever reachable without a validated token.
- **M4.5 — OIDC resource-server identity** (its own milestone on purpose:
  token validation is security-critical and gets test-first discipline):
  validate `Authorization: Bearer` against the configured issuer's JWKS
  (`MIDPOINT_MCP_OIDC_ISSUER`, `MIDPOINT_MCP_OIDC_AUDIENCE`), map
  `sub`→`externalId` (fallback `preferred_username`→`name`), execute per
  request as the mapped user via `Switch-To-Principal` (service account holds
  the archetype-filtered `#proxy` authorization — see Identity model below).
  Non-loopback binding unlocks only when this is configured. AC against a
  real Keycloak + midPoint: two different users' tokens → midPoint audit
  attributes each call to the right human; unmapped/expired/wrong-audience
  tokens refused; the M3 request/approval flows attribute correctly end to
  end over HTTP.

  **Verified live 2026-07-16** (real Keycloak + midPoint 4.10.3, via the
  build-tagged `TestLiveOIDCResourceServer`): no token → refused; a valid
  token for a user present in the IdP but not midPoint → correlation fails →
  refused; a mapped user's token → `ping` runs as that user via
  `Switch-To-Principal` (IdP `preferred_username` → midPoint identity, not the
  service account). Two environment prerequisites confirmed necessary: the IdP
  must emit the expected `aud` (Keycloak needs an audience mapper — the audience
  check is not relaxed to `azp`), and the REST service account needs the
  `authorization-rest-3#proxy` action (the model `#all` of superuser does NOT
  include it) scoped to the users it may impersonate.
- **M5 — audit & reporting (read-only, query-driven)**: deliberately skip
  midPoint's native report engine — its CSV/HTML output lands on the server
  filesystem (a `reportData` `filePath`, not a downloadable stream), so it's
  unreachable in shared HTTP mode. Instead build our own query/aggregation
  layer over the REST search API:
  - `search_audit` — audit-trail queries (time range, initiator, target, event
    type, outcome, channel). Audit records are container values with no parent
    object, so the exact REST search shape is verified against midPoint 4.10
    during implementation (same discipline as M3's case endpoints).
  - `search_objects` — filtered searches across users / roles / orgs /
    assignments / shadows (midPoint query language) so the assistant can
    compose ad-hoc reports: orphaned accounts, unused roles, assignments
    expiring soon, disabled users still holding access, SoD conflicts.
  - Optional local read-model: cache/index REST results to power heavier
    aggregation and point-in-time snapshots. **Open design decision for M5:**
    in-memory/ephemeral vs a persistent store. Persisting identity and audit
    data at rest is a real security surface (public repo, IGA data) and adds a
    sync/staleness burden — default to ephemeral, and only introduce
    persistence if a concrete report genuinely requires it, documented in
    CHANGELOG when it does.
  - All read-only, so it stays outside the `MIDPOINT_MCP_ALLOW_WRITES` gate.
  AC against a live midPoint: assistant answers "every change to role X in the
  last 30 days" and "orphaned accounts on resource Y" end to end.

  **Implemented 2026-07-15 (verification result):** midPoint 4.10 exposes **no
  REST audit endpoint** (confirmed against the full endpoints table), and the
  bulk `search` action is objects-only. So:
  - `search_objects` — delivered as designed over users/roles/orgs/services/
    shadows/resources (covers "orphaned accounts on resource Y"). Assignments
    are reached via focus filters, not a separate container search.
  - `search_audit` — delivered via the `executeScript` RPC and **verified live
    against 4.10.3** (initially 500ed; fixed in Unreleased). The working recipe:
    a typed `<search>` seed (the generic dynamic `search`/`execute` actions are
    rejected in 4.10) feeds one input item to an `execute-script` action whose
    Groovy reaches `ModelAuditService` (via `modelInteractionService`, reflected —
    no audit accessor is exposed on the scripting binding, and
    `RepositoryService.searchContainers` rejects `AuditEventRecordType`) and
    **returns** each record as a tab-delimited data-output item (`log.info` does
    not reach the response). It still needs script-execution authorization and so
    does **not** work under resource-server (#proxy) impersonation. Plumbing +
    parsing are unit-tested; a live integration test asserts records parse.
  - No local read-model built — ephemeral, per the design decision above.
- **M6 — manager & team self-service**: the self-service loop (M3) plus the
  manager dimension — act for the people you manage, not just yourself. midPoint
  models management through org structure: a manager is a user assigned to an
  OrgType with the `manager` relation; their reports are that org's members
  (`getManagers`/`getMembers`/`isManagerOf` confirmed on MidpointFunctions, but
  those are script-path — the tools use REST `parentOrgRef matches (oid = … and
  relation = …)` queries so they run under resource-server impersonation and are
  scoped by the manager's own authorizations). Everything executes AS the caller;
  we never build a parallel permission model, midPoint enforces who may see/act
  for whom.
  - `list_my_team` — the caller's direct reports: members (default relation) of
    the orgs the caller manages (`parentOrgRef` with the `manager` relation).
    Empty for a non-manager. Read-only.
  - `list_my_managers` — who the caller reports to: the managers of the orgs the
    caller is a member of. Read-only.
  - Request a role **for a report** (done): `request_role` already accepts a
    target `userOid`; `list_requestable_roles` gained an optional `forUser` that
    returns the requestable roles that report does not already hold (reads the
    target's `roleMembershipRef`, as the caller). Respects the write gate;
    midPoint's approval policy still applies.
  - View a report's access: the existing `get_user_assignments` (by OID from
    `list_my_team`) — documented as the manager flow, not new code.
  - Approvals already exist (`list_work_items`, `decide_work_item`) — the
    manager's inbox is the same tools.

  **Verified live 2026-07-16** with a manager→report fixture: `list_my_team`
  returns real reports and `list_requestable_roles?forUser=` excludes roles the
  report already holds. **Deployment requirement discovered:** a non-superuser
  manager only sees reports if granted **read authorization over them** — the
  `manager` org relation alone is not enough (an org manager's search returned
  only themselves). midPoint's standard fix is an authorization whose object
  selector uses `orgRelation` with `subjectRelation = manager`; provisioning that
  is the deployment's IAM decision, so the tools stay agnostic and simply run as
  the caller. Relation-scoped `parentOrgRef` query shapes confirmed valid on 4.10.
  AC against a live midPoint: a manager lists their reports, views a report's
  access, requests a role for that report, and the request routes to the correct
  approver.

  **Extended 2026-07-27, from live testing in a gateway-brokered deployment**
  (the server spawned over stdio with a *technical* account while the gateway
  knew the end user). Findings, all fixed:
  - **A self-scoped tool must never claim an identity the server does not have.**
    Personal mode silently means "the configured credentials", which is the human
    only when those credentials are theirs. `list_my_team` / `list_my_managers` /
    `list_work_items` / `list_my_requests` said "you" and reported a service
    account's empty inbox to a user who had a pending work item. They now name
    their subject and carry it structurally; `whoami` reports the acting identity,
    the mode, and the org links; `identity.credentialIsShared` makes them refuse
    rather than answer for a technical account. The real fix remains
    resource-server mode.
  - **An empty answer must say which empty it is.** Team results carry the org
    links they were derived from, separating "no org links" from "org links whose
    members this identity may not see" (the authorization gap above).
  - **A tool named `request_role` must not grant.** midPoint routes an
    assignment-add through approval only where policy matches and executes it
    immediately otherwise — the same delta `assign_role` sends. It now refuses
    roles not flagged `requestable` (`requests.requireRequestable`, default on).
  - **`list_my_teammates`** (peers) completes the trio, and the org modelling all
    three depend on became configurable via `MIDPOINT_MCP_CONFIG`
    (`team.orgSource` incl. an assignment fallback, relation local parts, and an
    org selector). Verified live end to end against midPoint 4.10.3.
  - Noted for M4.5/v1.5: `GET /self` **ignores** `options=resolveNames` on 4.10.3,
    so org refs come back unnamed; the by-OID read honours it.
- **M6.5 — anonymous discovery (opt-in)**: in resource-server mode,
  `MIDPOINT_MCP_ANONYMOUS_DISCOVERY=true` serves `initialize`,
  `notifications/initialized`, `ping` (the protocol method, not the tool) and
  `tools/list` without a bearer token; every `tools/call` still requires one.
  It exists because gateways and catalog builders inventory a server's tool
  surface *before* they hold a user token, and a transport-layer 401 makes that
  impossible — the tool surface is static metadata, identical for every caller,
  and none of the four methods reaches the REST client.

  Classification happens at the HTTP layer rather than in an
  `mcp.MethodHandler`, because the SDK's token-info context key is unexported:
  `RequireBearerToken` is the only way to get a verified `TokenInfo` onto a
  request, so the require-or-not decision has to precede SDK dispatch. A
  request carrying *any* `Authorization` header is always verified, whatever it
  asks for — that is what binds the MCP session to a user ID. A JSON-RPC batch
  is discovery-only when every member is; one `tools/call` among them makes the
  whole request privileged. Unparseable and oversized bodies fail closed.

  AC: tokenless `initialize` + `tools/list` succeed and touch midPoint zero
  times; tokenless `tools/call` refused; an anonymously-initialized session
  accepts a later authenticated `tools/call` and impersonates the token's user
  (the SDK skips its session-hijack check when the session's user ID is empty —
  pinned by `TestAnonymousDiscoveryThenAuthenticatedCall`); default-off proven.
- **M10 — MCP Apps views, approval inbox first**: the four interactive views of
  [`docs/ui-contract.md`](docs/ui-contract.md) (`1.0-draft.9`), built in the
  contract's order (inbox, Get access, My requests, My team's access). The
  contract is the spec; its section 8 numbers the server changes (S1 to S25)
  and the slices below cite them. Each slice is one session.
  - **M10.1 — views plumbing (no view yet)**: UI-session detection from the
    client's `io.modelcontextprotocol/ui` extension; per-session `tools/list`
    (`_meta.ui.resourceUri` for tools whose view is embedded,
    `visibility: ["model"]` for tools no view calls, nothing in other
    sessions); the embedded view directory with its size and network-construct
    test; `ui://midpoint/*` resources listed only in UI sessions, readable
    always (S1); anonymous discovery covering them (S17); `tool`, `acting` and
    `server` on the results of the tools views render or call (S2); no URLs in
    tool errors (S14); `initialize` instructions for readable chat (S24). AC:
    unit tests in both modes, with and without the UI capability; a non-UI
    session's `tools/list` stays byte-identical.
  - **M10.2 — inbox data**: `workItems[].context` with justification,
    validity, reason, co-assignees, stage approvers and the requestee's access
    (S4, S23, S25 for work items and cases); `nextApprovers` (S8); `get_case`
    enrichment (S5); complete, marked text for `list_work_items` and
    `get_case` (S13, S19); stable error codes (S18). AC: the [live] and
    [verify] items for these fields fired on a midPoint 4.10 instance.
  - **M10.3 — the inbox view**: `ui://midpoint/approval-inbox`, one
    self-contained document built from the reviewed mockup, linked from
    `list_work_items` and `decide_work_item`. AC: an approver opens the inbox in
    a host that supports MCP Apps, approves and rejects, and the view follows
    contract 7.1.
  - **Later slices, built together (2026-10-01)**: Get access (S6, S7,
    S20, S21, S22), My requests (`cancel_request`, S16; S5) and My team's
    access (S9, S10, S25), each with its own browser suite in
    `test/views/`. Open: firing each on a live midPoint 4.10 and in a real
    MCP Apps host; S15 (Open in midPoint links).
  - **M10.4 — readable confirmations (2026-10-03, contract D38, S26)**: a
    host's "allow this tool" card shows the call's arguments, which were OIDs
    only. Every write tool also takes the midPoint `name` of the person and
    the role (`userName`, `roleName`; the unique `name` attribute, owner's
    choice, not display names), and the server refuses a name that doesn't
    match its OID before any write. The four views send the names they hold.
    AC: a write with a missing or wrong name is refused with `invalid-input`
    and nothing is written, gate open or closed; the views' calls carry the
    names; `go test ./...` and the four browser suites pass.
  - **M10.5 — readable views (2026-10-03, contract draft.11, D39, S27)**:
    the four views take the reviewed redesign: layout A inline, B (ledger) in
    full screen, colour for status only, initials avatars and one-tint kind
    tiles, status pills, real buttons, labelled facts, and each role's origin
    and "ends soon" in My team's access. Two independent reviewers validated
    the mockup first (a line manager on a phone, and UX and accessibility);
    their fixes are in. AC: the four browser suites pass with the new
    strings, palette and layout, including contrast in both themes and no
    overflow at 320 px; `go test ./...` passes. Next: `list_recent_errors`
    as a text tool only; no new views.
- **M11 — self-service and operator tools (2026-10-03, owner: "go implement
  rec")**: text tools (no new views) plus the inbox's Claim.
  - **For everyone:** `get_my_access` and `list_expiring_access`.
  - **Group-offered approvals** (D40, Q4 settled): `claim_work_item` and
    `release_work_item`.
  - **For operators:** `list_recent_errors`, `list_tasks` and `get_task`,
    `run_task`, `suspend_task` and `resume_task`, and `test_resource`.
  - Every write takes D38 names.

  Each was fired against midPoint 4.10.3 (throwaway objects deleted). AC:
  `go test ./...` and the four view suites pass; the integration-tagged live
  tests pass against a 4.10 instance.
- **M12 — midPoint is the judge of a request (2026-10-04, contract
  draft.12+, D42)**. The owner challenged the `requests.formItems` settings
  list: midPoint can make the fields differ per person and per role, and policy
  rules can demand anything. So the server must not re-implement those rules.
  What was fired on 4.10.3: policy rules and `assign` field restrictions are
  enforced on a REST request (409 or 403, before any approval case). midPoint's
  REST API cannot say beforehand which fields a person may fill, and cannot
  carry the request comment.
  1. **Refusal reasons** (S29, done): midPoint's `userFriendlyMessage`
     reaches the view and the assistant. 409 is coded `refused`.
  2. **Approvers see what was asked** (S30, done): every filled assignment
     field, labelled, and the comment typed in midPoint's own page (the case's
     creation event, which the stock Approver role can't read, live).
  3. **No field settings** (D43, S30, done): `requests.formItems` and
     `requests.justificationItem` are ignored with a warning. The dialog
     offers every assignment field of midPoint's schema (choices, lookup
     tables, lists, numbers), required as the schema says. Not done: the
     requester's archetype template as a source of "required" marks. It is
     only a GUI marker, and so far it has been read only in source.
  4. **Docs** (done): README "Request fields" and docs/authorization.md list
     the REST limits and the startup reads, and the sample role carries them
     (live).

  AC: each step fired on eval where midPoint behaviour is involved;
  `go test ./...` and the four view suites pass; CHANGELOG per step.
- **M7 (sketch) — delegation & deputy**: hand your work items / access to a
  deputy while away (midPoint's `deputy` relation); list/create/revoke
  delegations. Needs live shape verification.
- **M8 (sketch) — access review / certification**: managers attest to reports'
  access (the built-in `Reviewer` role + certification campaigns).
- **M9 (sketch) — governance reports**: SoD conflicts, orphaned accounts, stale
  access, over-privileged users — composed over `search_objects`.

## Identity model (who is the caller?)

Requests and approvals are only meaningful if midPoint sees the real human.
Two supported modes, decided by transport:

- **Personal mode (stdio, default)**: the server runs locally with the USER's
  own midPoint credentials — midPoint natively sees them, approval cases are
  attributed correctly, no delegation machinery exists to abuse.
- **Resource-server mode (HTTP, shared)**: per the MCP Authorization spec the
  client presents an OAuth bearer token; the server validates it against the
  configured OIDC issuer's JWKS (`MIDPOINT_MCP_OIDC_ISSUER`,
  `MIDPOINT_MCP_OIDC_AUDIENCE`), extracts `sub`/`preferred_username`, maps it
  to the midPoint user (correlate `sub` == `externalId`, fall back
  `preferred_username` == `name`), and calls midPoint as the service account
  with the **`Switch-To-Principal: <oid>`** header. The service account holds
  the REST **`#proxy`** authorization, filtered (e.g. to the `Person`
  archetype) so it can never impersonate administrators
  (docs.evolveum.com → REST → authentication → impersonation).
- **Never**: an `on_behalf_of` tool parameter. Identity comes from the
  transport's authentication or the local credential — never from tool
  arguments a caller can fabricate.

## Rules

- Definition of done: AC + tests green (`go test ./...`) + CHANGELOG.md line.
- No AI attribution trailers in commits.
- Credentials never in code, logs, tool output, or fixtures.
- Prefer stdlib beyond the MCP SDK; justify every dependency in CHANGELOG.
