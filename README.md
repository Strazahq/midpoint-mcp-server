# midPoint MCP Server

A [Model Context Protocol](https://modelcontextprotocol.io) server for
[Evolveum midPoint](https://evolveum.com/midpoint/), exposing identity
governance operations as MCP tools so AI assistants can query and (optionally)
manage users, roles, and resources through midPoint's REST API.

> **Status: early development.** The tool surface below is the design target.
> Implemented so far: `ping` (M0), the read tools (M1), the write tools with
> their gate (M2), self-service requests & approvals (M3), the streamable-HTTP
> transport + packaging (M4), OIDC resource-server identity for shared HTTP
> (M4.5), and query-driven reporting (M5).

## Two modes: personal vs shared

The server runs in one of two identity modes. **Most users want the first, and it
needs no identity provider.**

**Personal mode — stdio (the default).** You run the binary locally (Claude
Desktop, VS Code, a script) with *your own* midPoint credentials. It acts to
midPoint as you; midPoint sees you as you. No OAuth, no OIDC, no Keycloak — just
`MIDPOINT_URL` + `MIDPOINT_USERNAME` + `MIDPOINT_PASSWORD`. This is the common
case.

**Resource-server mode — HTTP + OIDC (opt-in).** One shared server serves many
people over the network, each authenticated by their own OAuth bearer token, and
each request runs as the *real* human so approvals and audit attribute correctly.
This is the only mode that needs an identity provider — and it is *any* OIDC
provider (Keycloak, Okta, Entra / Azure AD, Auth0, …), not a specific one. You opt
in by setting `MIDPOINT_MCP_OIDC_ISSUER` / `MIDPOINT_MCP_OIDC_AUDIENCE`; leave them
unset and the OIDC code never runs.

| You are… | You set | Transport | Identity provider |
| --- | --- | --- | --- |
| one person, your own machine | `MIDPOINT_URL` + your username/password | stdio (default) | **none** |
| a team sharing one server | the above **+** `MIDPOINT_MCP_OIDC_ISSUER` / `_AUDIENCE` | `--http` | **any OIDC** |

Why the split? A single shared server must know *which* human is behind each
request, and it can't trust a caller to self-declare — there is deliberately no
on-behalf-of tool argument — so it requires a signed token from an IdP the
organization already runs. A local personal process has no such problem: it simply
*is* one person with their own credentials. See [Running](#running) for both.

> **Personal mode with a *service* account is the case to watch.** Personal mode
> is correct when the credentials belong to the person using it. Point it at a
> shared technical account — a gateway spawning the server per session, for
> instance — and the self-scoped tools ("my team", "my inbox") start answering
> for that account while the human reads the answer as being about themselves.
> `whoami` always says which is happening, and
> [`identity.credentialIsShared`](#settings-file) makes those tools refuse rather
> than mislead. The actual fix is resource-server mode.

## Configuration

Credentials are read from the environment at runtime (never written to disk):

| Variable | Required | Purpose |
| --- | --- | --- |
| `MIDPOINT_URL` | yes | midPoint deployment root, e.g. `https://localhost:8443/midpoint` |
| `MIDPOINT_USERNAME` | yes | REST user for HTTP Basic auth |
| `MIDPOINT_PASSWORD` | yes | password for that user |
| `MIDPOINT_INSECURE_TLS` | no | `true` skips TLS verification — self-signed dev instances only |
| `MIDPOINT_MCP_ALLOW_WRITES` | no | `true` enables the write tools; otherwise they return a dry-run preview |
| `MIDPOINT_MCP_OIDC_ISSUER` | no | OIDC issuer URL; enables resource-server mode for HTTP (must be set with the audience) |
| `MIDPOINT_MCP_OIDC_AUDIENCE` | no | expected token audience for resource-server mode |
| `MIDPOINT_MCP_OIDC_CORRELATION_CLAIM` | no | token claim matched to a midPoint user (default `preferred_username`); see [docs](docs/identity-providers.md#requirement-2--correlation-which-midpoint-user-is-this) |
| `MIDPOINT_MCP_OIDC_CORRELATION_ATTRIBUTE` | no | midPoint attribute the claim is matched against (default `name`) |
| `MIDPOINT_MCP_CONFIG` | no | path to a JSON settings file (below) — org modelling and self-service guardrails |

In resource-server mode, `MIDPOINT_USERNAME`/`MIDPOINT_PASSWORD` are the **service
account** — it authenticates the server to midPoint and must hold the
archetype-filtered `#proxy` authorization so it can act as the mapped end users.

### Settings file

Some behaviour can't be guessed: deployments model org structure differently, and
a wrong guess silently returns nobody. `MIDPOINT_MCP_CONFIG` points at a JSON file
of **non-secret** settings — credentials are never read from it. Every key is
optional; see [`examples/midpoint-mcp.config.json`](examples/midpoint-mcp.config.json)
for an annotated copy (keys prefixed `//` are treated as comments; any *other*
unknown key is an error, so a typo can't silently keep the default).

| Key | Default | Purpose |
| --- | --- | --- |
| `identity.credentialIsShared` | `false` | `true` declares `MIDPOINT_USERNAME` a technical/shared account: self-scoped tools then **refuse** in personal mode rather than answering for the service account (see below) |
| `team.orgSource` | `parentOrgRef` | where a caller's org links come from: `parentOrgRef` (midPoint's computed membership), `assignment` (org assignments only), `fallback` (parentOrgRef, then assignments when empty), `both` |
| `team.managerRelation` | `manager` | relation local part marking a manager of an org — local part only, not `org:manager` |
| `team.memberRelation` | `default` | relation local part marking plain membership, used when searching an org for members |
| `team.orgOids` / `team.orgNames` | all | which of the caller's orgs count as "my team". Empty means all of them, so a user in several orgs gets everyone from all of them out of `list_my_teammates`. Names match case-insensitively |
| `requests.requireRequestable` | `true` | refuse `request_role` for roles midPoint's catalog does not flag `requestable` |

**On `credentialIsShared`.** Personal mode assumes the credentials *are* the
person. When they're a shared service account — a gateway spawning the server
per-session, say — that assumption breaks quietly: midPoint answers correctly for
the service account, and the human reads it as an answer about themselves.
`whoami` always reports which of the two is happening; setting
`identity.credentialIsShared: true` makes the self-scoped tools refuse instead of
answering, and the real fix is resource-server mode, which gives each request the
caller's own identity.

## Running

The server speaks **stdio** by default (personal mode — it acts with the
identity of the configured `MIDPOINT_*` credentials). Point your MCP client at
the binary and pass the environment.

### Claude Desktop

In `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "midpoint": {
      "command": "/path/to/midpoint-mcp-server",
      "env": {
        "MIDPOINT_URL": "https://localhost:8443/midpoint",
        "MIDPOINT_USERNAME": "administrator",
        "MIDPOINT_PASSWORD": "your-password"
      }
    }
  }
}
```

### VS Code

In `.vscode/mcp.json` (or your user-level `mcp.json`):

```json
{
  "servers": {
    "midpoint": {
      "command": "/path/to/midpoint-mcp-server",
      "env": {
        "MIDPOINT_URL": "https://localhost:8443/midpoint",
        "MIDPOINT_USERNAME": "administrator",
        "MIDPOINT_PASSWORD": "your-password"
      }
    }
  }
}
```

### Docker

```sh
docker build -t midpoint-mcp-server .
docker run --rm -i \
  -e MIDPOINT_URL=https://host:8443/midpoint \
  -e MIDPOINT_USERNAME=administrator \
  -e MIDPOINT_PASSWORD=your-password \
  midpoint-mcp-server
```

The image is `scratch` plus the static binary and CA certificates, and runs as a
non-root user. `-i` keeps stdin open for the stdio transport.

### HTTP transport

```sh
midpoint-mcp-server --http :3001   # streamable transport at http://127.0.0.1:3001/mcp
```

**Personal mode (no OIDC):** HTTP binds `127.0.0.1` by default and *refuses to
start* on any non-loopback address. Without per-request auth, a network-reachable
endpoint would let every caller act as the single configured identity, so this is
loopback-only by design. Use it for a local client over HTTP; use stdio for
everything else.

**Resource-server mode (OIDC):** set `MIDPOINT_MCP_OIDC_ISSUER` and
`MIDPOINT_MCP_OIDC_AUDIENCE`. Now every request must carry an
`Authorization: Bearer` token:

- the token is validated against the issuer's JWKS (signature, issuer, audience,
  expiry) — invalid tokens get `401`;
- the caller is mapped to a midPoint user (`sub` → `externalId`, else
  `preferred_username` → `name`);
- the request executes **as that user** via midPoint's `Switch-To-Principal`
  header, while the server authenticates as the `#proxy` service account — so
  approvals and audit attribute to the real human.

Because requests are authenticated per user, binding a non-loopback address is
allowed in this mode:

```sh
MIDPOINT_MCP_OIDC_ISSUER=https://keycloak.example.com/realms/corp \
MIDPOINT_MCP_OIDC_AUDIENCE=midpoint-mcp \
midpoint-mcp-server --http 0.0.0.0:3001
```

Identity always comes from the validated token — there is no on-behalf-of tool
argument a caller could use to act as someone else.

**Setting up an identity provider** (Entra ID, Keycloak, Okta, Auth0, …) — how to
configure the audience, correlate tokens to midPoint users, and grant the service
account its `#proxy` authorization — is covered in
[docs/identity-providers.md](docs/identity-providers.md). The short version: the
server is a *resource server*, so it needs **no client secret** — only the issuer
and audience.

**What the midPoint account may do** — the least-privilege roles for both
deployment shapes, verified against midPoint 4.10.3 — is covered in
[docs/authorization.md](docs/authorization.md), with importable examples:

- [`examples/role-mcp-rs-service.xml`](examples/role-mcp-rs-service.xml) — for
  resource-server (OIDC) mode: REST entry plus archetype-scoped `#proxy`, and
  **no model rights at all**. The account can read nothing and change nothing by
  itself; it only borrows the authorizations of the correlated end user.
- [`examples/role-mcp-direct-service.xml`](examples/role-mcp-direct-service.xml) —
  for a shared technical account: exactly the REST endpoints and model operations
  the tools use, replacing the Superuser role such deployments usually reach for.

Neither profile should be a superuser. If you are wondering whether `#proxy` can be
restricted to approvals only, that question is answered (with the reasoning) in the
authorization doc.

## Tools

Identity (**implemented**, read-only):

- `ping` — connectivity check; reports the authenticated identity
- `whoami` — the identity midPoint executes as, how it was established
  (`personal` / `resource-server`), whether the request is impersonated, and that
  identity's org links. **Call this first when a `list_my_*` tool comes back
  unexpectedly empty** — it separates "you genuinely have none" from "this server
  is not acting as you"

Read (default, **implemented**):

- `search_users` / `get_user` — find identities by name, email, or OID
- `list_roles` / `get_role` — role catalog and definitions
- `list_resources` / `get_resource` — connected systems and their status
- `get_user_assignments` — what a user actually has, and why

Write (**implemented**; off unless `MIDPOINT_MCP_ALLOW_WRITES=true`, otherwise
each returns a dry-run preview of the exact request it would send):

- `create_user`, `enable_user`, `disable_user`
- `assign_role`, `unassign_role`
- `recompute_user` — trigger midPoint's recompute after changes

Requests & approvals (**implemented**; reads are always available, `request_role`
and the approval actions respect the write gate):

- `list_requestable_roles` — the roles you can request: those flagged
  `requestable` in the catalog, filtered to what you're authorized to see (runs
  as you, so it works per-user in resource-server mode). Pass `forUser` (a report's
  OID from `list_my_team`) to list what that report can be given but doesn't
  already hold — then `request_role` for them
- `request_role` — request a role for yourself or a report. It submits an
  assignment-add delta, and midPoint's policy decides whether that opens an
  approval case or applies immediately — so by default it **refuses roles the
  catalog does not flag `requestable`**, because for those it would grant rather
  than request. Use `assign_role` for a deliberate grant, or set
  `requests.requireRequestable: false` to lift the guardrail. When no approval
  policy matches a requestable role, the result says the role was GRANTED
- `list_my_requests` — approval cases you initiated
- `list_work_items` — your approval inbox
- `get_case` — a case and its work items
- `approve_work_item` / `reject_work_item` — decide a work item

Manager & team (**implemented**, read-only; run as the caller, so midPoint scopes
them to what that manager may see):

- `list_my_team` — your direct reports: members of the orgs you manage (empty if
  you manage none). Pair with `get_user_assignments` to review a report's access,
  `list_requestable_roles?forUser=` to see what they can be given, and
  `request_role` (which accepts a target user) to request it for them
- `list_my_managers` — who you report to: the managers of the orgs you belong to
- `list_my_teammates` — your peers: the other members of the orgs you belong to.
  Which orgs count is a deployment question — narrow it with `team.orgOids` /
  `team.orgNames`, or a user in several orgs gets everyone from all of them

All three name the identity they answered for and return the org links they were
derived from, so an empty result says *why*: no qualifying org links at all, or
org links whose members midPoint did not return for this identity (the
authorization gap below). Where those links are read from is `team.orgSource`.

> Manager tools run as the caller, so a **non-superuser manager needs read
> authorization over their reports** for `list_my_team` to return anyone — being an
> org `manager` is not sufficient by itself. Grant it with a role whose read
> authorization is scoped by `orgRelation` (verified against 4.10):
>
> ```jsonc
> // Role authorization: read the users in orgs where I am the manager.
> { "action": ["…/authorization-model-3#read"],
>   "object": [{ "type": "UserType",
>                "orgRelation": { "subjectRelation": "org:manager",
>                                 "scope": "allDescendants" } }] }
> ```

Reporting (**implemented**, read-only):

- `search_objects` — filtered search across users/roles/orgs/services/shadows/
  resources with a midPoint query-language filter (ad-hoc reports: orphaned
  accounts, unused roles, ...)
- `search_audit` — audit-trail queries (time range + initiator / target / event
  type / outcome / channel). midPoint 4.10 has no REST audit endpoint, so this
  runs a server-side script that reaches the audit service and returns the
  records. It therefore needs script-execution authorization and does **not** work
  under OIDC impersonation (the mapped end user lacks that privilege) — use it in
  personal/service-account mode

## Design

- Single static binary (Go, official MCP SDK), no runtime dependencies
- **stdio** transport by default (personal mode) — drops into Claude Desktop,
  VS Code, or any MCP client; **streamable HTTP** via `--http` (loopback-only
  unless OIDC resource-server mode is configured, then per-user via bearer tokens)
- Talks to midPoint's REST API (4.8+); credentials via environment variables,
  never written to disk
- Write operations are off unless `MIDPOINT_MCP_ALLOW_WRITES=true` — an AI
  assistant reading your IGA is useful, one mutating it is a decision

## Development

- `go test ./...` — unit tests against recorded REST fixtures, no external
  dependencies.
- `go test -tags=integration ./...` — additionally runs live tests against a
  real midPoint (e.g. a 4.10 docker container). Point it at an instance with the
  `MIDPOINT_*` variables above; it skips when they are unset.
- CI (`.github/workflows/ci.yml`) runs `gofmt`, `go vet`, and the tests on every
  push and PR. Pushing a `vX.Y.Z` tag triggers `release.yml`, which cross-builds
  static binaries (linux/darwin/windows, amd64/arm64) and attaches them, with
  checksums, to a GitHub release.

## License

Apache-2.0
