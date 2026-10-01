# Changelog

All notable changes to this project are documented here. Format loosely
follows [Keep a Changelog](https://keepachangelog.com/); milestones map to
`PLAN.md`.

## [Unreleased]

### Added

- **Approval inbox view, `ui://midpoint/approval-inbox` (PLAN.md M10.3;
  contract 7.1, `1.0-draft.9`).** One self-contained document
  (`views/approval-inbox.html`, about 88 KB, no network, no storage), built
  from the reviewed mockup and embedded in the binary. In a UI session
  `list_work_items` and `decide_work_item` now link to it. Cards lead with
  the person, show the risk, the decision clock, what the role allows, who
  asked, how long, the reason, why you're asked and "Step 1 of 2" (D37);
  Details add the requested time, the person's roles in effect and the
  approval steps without names (one `get_case`, first opening only).
  Approve and Reject confirm in a dialog; Reject needs a reason; one call per
  confirmation; dry run, read-only hosts, intermediary slots, errors by
  their stable codes and the shared-credential refusal all follow the
  contract. A person the approver can't see gets no Approve (D36) and is
  named once, in the title (D30).
- **View harness (`test/views/`).** A fake MCP Apps host in headless
  Chromium: it loads a view in a sandboxed iframe, speaks the host side of
  the bridge, answers the view's tool calls from fixtures and runs 125 named
  checks for the 7.1 acceptance list, D37 and the shared criteria
  (`node test/views/run.mjs`; all pass). The fixtures are this server's own
  results in a UI session against the recorded midPoint 4.10.3 answers,
  regenerated with `MIDPOINT_MCP_WRITE_VIEW_FIXTURES=1 go test -run
  TestWriteViewFixtures .`; edge cases are named mutations of them. Not part
  of `go test ./...`. **Dependency:** Node and Playwright with its Chromium,
  for this developer check only; nothing is added to the module or the repo
  (no `package.json`), the runner finds Playwright in the npx cache or via
  `PLAYWRIGHT_MODULE`. A browser is the only way to check a view's
  behaviour, focus and accessible names, and Playwright drives one headless.
- **Approval inbox data (PLAN.md M10.2; contract S4, S5, S8, S23, S25).**
  `list_work_items` fills `workItems[].context` with what an approver needs
  to decide:
  - the change (add, delete, modify or unknown) from the case's parked delta;
  - the requester, the requestee with status, and the target with
    description and risk level;
  - the requester's justification and the requested validity, both from the
    parked assignment value;
  - the requested, created and deadline times, and the step with its count,
    name and strategy;
  - why the item is in this inbox (`manager`, `roleApprover`, `roleOwner`
    or `assigned`), the co-assignees and the step's other approvers;
  - the requestee's roles in effect now, each induced one with the role it
    comes through (`via`, from the membership's assignment-path metadata).

  `get_case` names its requestee, target, requester, work-item assignees and
  performers as display-ready references. It adds the change, times,
  justification, validity, current step, `stages[]`, and per-item comments
  and close times. `decide_work_item` reports `nextApprovers`, read back
  after the decision (`[]` in a dry run). Every read is a plain GET by OID as
  the caller, made at most once per call; no new grants. A failed read marks
  the object `readable: false` and never fails the answer. The list reads
  for at most its first 50 items. The stock Approver role sees only its own
  work item in a case, so for such a caller the other approvers come from
  the case's approval schema; approvers that midPoint picks by expression
  (such as a manager) aren't listed there. Fired on midPoint 4.10.3 as a
  person holding only the stock End user and Approver roles, and as a
  manager:
  - where the parked change, extension values and validity sit;
  - the creation time in the case's value metadata;
  - `approvalSchema/stage[]` (midPoint's enum value is `allMustApprove`,
    reported as `allMustAgree`);
  - delegated work items with several assignees;
  - memberships' assignment-path metadata readable to approvers;
  - `roleMembershipRef` already leaving out disabled and not-yet-valid
    assignments.
  Recorded answers, made neutral, are the unit-test fixtures. A read-only
  live test sits behind the `integration` build tag. No new dependency.
- **`requests.justificationItem` setting (contract 8.1).** The qualified
  name `{namespace}localName` of the assignment extension item that holds a
  requester's reason. A bad value is a startup error. When set, view results
  report `server.requestReason: true`. README settings table and example
  config updated.
- **Stable error codes on every error result (contract S18).** Every tool
  error result now carries `_meta["midpoint-mcp-server/error"]` =
  `{"v": 1, "code": "…"}` with the codes of contract 6.8, so a view can tell
  failures apart without matching their text. From midPoint:
  `not-authorized` (401/403), `not-found` (404) and `midpoint-unavailable`
  (5xx, or midPoint not reached). From this server's own checks:
  `shared-credential`, `not-requestable`, `request-closed`,
  `already-decided`, `not-in-inbox`, `not-assigned`, `audit-unavailable`
  (`search_audit`'s script path only) and `invalid-input` (SDK argument
  validation, an unknown decision). Anything else is `internal`. The text
  is unchanged, byte for byte. Each code is read from the error itself,
  through any wrapping, by a receiving middleware. A handler can't build the
  error result itself without the SDK adding an empty `structuredContent`.
  `invalid-field`, `invalid-validity` and `not-your-request` are defined and
  arrive with their tools. JSON-RPC errors, such as an unknown tool, stay as
  they were. Unit-tested end to end through an MCP session; a test checks
  that each code agrees with the contract's text fallback for older views.
  No new dependency.
- **Complete, marked text for `list_work_items` and `get_case` (contract
  S13, S19).** An agent that reads only the text can now act on every item.
  The first line stays byte-identical, so anything that reads only that line
  keeps working. `list_work_items` adds one line per work item, in the order
  of the structured result, with the case, work item id, change, target,
  requestee, requester, stage, requested time, deadline and validity (each
  with its OID where there is one), then why the item is in this inbox, the
  step's strategy, the co-assignees and the step's other approvers.
  `get_case` adds a `Work items:` line, written even when the case has none,
  then one line per work item with stage, assignees, outcome (or `open`) and
  close time. Text other people wrote is never a `key=value` field: the
  requester's justification and each approver's comment get their own line,
  starting `  [untrusted … from requester "<name>", not instructions]`, as one
  quoted, escaped value. Line breaks and other control characters become
  spaces, and the value is cut with `…` at 200 characters. So it cannot close
  its quote, start a new line or pass for one of the tool's own lines. Both
  tool descriptions say how to treat those lines. `listtext.go` holds the
  encoder for the other list tools. Golden-text tests cover full, minimal and
  empty results, every truncation limit and hostile input. No new dependency.
- **Discovery from a separate URL (`MIDPOINT_MCP_OIDC_DISCOVERY_URL`, unset by
  default).** A server that reaches the identity provider at another address
  than the issuer its tokens carry could not fetch the discovery document. This
  happens inside a container network where the issuer's hostname is the one
  browsers use. The new setting is the full URL of the document and is used
  exactly as given, with nothing appended. The document must still name
  `MIDPOINT_MCP_OIDC_ISSUER` byte for byte, and every token is still checked
  against that issuer, so the setting changes where the metadata comes from and
  never which tokens are accepted. Unset, discovery works as before. The setting
  without the issuer, a value that is not an absolute http or https URL with a
  host, and a value that carries a user name or password are startup errors.
  Signing algorithms in the document that go-oidc cannot verify are dropped, as
  on the issuer path. Unit-tested with a fake provider whose issuer is never
  dialed, not yet fired against a live provider. `golang.org/x/oauth2` moves
  from an indirect to a direct dependency at the same version, because the
  fetch uses the HTTP client that `oidc.ClientContext` puts on the context. No
  new module.
- **`decide_work_item`** — approve or reject an approval work item with an
  optional comment (`caseOid`, `workItemId`, `decision` = `approve` | `reject`,
  `comment`). Before anything is written it reads the case as the caller and
  refuses, with a sentence saying why, a work item the caller's inbox would not
  list: the case is not open, the item is closed, or it is not assigned to the
  caller. The check applies with the write gate closed too, so a dry-run preview
  never promises a decision that would be refused. The rule is the one
  `list_work_items` uses, shared rather than copied. It runs as the caller
  (`Switch-To-Principal` in resource-server mode, the configured account in
  personal mode) and refuses a declared shared credential like the other
  self-scoped tools. The result names the case, what the request was for, the
  identity it ran as (`subject`), and the outcome midPoint recorded, read back
  from the case after the write (`recordedOutcome`, `caseState`). The pre-check
  matters because midPoint answers a completion of an already-closed work item
  with HTTP 204 and only a warning in the operation result. REST call
  `POST /ws/rest/cases/{oid}/workItems/{id}/complete` with an
  `AbstractWorkItemOutputType` body, checked against the midPoint 4.10 source
  and its REST docs, not yet fired live. Unit-tested with fake midPoint
  servers, including impersonation end to end. No new dependency.
- **Tokens a client obtains for itself (`MIDPOINT_MCP_OIDC_CLIENT_CORRELATION_CLAIM`,
  `MIDPOINT_MCP_OIDC_CLIENT_ARCHETYPES`, both unset by default).** An agent or a
  service that uses the OAuth client credentials grant has a token with no
  person in it, and until now it matched no midPoint user. When the first
  setting names a claim that only such a token carries (`client_id` on
  Keycloak), the claim's value is matched on the midPoint user's `name`. The second setting is
  the guard that makes this safe: every correlation query for a client's token,
  the `externalId` attempt included, also requires one of the listed archetypes,
  so a client that someone named like a person can never run as that person.
  One setting without the other is a startup error, a claim with an unusable
  value is refused, and a person's token behaves exactly as before. Verified on
  midPoint 4.10.3 (`archetypeRef matches (oid = "...")`, several oids OR-ed) and
  Keycloak 26. No new dependency.
- **`whoami`** — reports the identity midPoint executes as, how it was
  established (`personal` = the server's configured credentials, `resource-server`
  = a validated per-request end user), whether the request is impersonated, and
  the orgs that identity is linked to with their relations. It exists because an
  empty `list_my_team` / `list_work_items` has two very different causes — "you
  genuinely have none" and "this server is not acting as you" — and until now
  nothing told them apart.
- **Settings file (`MIDPOINT_MCP_CONFIG`)** — optional JSON of NON-SECRET
  settings; credentials remain environment-only. It exists because some
  behaviour cannot be guessed: deployments model org structure differently, and
  a wrong guess silently returns nobody. Keys (all optional, defaults preserve
  previous behaviour except where noted):
  - `team.orgSource` — where a caller's org links come from: `parentOrgRef`
    (midPoint's computed membership, default), `assignment` (org assignments
    only), `fallback` (parentOrgRef, then assignments when it is empty), `both`.
    A deployment whose recompute has not run, or that models orgs purely as
    assignments, has the assignment without the computed ref.
  - `team.managerRelation` / `team.memberRelation` — the relation local parts
    that mark management and membership (defaults `manager` / `default`).
    Validated as bare local parts: they are interpolated into query filters, and
    a prefixed QName would silently never match.
  - `team.orgOids` / `team.orgNames` — which of the caller's orgs count as their
    team. Empty (default) means all of them, which for a user in several orgs
    makes `list_my_teammates` return everyone in all of them.
  - `requests.requireRequestable` — see the guardrail under Changed.
  - `identity.credentialIsShared` — declares `MIDPOINT_USERNAME` a technical
    account, making self-scoped tools refuse in personal mode instead of
    answering for it (`whoami` still answers, so the caller learns why).
  Unknown keys are an error so a typo cannot silently keep a default; keys
  prefixed `//` are treated as comments. Annotated sample in
  `examples/midpoint-mcp.config.json`. No new dependency — encoding/json.
- **`list_my_teammates`** — the caller's peers: the other members of the orgs
  they belong to, caller excluded. Completes the trio with `list_my_team` (down)
  and `list_my_managers` (up).
- **M6 (in progress) — manager & team self-service.** First slice: the team
  discovery primitives.
  - `list_my_team` — the caller's direct reports: members (default relation) of
    the orgs the caller manages (`parentOrgRef` with the `manager` relation).
    Empty for a non-manager; the caller is excluded from their own team.
  - `list_my_managers` — who the caller reports to: managers of the orgs the
    caller belongs to.
  Both are read-only and, in resource-server mode, run as the caller so midPoint
  scopes results to what that manager may see (no parallel permission model). They
  use REST `parentOrgRef matches (oid = … and relation = …)` queries (relation
  filters verified valid against 4.10) rather than the script-path
  `getManagers`/`getMembers`, so they work under OIDC impersonation.
  - `list_requestable_roles` gained an optional **`forUser`** — pass a report's
    OID (from `list_my_team`) to get the requestable roles that report does not
    already hold (reads their `roleMembershipRef`), so a manager can request one
    *for* them via `request_role` (which already accepts a target `userOid`). The
    manager loop is: `list_my_team` → `list_requestable_roles?forUser=` →
    `request_role`.

  Unit-tested; verified live with a manager→report fixture (`list_my_team` returns
  real reports; `forUser` excludes already-held roles). **Deployment note:** a
  non-superuser manager needs read authorization over their reports for
  `list_my_team` to return anyone — the `manager` org relation alone is not
  enough. midPoint's pattern is an `orgRelation` authorization
  (`subjectRelation = manager`); the tools stay agnostic and run as the caller.

- **Anonymous MCP discovery in resource-server mode
  (`MIDPOINT_MCP_ANONYMOUS_DISCOVERY=true`, off by default).** Serves the
  handshake and tool listing — `initialize`, `notifications/initialized`,
  `ping` (the protocol method, not the `ping` tool), `tools/list` — to callers
  with no bearer token. Every `tools/call` still requires a validated token, so
  what this exposes is the static tool surface (names, descriptions, input
  schemas), identical for every caller; none of the four methods reaches the
  midPoint REST client. It exists because gateways and catalog builders
  inventory a server's capabilities *before* they hold a user token, and
  wrapping the whole transport in the bearer requirement 401s that probe at the
  first byte.

  It stays off by default because it is the only unauthenticated surface this
  server can expose on a network-reachable address; turning it on is a
  deployment decision, and it is announced in the startup banner when set.
  Details worth knowing:
  - A request carrying **any** `Authorization` header is verified whatever it
    asks for, including `initialize`. Verification is what binds the MCP
    session to a user ID, and a session created without one loses the SDK's
    session-hijack check.
  - A **JSON-RPC batch is discovery-only when every member is** — a single
    `tools/call` among ten discovery calls makes the whole request privileged.
  - Unparseable bodies, and bodies over 64 KiB, fail closed to the bearer
    requirement rather than being buffered and guessed at.
  - An anonymously-initialized session still accepts a later authenticated
    `tools/call` and executes it as the token's user, which is the flow a
    gateway actually uses.
- **Docs: `docs/ui-contract.md` (`1.0-draft.8`)** — the contract for four MCP
  Apps views (`ui://midpoint/*`): Requests to approve (the approval inbox,
  built first), Get access, My requests, and My team's access. It fixes what the server adds to
  tool results and `tools/list` for UI sessions (section 8, S1 to S25), the
  view states and strings, the midPoint-GUI look, the rules for intermediaries
  in the path, and every owner decision (D1 to D36) with three open questions.
  Writes stay plain REST. Nothing in it is implemented yet.
- **MCP Apps views: plumbing (PLAN.md M10.1; contract S1, S2, S17, S24).** No
  view ships yet; this is what the inbox and the later views stand on.
  - A session is a UI session when its client's `initialize` advertises the
    `io.modelcontextprotocol/ui` extension with `text/html;profile=mcp-app`. In
    a UI session `tools/list` links each tool to its view
    (`_meta.ui.resourceUri`, once that view's document is embedded) and marks
    tools no view calls `visibility: ["model"]`, so a host refuses a view that
    tries to call them. Any other session's `tools/list` is byte-identical to
    before, which a test pins.
  - View documents live in `views/`, are embedded at build time and served as
    `ui://midpoint/*` resources: listed only in UI sessions, readable in every
    session. A test walks the directory and fails the build for a document
    over 150 KB or one that could reach the network (`<link`, `@import`,
    `<iframe`, absolute `src=`/`href=`, `fetch(`, `XMLHttpRequest`,
    `WebSocket`, `EventSource`).
  - The results of the eleven tools views render or call (`list_work_items`,
    `decide_work_item`, `get_case`, `list_my_requests`,
    `list_requestable_roles`, `request_role`, `get_user_assignments`,
    `unassign_role`, `list_my_team`, `list_my_managers`, `whoami`) lead with
    `tool`, `acting` and `server`, in every session. `acting` is who midPoint
    ran the call as, with its org links. It never refuses: under
    `identity.credentialIsShared` it describes the shared account and says so
    in `sharedCredential`. `server` carries the write gate,
    `requireRequestable`, `requestReason` (false until
    `requests.justificationItem` exists), the contract version (`1.0-draft.8`)
    and the server version. Text is unchanged.
  - The `initialize` result now carries `instructions`: name people, roles
    and requests by display name when writing to a person, and keep OIDs for
    tool calls. Every tool description ends with the same request.
  - With `MIDPOINT_MCP_ANONYMOUS_DISCOVERY=true`, `resources/list` and a
    `resources/read` of a `ui://midpoint/` URI need no token either. A read of
    any other URI, and a batch carrying one, still does.
  - No new dependency: `embed` and `testing/fstest` are standard library.

### Changed

- **Docs: `docs/ui-contract.md` `1.0-draft.9`.** The owner's D37 ("you
  approve your own part"): an inbox card shows "Step 1 of 2" instead of who
  else decides; the approve dialog's sentence follows the steps only; the
  approver's "Approval steps" name no people; an approval that leaves the
  case open no longer names the next approvers (`inbox.outcome.approvedNext`
  removed). Plus the fixes found building the inbox data (M10.2): text key
  order as the server writes it, best-effort approver lists, `allMustApprove`,
  removals without validity or reason, function names instead of stale line
  numbers, the input checks still coded `internal`, and the items fired on
  4.10.3 marked [live]. Views report `server.uiContract` `1.0-draft.9`.
  A person the approver can't see is named once: the reason's label, the
  reject sentence and the current-roles line got wordings without the name
  (D30).
- **The eleven view tools read the caller's own user first.** Building
  `acting` costs one `GET /ws/rest/self` (plus the by-OID re-read that names
  org links, as `whoami` does) before the tool runs, so a write never happens
  without it. Self lookups inside the same call share that read, so tools that
  already resolved the caller make no extra request. An unreachable midPoint now
  fails these tools with `resolving the acting identity: …`.

- **Module path is now `github.com/strazahq/midpoint-mcp-server`.** The
  repository moved to the `strazahq` organisation; the module path follows it so
  the canonical import matches where the code actually lives. Existing tags
  (`v0.1.0`–`v0.3.0`) remain resolvable under the old path via GitHub's
  redirect, so nothing pinned to them breaks; new versions are published under
  the new path. Consumers on an unpinned import should update it.
- **`request_role` refuses roles midPoint does not offer for request** (new
  default; `requests.requireRequestable: false` restores the old behaviour).
  `request_role` submits an ordinary assignment-add delta — midPoint converts
  that into an approval case only where policy matches and executes it
  immediately everywhere else, so on a deployment without approval policy the
  tool was an unrestricted grant path wearing a reassuring name. It is also
  byte-for-byte the same delta as `assign_role`, which does not claim to be a
  request. The check runs before the dry-run preview too: a preview promising
  "would request" for a role that would in fact be granted is the same false
  promise. Found live: `request_role` on a role with no approval policy granted
  it outright, and the tool reported "no approval case found; likely executed
  directly".
- **A `request_role` that executes immediately now says so plainly.** The result
  reads `GRANTED directly — no approval case was created` instead of hedging.

### Removed

- **`approve_work_item` and `reject_work_item`**, retired in favour of
  `decide_work_item`. The old tools completed whatever work item they were
  given; `decide_work_item` first checks that the item is open and assigned to
  the caller, refusing before any write otherwise, and reads back the outcome
  midPoint recorded. Clients calling the old names must switch to
  `decide_work_item` with `decision` set to `approve` or `reject` (same
  `caseOid`, `workItemId` and optional `comment`).

### Fixed

- **The `integration`-tagged tests compile again.** The team test still
  ranged over a team answer as a list after it became a `TeamResult`, so
  `go test -tags=integration` did not build the package.
- **Tool errors no longer carry midPoint's base URL (contract S14).** A
  transport failure was wrapped with Go's `*url.Error`, whose message repeats
  the full request URL, and that text reached tool results. Errors now name the
  operation and the REST path only: `calling midPoint GET /self: dial tcp …:
  connection refused`. A base URL that can't be parsed is reported the same
  way, as `building request for /self: …`.
- **Search results carry namespace-prefixed reference keys; the inbox now
  reads them.** In a `POST /{collection}/search` answer midPoint 4.10.3 writes a
  reference as `{"t:oid":…,"t:type":…,"t:relation":…,"targetName":…}`, while a
  single-object `GET` writes plain `oid` / `type` / `relation`. The reference
  decoder read only the plain keys, so every `assigneeRef` in the case search
  behind `list_work_items` came back without an OID and the inbox was empty for
  a caller who had an open work item (found live). References now decode in
  either spelling, matched on the key's local part with the plain key winning,
  and the other reference readers (`parentOrgRef`, `roleMembershipRef`,
  assignment targets) go through the same decoder. Regression tests use the
  real search shape, single and multi-assignee.
- **`list_work_items` read a work item's assignees as a single reference.**
  `assigneeRef` is multi-valued (delegation and escalation add assignees), so a
  work item with several assignees failed to decode and was silently left out of
  the caller's inbox. It now lists such an item when the caller is among them.
  It also no longer lists an item midPoint closed without a decision (a
  `closeTimestamp` but no output, e.g. cancelled when another approver decided
  the stage); `closeTimestamp` is midPoint's own open/closed marker. `get_case`
  shows every assignee.
- **`unassign_role` no longer sends a delta midPoint rejects.** The delete delta
  used an indexed path (`assignment[<id>]`), which midPoint answers with HTTP 400
  "Delta path must always point to item, not to value" — so the tool never removed
  anything. The path now points at the item (`assignment`) and the container id
  travels in the value (`{"@id": <id>}`), the form midPoint accepts. Found live
  against 4.10.3, where an administrator account got the same 400, so it was a
  request-shape problem and not an authorization one. Pinned by tests at both
  levels: the plan body, and the PATCH the tool actually issues with the write
  gate on.
- **README: what the resource-server service account must be authorized for.**
  It said the account "must hold the archetype-filtered `#proxy` authorization",
  which reads as if `#proxy` alone were enough. On 4.10.3 an account holding only
  `#proxy` is refused `403` on every REST endpoint, `/self` included: REST entry
  actions are evaluated against the authenticated account, so it needs those *and*
  `#proxy`. The paragraph now says so, and records that superuser's `#all` does
  cover `#proxy` while still not being an acceptable service-account profile.
  (`docs/identity-providers.md` and `docs/authorization.md` already carried the
  correction; the README did not.)
- **Self-scoped tools no longer claim an identity the server does not have.**
  `list_my_team`, `list_my_managers`, `list_work_items` and `list_my_requests`
  said "you" — but in personal mode midPoint sees the server's configured
  credentials, which in a shared deployment is a service account and not the
  human at the MCP client. All four now name the identity they answered for and
  carry it in structured output as `subject` (oid, name, mode); when the answer
  is empty in personal mode they say plainly that it describes that account.
  Found live: a gateway-brokered deployment where the server authenticated as a
  technical account reported "0 work items in your inbox" to a user who had one.
- **An empty team answer now says which kind of empty it is.** `list_my_team` /
  `list_my_managers` return the org links they were derived from (`orgs`), so
  "linked to no orgs at all" is distinguishable from "orgs found, but midPoint
  returned no members for this identity" — the latter being the authorization gap
  documented under M6. `GET /self` now asks for `resolveNames` so those orgs are
  reported by name.

## [0.3.0] - 2026-07-16

Self-service role catalog, a working audit search, and configurable OIDC
correlation, plus an identity-provider setup guide. Resource-server mode (M4.5)
was also verified live end to end against real Keycloak + midPoint 4.10.3 (no
token → refused; IdP-only user → refused; mapped user impersonated via
`Switch-To-Principal`).

### Added

- **Configurable OIDC correlation** — two new env vars,
  `MIDPOINT_MCP_OIDC_CORRELATION_CLAIM` (default `preferred_username`) and
  `MIDPOINT_MCP_OIDC_CORRELATION_ATTRIBUTE` (default `name`), let a deployment map
  a token to a midPoint user by any claim/attribute (e.g. `email`→`emailAddress`,
  Entra `oid`→a stable id, an employee number). The `sub`→`externalId` match still
  runs first; these customize only the fallback. Numeric claims are stringified;
  the attribute is validated as a plain query path at startup (injection guard).
  Verified live against Keycloak + midPoint 4.10.3: `email`→`emailAddress`
  impersonates the mapped user, and a deliberately-wrong pairing refuses (proving
  the configured claim value is the key, with no silent `preferred_username`
  fallback). Default behavior is unchanged.
- **Docs: `docs/identity-providers.md`** — a provider-agnostic guide to
  resource-server mode: the resource-server model (why the server needs no client
  secret), the audience requirement, token→midPoint correlation, the service
  account's `#proxy` authorization, and step-by-step walkthroughs for Microsoft
  Entra ID, Keycloak, and generic OIDC, plus a troubleshooting table. Linked from
  the README.
- **`list_requestable_roles`** — self-service catalog query: the roles the
  authenticated user can request. Searches roles flagged `requestable = true`,
  executed as the calling identity, so in resource-server mode midPoint filters
  results to what that user is authorized to see (requestable-and-visible). It
  completes the self-service loop already provided by `request_role`,
  `list_my_requests`, `list_work_items`, and the approval actions. Read-only
  (outside the write gate); verified live against 4.10.3. (Exact `#assign`
  eligibility via `getAssignableRoleSpecification` was considered but needs the
  script path, which — like `search_audit` — can't run under OIDC impersonation;
  the requestable-and-visible approximation was chosen deliberately.)

### Fixed

- **`search_audit` now works against a live midPoint 4.10** (previously HTTP 500,
  shipped as non-functional/experimental in 0.2.0). Diagnosed end to end against
  midPoint 4.10.3; three separate defects in the executeScript path:
  1. The bulk-action names were wrong for 4.10 — the generic dynamic `execute`
     action is *"unknown action executor"* and the generic `search` *"cannot be
     invoked dynamically"*. Fixed by using the **typed `<search>`** element to
     seed one input item and the **`execute-script`** action name.
  2. Output was read from `consoleOutput`, but `log.info` writes to the server
     log, not the script console — so records never came back. The script now
     **returns** each record as a tab-delimited `xsd:string`, which midPoint
     surfaces as a `dataOutput` item; parsing reads `item.value.@value`.
  3. There is no audit accessor on the scripting `midpoint` binding and
     `RepositoryService.searchContainers` rejects `AuditEventRecordType`
     (*"Missing mapping for schema type"*). The script now reaches
     `ModelAuditService` (via `modelInteractionService`) and calls
     `searchObjects(query, null, task, result)`; timestamp bounds use the JDK
     `DatatypeFactory` (the previously-imported `XmlTypeConverter` is not on the
     script classpath). **Verified live**: returns real records (e.g.
     `MODIFY_OBJECT` / `administrator` → `bob`), including server-side time-range
     filtering. Still requires script-execution authorization, so it remains
     unavailable under resource-server (OIDC) impersonation — documented, not a
     regression. Reaching `modelAuditService` uses reflection over a non-public
     field (the only route 4.10 exposes) and may need revisiting on a future
     midPoint; a live integration test asserts records parse with populated
     fields.

## [0.2.1] - 2026-07-15

### Fixed

- **List/search tools returned `count` correctly but empty `oid`/`name`** for
  every result (`search_users`, `list_roles`, `list_resources`, `search_objects`,
  `list_my_requests`, `list_work_items`, and `get_case`'s work items). Root cause:
  midPoint 4.10 double-wraps a search response — the `ObjectListType` sits under a
  top-level `object` key, and the actual results under *that* type's own nested
  `object` key (`{"object":{"@type":"…ObjectListType","object":[…]}}`) — but the
  decoder read the top-level `object` as the results array, so each row decoded
  the wrapper (no `oid`/`name`) instead of an object. `parseObjectList` now
  handles the nested shape (and still tolerates a flat array and single-element
  collapse). Get-by-oid tools and `ping` were unaffected (single-wrapped). Fixed
  and **verified live against midPoint 4.10.3** (`list_roles` → "Superuser",
  `search_objects` → real users). Fixtures updated to the real shape; a
  `parseObjectList` unit test covers every envelope form.

## [0.2.0] - 2026-07-15

Adds query-driven reporting (M5). **Ships with a known live-testing regression in
the list/search tools — see below.**

### Known issues (found live-testing against midPoint 4.10.3, 2026-07-15)

- `search_users` returns the correct count but **empty `name`/`oid` in
  `structuredContent.users`**. Live repro: search for an existing user →
  `{"count":1,"users":[{"name":"","oid":""}]}`. Likely cause: midPoint JSON
  renders `name` as a **PolyString object** (`{"orig":"bob","norm":"bob"}`),
  not a plain string — decode `orig` (and check every other PolyString-typed
  field: `fullName`, `displayName`, role/resource names). `ping` is unaffected
  (verified live); audit other read tools for the same decode gap.

### M5 — audit & reporting (read-only, query-driven)

- `search_objects` — filtered search across users / roles / orgs / services /
  shadows / resources using a raw midPoint query-language filter, returning
  compact type-agnostic summaries. The building block for ad-hoc reports
  (orphaned accounts, unused roles, disabled users with access, ...). Read-only
  and, in resource-server mode, executed as the mapped user so midPoint enforces
  that user's authorizations.
- `search_audit` — audit-trail queries (time range + event type / outcome /
  initiator / target / channel). **Experimental:** midPoint 4.10 exposes no REST
  audit endpoint (verified against the full endpoints table) and the bulk
  `search` action is objects-only, so this runs a server-side Groovy via the
  `executeScript` RPC that searches audit containers and prints delimited
  records. It requires script-execution authorization and therefore does not
  work under resource-server (#proxy) impersonation; the embedded script is
  isolated and may need per-version tuning, and the raw console is returned to
  aid that. Both tools are read-only (outside the write gate).
- No native report engine (its output lands on the server filesystem) and no
  local read-model — reporting stays ephemeral and query-driven, per PLAN.md.
- Tests: object-search routing/decoding + filter forwarding; executeScript
  response parsing; audit console parsing, Groovy construction, and client-side
  refinement; MCP round-trip for both tools. Live integration asserts
  `search_objects`; `search_audit` integration is best-effort (skips when
  script-exec is unavailable). `go test ./...` green.

## [0.1.0] - 2026-07-15

First tagged release. Covers M0–M4.5: stdio + streamable-HTTP transports, the
read/write/requests-&-approvals tool set with the write gate, and OIDC
resource-server identity for shared HTTP.

### M4.5 — OIDC resource-server identity

- Resource-server mode for HTTP: set `MIDPOINT_MCP_OIDC_ISSUER` +
  `MIDPOINT_MCP_OIDC_AUDIENCE` and every request must present an OAuth
  `Authorization: Bearer` token. Tokens are validated against the issuer's JWKS
  (signature, issuer, audience, expiry) via `go-oidc`; failures return 401.
- Each caller is mapped to a midPoint user — `sub` → `externalId`, falling back
  to `preferred_username` → `name` — and the request executes as that user via
  the `Switch-To-Principal: <oid>` header while authenticating as the service
  account (which holds the archetype-filtered `#proxy` authorization). The
  correlation search itself runs as the service account, not impersonated.
- Identity flows the guaranteed way: an SDK receiving-middleware reads the
  per-request `TokenInfo` and puts the correlated OID into the request context;
  the REST client sets `Switch-To-Principal` from the context. No on-behalf-of
  tool argument exists — identity comes only from the validated token.
- Non-loopback `--http` binding is unlocked **only** when OIDC is configured;
  otherwise the loopback-only rail from M4 stands. `MIDPOINT_MCP_OIDC_*` is
  all-or-nothing (a half-configured resource server is rejected at startup).
- Test-first (security-critical): token validation with a static JWKS and
  self-signed tokens (valid / expired / wrong-audience / wrong-issuer /
  untrusted-signature / malformed); correlation precedence and ambiguity;
  `Switch-To-Principal` header injection; config validation; and a full
  end-to-end test — mock OIDC (discovery + JWKS) + MCP client with a bearer
  token → `ping` → the mapped `Switch-To-Principal` reaches midPoint, and
  missing/expired/wrong-audience tokens are refused. `go test ./...` green.

### Dependencies

- `github.com/coreos/go-oidc/v3` — OIDC discovery, JWKS handling, and bearer
  token verification. Hand-rolling JWT signature/issuer/audience/expiry checks
  for a security boundary would be a liability; this is the canonical Go OIDC
  library. Pulls in `github.com/go-jose/go-jose/v4` (JWT/JWK crypto).
- `github.com/go-jose/go-jose/v4` — also imported directly in tests to mint
  signed tokens and serve a JWKS; already required transitively by `go-oidc`.

### M4 — HTTP transport + packaging

- `--http <addr>` runs the SDK's streamable HTTP transport at `/mcp` (stdio stays
  the default). `--version` prints the build version.
- **Safety rail (part of the milestone):** HTTP binds `127.0.0.1` by default and
  *refuses to start* on any non-loopback address — there is no bypass flag.
  HTTP mode has no per-request authentication yet (that is M4.5), so it must not
  expose an unauthenticated network surface. In M4, HTTP is still personal mode
  (the configured credentials' identity) over a different transport. The SDK's
  built-in DNS-rebinding protection is left enabled.
- `Dockerfile`: multi-stage build producing a static (`CGO_ENABLED=0`) binary on
  `scratch`, with CA certificates copied in (for HTTPS to midPoint) and a
  non-root user. `.dockerignore` trims the build context.
- Release automation: `.github/workflows/release.yml` cross-builds static
  binaries (linux/darwin/windows × amd64/arm64) on a `vX.Y.Z` tag and publishes
  them with checksums to a GitHub release; `main.version` is injected via
  `-ldflags`. `.github/workflows/ci.yml` runs gofmt/vet/tests on push and PR.
- README: MCP client config snippets (Claude Desktop, VS Code), Docker usage, and
  the HTTP transport with its loopback-only caveat.
- Tests: address resolution / non-loopback refusal (table-driven) and an in-
  process `/mcp` initialize smoke test. `go test ./...` green.

### M3 — requests & approvals (self-service)

- Six tools: `request_role`, `list_my_requests`, `list_work_items`, `get_case`,
  `approve_work_item`, `reject_work_item`.
- `request_role` submits an assignment-add delta on the target user (defaults to
  the authenticated user); midPoint policy decides whether it executes directly
  or opens an approval **case**. The requester identity is always the
  authenticated principal — midPoint sets `requestorRef`; the tool never accepts
  an on-behalf-of requester. After applying, it best-effort surfaces the created
  case oid.
- `list_my_requests` scopes cases to `requestorRef` = self; `list_work_items` is
  the caller's inbox — open cases where `workItem/assigneeRef` = self, returning
  only the caller's still-open work items. Both resolve the caller via
  `/ws/rest/self`, so identity comes from the credentials, not from arguments.
- `approve_work_item` / `reject_work_item` complete a work item via
  `POST /ws/rest/cases/{oid}/workItems/{id}/complete` with the approval-outcome
  URI; both respect the write gate (dry-run preview when off), as does
  `request_role`.
- REST verified against the 4.10 docs: cases `GET`/`POST …/search`,
  work-item `…/complete` (204). Note: an older support-4.10 example page still
  flags work-item completion as unimplemented (MID-6067), but the current cases
  endpoint reference documents it; the integration test is the live check.
- Tests: fixture-based decoding of cases/work-items (incl. inbox filtering to
  self + open items), request/complete delta correctness, MCP round-trip and
  gate on/off proofs. Integration test extended with the full request → case →
  approve → assignment-appears flow (opt-in via `MIDPOINT_IT_APPROVAL_ROLE_OID`).
  `go test ./...` green.

### M2 — write tools + gate

- Six write tools: `create_user`, `enable_user`, `disable_user`, `assign_role`,
  `unassign_role`, `recompute_user`.
- `MIDPOINT_MCP_ALLOW_WRITES` gate (default off). When off, every write tool
  returns a **dry-run preview** — the exact method, endpoint, and request body it
  *would* send — and makes no mutating call. When on, it applies the change and
  reports the result (e.g. the new oid from the create `Location` header).
- REST mapping verified against the 4.10 docs: create via `POST /ws/rest/users`
  (`{"user":{…}}` → 201 + `Location`); enable/disable/assign/unassign via
  `PATCH /ws/rest/users/{oid}` with an `objectModification`/`itemDelta`
  (`replace activation/administrativeStatus`; `add assignment`; `delete
  assignment[<id>]`); recompute via `PATCH …?options=reconcile` with an empty
  modification. `unassign_role` reads the user first to resolve the exact
  assignment container id(s).
- Write path is structured as build-a-Plan then apply, so the preview and the
  applied request are guaranteed identical.
- Tests: delta-JSON correctness for every plan, `Location`→oid parsing, and MCP
  round-trip tests proving the gate — **gate off makes no write call and returns
  a preview; gate on issues the expected PATCH/POST**. Integration test extended
  with a live disable→enable round-trip (runs only when the gate is on).
  `go test ./...` green.

### M1 — read tools

- Seven read-only MCP tools: `search_users` (free-text over name/full
  name/email, or exact OID), `get_user`, `get_user_assignments` (direct
  assignments plus effective membership, each flagged direct or inherited),
  `list_roles`, `get_role`, `list_resources`, `get_resource` (with connection
  status where midPoint reports it). All return structured output plus a
  human-readable line.
- REST client extended with a generic request core (GET/POST, query options),
  `POST /ws/rest/{type}/search` using midPoint's text query language, and
  `GET /ws/rest/{type}/{oid}?options=resolveNames`. Tolerant decoding handles
  midPoint's JSON quirks: PolyStrings as string or object, and single-element
  collections serialized as a bare object instead of an array.
- Search input is escaped into query-language string literals so a
  caller-supplied value can't inject filter syntax; result sizes are capped
  (default 20, max 100).
- Table-driven tests against recorded REST fixtures (`internal/midpoint/testdata`)
  covering search/get/list, the array/single-object envelope, direct-vs-inherited
  membership, and the injection-escaping guard; plus an in-process MCP
  round-trip test that drives every tool through the SDK (verifying input/output
  schema validation, including empty arrays marshaling as `[]` not `null`).
- Integration test (`-tags=integration`) against a live midPoint 4.10 container;
  skips cleanly when `MIDPOINT_*` is unset, so a missing container is never a
  failure. `go test ./...` green.

### M0 — scaffold

- Go module `github.com/strazahq/midpoint-mcp-server` (Go 1.25; the MCP SDK
  requires >= 1.25).
- stdio MCP server exposing one tool, `ping`, which calls midPoint
  `GET /ws/rest/self` and returns the authenticated identity (oid, name, and
  full name / email when set), as both human-readable text and structured
  output.
- `internal/midpoint`: minimal REST client with HTTP Basic auth. Config comes
  from `MIDPOINT_URL`, `MIDPOINT_USERNAME`, `MIDPOINT_PASSWORD`; optional
  `MIDPOINT_INSECURE_TLS=true` skips certificate verification for self-signed
  dev instances only. Credentials are read from the environment at runtime and
  never appear in code, logs, or errors.
- Table-driven tests for the client (recorded `/ws/rest/self` responses via
  `httptest`, covering PolyString-object and bare-string `name` forms, non-2xx
  handling, and a guard that errors never leak the password) and for
  `ConfigFromEnv`. `go test ./...` green.

### Dependencies

- `github.com/modelcontextprotocol/go-sdk` v1.6.1 — the official Model Context
  Protocol SDK for Go; mandated by `PLAN.md` as the server framework. Its
  transitive dependencies (`google/jsonschema-go`, `segmentio/encoding`,
  `segmentio/asm`, `yosida95/uritemplate`, `golang.org/x/oauth2`,
  `golang.org/x/sys`) are pulled in indirectly. No other direct dependencies —
  everything else uses the standard library.
