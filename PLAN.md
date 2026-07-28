# midPoint MCP server — PLAN

Go MCP server for Evolveum midPoint. Public repo — keep README/docs
product-neutral (midPoint + MCP only; no downstream deployment stories).

## Stack

- Go, `github.com/modelcontextprotocol/go-sdk` (official SDK)
- midPoint REST API (`/ws/rest/...`), midPoint 4.8+ / tested against 4.10
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
  approve_work_item / reject_work_item (behind the write gate), get_case.
  Exact case/work-item REST endpoints verified against midPoint 4.10 during
  implementation. AC against a live midPoint: request → case opens attributed
  to the correct requester → approve via work item → assignment appears.
  **Extended (Unreleased):** `list_requestable_roles` closes the loop's front
  door — searches `requestable = true` roles as the calling user (so midPoint's
  read authorization filters to the requestable-and-visible set). Exact `#assign`
  eligibility (`getAssignableRoleSpecification`) was rejected: it needs the
  script path, unavailable under OIDC impersonation (same limit as `search_audit`).
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
  - Approvals already exist (`list_work_items`, `approve_work_item`,
    `reject_work_item`) — the manager's inbox is the same tools.

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
