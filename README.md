# midPoint MCP Server

midpoint-mcp-server connects AI assistants to
[Evolveum midPoint](https://evolveum.com/midpoint/) through the
[Model Context Protocol](https://modelcontextprotocol.io) (MCP). It gives an
assistant 27 tools to look up users, roles and resources, request roles, decide
approvals, see a manager's team and, when you allow it, change midPoint. Every call
goes through midPoint's REST API as a real midPoint user, so midPoint's own
authorizations, approval policies and audit trail apply to it. It is written for
midPoint administrators who want to offer an assistant to their users or use one
themselves. It ships as one static Go binary and is tested against midPoint 4.10.

## Choose how to run it

The server has two modes. The mode decides which midPoint user each call runs as.

Personal mode is the default. You run the server on your own machine, your MCP
client starts it over stdio, and it signs in to midPoint with your own credentials.
midPoint sees you because the server signs in as you. No identity provider is
involved.

Shared mode serves many people from one server over HTTP. Each request carries an
OpenID Connect (OIDC) access token. The server validates the token, finds the
matching midPoint user and runs the call as that user through a service account.
midPoint sees the real person behind every request.

| | Personal mode | Shared mode |
| --- | --- | --- |
| Who it serves | One person | Many people |
| Transport | stdio, or HTTP on the local machine only | HTTP on any address |
| midPoint login | The person's own | A service account that acts as each caller |
| midPoint runs each call as | The configured login | The user matched to the request's token |
| Identity provider | None | Any OIDC provider, such as Keycloak or Microsoft Entra ID |
| Required settings | `MIDPOINT_URL`, `MIDPOINT_USERNAME`, `MIDPOINT_PASSWORD` | The same three, plus `MIDPOINT_MCP_OIDC_ISSUER` and `MIDPOINT_MCP_OIDC_AUDIENCE` |

Shared mode needs both the issuer and audience settings and the `--http` flag.
Over stdio the server always runs in personal mode, and without the two OIDC
settings the token code never runs.

Personal mode assumes the credentials belong to the person using the assistant. If
you run it with a shared technical account instead, for example when a gateway
starts one server process per session, midPoint answers for that account. Tools
such as `list_my_team` and `list_work_items` then describe the account, not the
person reading the answer. The `whoami` tool always says which identity the server
acts as. Set `identity.credentialIsShared` in the [settings file](#the-settings-file)
to make the self-scoped tools refuse instead of answering. Shared mode is the real
fix, because each request then carries the caller's own identity.

## Get started

In the examples below, replace the `example.com` addresses and every value in angle
brackets, such as `<service-account-password>`, with your own.

### Get the binary

Download the archive for your platform from this repository's
[GitHub releases](https://github.com/strazahq/midpoint-mcp-server/releases).
Releases are built for Linux and macOS on amd64 and arm64, and for Windows on
amd64. [Check a release](#check-a-release) lists the files a release carries and
shows how to verify them. To build it yourself you need Go 1.25 or later:

```sh
CGO_ENABLED=0 go build -o midpoint-mcp-server .
```

`midpoint-mcp-server --version` prints the version, as [Versions](#versions)
explains.

### Check a release

Each release carries these files, where `<tag>` is the release tag, such as
`v1.2.3`:

- `midpoint-mcp-server_<tag>_<os>_<arch>.tar.gz` for `linux` and `darwin` on
  `amd64` and `arm64`, and `midpoint-mcp-server_<tag>_windows_amd64.zip`. Each
  archive holds the binary, `README.md`, `LICENSE` and `CHANGELOG.md`.
- SPDX SBOMs in JSON: `midpoint-mcp-server_<tag>_binaries.spdx.json` for the
  binaries, and `midpoint-mcp-server_<tag>_image_linux_amd64.spdx.json` and
  `midpoint-mcp-server_<tag>_image_linux_arm64.spdx.json` for the container image.
- `midpoint-mcp-server-kubernetes.yaml`, the manifests from
  [deploy/kubernetes](deploy/kubernetes/README.md) with the image pinned to the
  release's version.
- `checksums.txt`, the SHA-256 sum of each file above, and
  `checksums.txt.sigstore.json`, its signature bundle.

The release workflow signs `checksums.txt` and the
[container image](#docker) with cosign's keyless signing, so no key is published.
The signing certificate names this repository's release workflow and the tag it
ran for. Verify the signatures with cosign 3 or later. For the downloads, run this in the
directory that holds them:

```sh
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/Strazahq/midpoint-mcp-server/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --ignore-missing -c checksums.txt
```

For the image, where `<version>` is the release version without its `v`, such as
`1.2.3`:

```sh
cosign verify ghcr.io/strazahq/midpoint-mcp-server:<version> \
  --certificate-identity-regexp '^https://github\.com/Strazahq/midpoint-mcp-server/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

GitHub also holds a build provenance attestation for each archive, each binary
and the image. `gh attestation verify <file> --repo strazahq/midpoint-mcp-server`
checks one file, and the same command with
`oci://ghcr.io/strazahq/midpoint-mcp-server:<version>` in place of the file checks
the image.

### Personal mode in three steps

1. Add the server to your MCP client. For Claude Desktop, put this in
   `claude_desktop_config.json`:

   ```json
   {
     "mcpServers": {
       "midpoint": {
         "command": "<install-dir>/midpoint-mcp-server",
         "env": {
           "MIDPOINT_URL": "https://midpoint.example.com/midpoint",
           "MIDPOINT_USERNAME": "<your-midpoint-login>",
           "MIDPOINT_PASSWORD": "<your-midpoint-password>"
         }
       }
     }
   }
   ```

2. Restart the client.
3. Ask the assistant to run the `whoami` tool. It answers with your midPoint login
   and says `personal mode`.

The write tools only return a dry-run preview until you add
`"MIDPOINT_MCP_ALLOW_WRITES": "true"` to `env`. The client's configuration file now
holds your password, so keep it private.

### Shared mode in five steps

Read [docs/misconfigurations.md](docs/misconfigurations.md) before you start the
server in step 4. It lists the configuration mistakes that let one person act as
another in midPoint.

1. In your identity provider, make the access tokens meant for this server carry an
   audience (`aud`) of your choice, for example `midpoint-mcp`.
   [docs/identity-providers.md](docs/identity-providers.md) shows how for common
   providers. [examples/keycloak.env](examples/keycloak.env) and
   [examples/entra.env](examples/entra.env) are annotated starting points.
2. In midPoint, create a service account for the server. Give it a role based on
   [examples/role-mcp-rs-service.xml](examples/role-mcp-rs-service.xml), after
   replacing its placeholder archetype oids with the archetypes of the users it may
   act for. The server also looks users up as this account, so it needs read access
   to the user attributes it matches on, which that example role does not grant.
   [docs/authorization.md](docs/authorization.md#let-the-service-account-find-users)
   shows that extra authorization, and the rest of that document explains each
   grant. That read authorization
   has not yet been tested live with this server, so run the correlation check in
   [Verify a deployment](docs/authorization.md#verify-a-deployment) after you add it.
3. Make sure every person matches exactly one midPoint user. With the default
   settings, the token's `preferred_username` must equal the user's `name`. The
   server first tries the token's `sub` against a user item called `externalId`, but
   midPoint 4.10's standard user schema has no such item, so on a stock deployment
   that first search never matches. Each person also needs midPoint authorizations
   of their own, such as the built-in End user role, because every call runs as
   them.
4. Start the server:

   ```sh
   export MIDPOINT_URL=https://midpoint.example.com/midpoint
   export MIDPOINT_USERNAME=mcp-service
   export MIDPOINT_PASSWORD='<service-account-password>'
   export MIDPOINT_MCP_OIDC_ISSUER=https://idp.example.com/realms/corp
   export MIDPOINT_MCP_OIDC_AUDIENCE=midpoint-mcp
   midpoint-mcp-server --http 0.0.0.0:3001
   ```

   This minimal example keeps the default correlation, which fits people in the
   Keycloak setup in [docs/identity-providers.md](docs/identity-providers.md). On
   Microsoft Entra ID the default claim is unsafe, so use the settings in
   [examples/entra.env](examples/entra.env) instead. The startup log line says
   `resource-server mode (OIDC bearer)`.
5. Check it. `curl -i -X POST http://localhost:3001/mcp` must answer `401`, because
   it carries no token. Then put a TLS-terminating proxy in front of the server, as
   [HTTP transport](#http-transport) explains, connect your MCP client or gateway to
   the proxy's `https://` address for `/mcp` with a person's access token, and call
   `whoami`. It names that person and says `resource-server mode`.

The client or gateway in front of the server gets the access token from your
identity provider and sends it as `Authorization: Bearer <token>` on every request.
The server only validates tokens, so it needs no client secret. It does not publish
OAuth protected-resource metadata, so it cannot point a client to the identity
provider by itself.

## Understand how shared mode decides who is calling

On every HTTP request, shared mode answers four questions in order. A request that
fails one of the first three gets HTTP `401` and never reaches a tool. No tool
argument can change who the caller is.

1. Is the token signed by the identity provider you trust? At startup the server
   reads the provider's discovery document for `MIDPOINT_MCP_OIDC_ISSUER`, and it
   refuses to start if it cannot. On each request it checks the token's signature
   against the provider's published keys, checks that the `iss` claim equals the
   configured issuer exactly, and checks that the token has not expired. Only
   asymmetric signatures (RSA, ECDSA, RSA-PSS and EdDSA) are accepted, and a token
   without an expiry is refused.
2. Was the token issued for this server? Its `aud` claim must contain
   `MIDPOINT_MCP_OIDC_AUDIENCE`. A valid token that the provider issued for another
   application fails here.
3. Which midPoint user is it? The server first looks for a user whose `externalId`
   equals the token's `sub`. midPoint 4.10's standard user schema has no
   `externalId` item, so on a stock deployment this search fails and the server
   moves on without reporting it. If the first search fails or finds no single
   user, the server looks for a user whose correlation attribute (`name` by default)
   equals the token's correlation claim (`preferred_username` by default). If
   neither lookup finds exactly one user, the request is refused. The lookup runs as
   the service account, without impersonation, so that account needs read access to
   the users it may match. Tokens that an agent or a service obtains for itself
   follow a stricter path, set up with the two `MIDPOINT_MCP_OIDC_CLIENT_*` settings.
4. What may that user do? The server calls midPoint as the service account and adds
   the header `Switch-To-Principal: <user-oid>`, so midPoint runs the call as the
   matched user. midPoint decides with its own authorizations. The service account
   needs REST access to the endpoints the tools use and the `#proxy` authorization,
   scoped to the users it may act for. The user needs the authorizations for the
   operation itself. The server grants nothing of its own, and midPoint's approvals
   and audit records name the real person.

[docs/identity-providers.md](docs/identity-providers.md) covers provider setup,
correlation and agent tokens in depth. [docs/authorization.md](docs/authorization.md)
covers the service account's role and the reason for each grant.
[docs/misconfigurations.md](docs/misconfigurations.md) lists the configuration
mistakes that break security and how to spot them.

## Configure the server

All settings are environment variables, plus one optional settings file.
Credentials are read from the environment only, and the server never writes them to
disk, logs or tool output. A true/false setting is on only when its value is
`true`, in any letter case.

### Settings for both modes

| Variable | Default | What it does |
| --- | --- | --- |
| `MIDPOINT_URL` | required | The midPoint deployment root, such as `https://midpoint.example.com/midpoint`. The server appends `/ws/rest/...` to it. |
| `MIDPOINT_USERNAME` | required | The midPoint login the server signs in with. In personal mode it is the person's own. In shared mode it is the service account. |
| `MIDPOINT_PASSWORD` | required | The password for that login. |
| `MIDPOINT_INSECURE_TLS` | `false` | `true` skips TLS certificate checks on the connection to midPoint. Use it only for test instances with self-signed certificates. It does not apply to the identity provider. |
| `MIDPOINT_MCP_ALLOW_WRITES` | `false` | `true` lets the write tools change midPoint. Otherwise each write tool returns a dry-run preview of the REST call it would send. |
| `MIDPOINT_MCP_CONFIG` | unset | Path to the optional [settings file](#the-settings-file). |

### Settings for shared mode

| Variable | Default | What it does |
| --- | --- | --- |
| `MIDPOINT_MCP_OIDC_ISSUER` | unset | The issuer URL, exactly as the `iss` claim of your tokens has it. Set together with the audience, it turns on shared mode. |
| `MIDPOINT_MCP_OIDC_AUDIENCE` | unset | The value the token's `aud` claim must contain. Set it together with the issuer. |
| `MIDPOINT_MCP_OIDC_DISCOVERY_URL` | the issuer's well-known path | The full URL of the provider's discovery document, used exactly as given. Use it when the server reaches the provider at another address than the issuer in the tokens, such as inside a container network. The document must still name the configured issuer, and tokens are still checked against it. |
| `MIDPOINT_MCP_OIDC_CORRELATION_CLAIM` | `preferred_username` | The token claim matched against the correlation attribute when the token's `sub` matches no `externalId`. On a stock midPoint 4.10 this is the match that decides, because the `externalId` search finds nobody. |
| `MIDPOINT_MCP_OIDC_CORRELATION_ATTRIBUTE` | `name` | The midPoint user attribute that claim is matched against, such as `emailAddress` or `extension/badgeId`. Letters, digits and `/` only. |
| `MIDPOINT_MCP_OIDC_CLIENT_CORRELATION_CLAIM` | unset | A claim that only an OAuth client's own token carries, such as `client_id` on Keycloak. A token with this claim is matched by the claim's value on the user's `name`. Set it together with the archetypes. |
| `MIDPOINT_MCP_OIDC_CLIENT_ARCHETYPES` | unset | Comma-separated archetype oids. A client's own token only runs as a midPoint user that holds one of them, for the `externalId` match too. |
| `MIDPOINT_MCP_ANONYMOUS_DISCOVERY` | `false` | `true` lets a caller without a token open a session, list tools and read the view templates. Every tool call still needs a token. See [Let a gateway list tools before sign-in](#let-a-gateway-list-tools-before-sign-in). |

The server refuses to start, with a message that names the setting to fix, when:

- a required variable is missing,
- only one of the issuer and the audience is set,
- only one of the two `MIDPOINT_MCP_OIDC_CLIENT_*` settings is set,
- the discovery URL is set without the issuer, carries a user name or password, or
  is not an absolute http or https URL,
- the correlation attribute or an archetype oid holds characters that are not safe
  in a midPoint query,
- the settings file cannot be read or fails validation.

### The settings file

`MIDPOINT_MCP_CONFIG` points at a JSON file of non-secret settings for things the
server cannot guess, such as how your deployment models org structure. Credentials
are never read from it. Every key is optional. Keys that start with `//` are
comments and are ignored. Any other unknown key stops the server at startup, so a
typo cannot silently keep a default.
[examples/midpoint-mcp.config.json](examples/midpoint-mcp.config.json) is an
annotated copy with every default.

| Key | Default | What it does |
| --- | --- | --- |
| `identity.credentialIsShared` | `false` | `true` declares `MIDPOINT_USERNAME` a shared technical account. In personal mode the self-scoped tools then refuse instead of answering for that account: the three team tools, `list_my_requests`, `list_work_items`, `decide_work_item` and `cancel_request`. |
| `team.orgSource` | `parentOrgRef` | Where a user's org links come from: `parentOrgRef` (midPoint's computed membership), `assignment` (org assignments only), `fallback` (`parentOrgRef`, then assignments when that is empty) or `both`. |
| `team.managerRelation` | `manager` | The relation local part that marks an org manager. Write `manager`, not `org:manager`. |
| `team.memberRelation` | `default` | The relation local part that marks plain membership, used when searching an org for members. |
| `team.orgOids`, `team.orgNames` | empty, meaning all orgs | Which of the user's orgs count as their team. A match on either list counts, and names match in any letter case. |
| `requests.requireRequestable` | `true` | `request_role` refuses roles that midPoint does not flag `requestable`. Without it, a request for such a role would grant it outright, because midPoint opens an approval case only where a policy matches. |
| `requests.formItems` | empty, meaning no form | Ordered qualified names, written `{namespace}localName`, of single-valued assignment extension items that `request_role` offers as form fields. Supported types are `string`, `boolean`, `int`, `date` and `dateTime`. |
| `requests.justificationItem` | unset | The qualified name of the assignment extension item that holds a requester's reason. `list_work_items` and `get_case` then show that reason. |

The server reads the definitions of `requests.formItems` once at startup, as its own
account and without impersonation, from `GET /ws/rest/schemas` and `GET /ws/schema`.
That account then needs model read on `SchemaType` plus `rest-3#getExtensionSchema`
and `model-3#getExtensionSchema`. Restart the server after a schema change. An item
that is missing, multi-valued or of an unsupported type is skipped with a warning. A
duplicate local name or a failed schema read stops startup.

## Connect a client or run it in a container

### VS Code

Claude Desktop is shown in [Personal mode in three steps](#personal-mode-in-three-steps).
VS Code uses the same shape in `.vscode/mcp.json` or your user-level `mcp.json`.
Keep a file that holds a password out of version control.

```json
{
  "servers": {
    "midpoint": {
      "command": "<install-dir>/midpoint-mcp-server",
      "env": {
        "MIDPOINT_URL": "https://midpoint.example.com/midpoint",
        "MIDPOINT_USERNAME": "<your-midpoint-login>",
        "MIDPOINT_PASSWORD": "<your-midpoint-password>"
      }
    }
  }
}
```

### Docker

Each release publishes an image for linux/amd64 and linux/arm64 as
`ghcr.io/strazahq/midpoint-mcp-server`. Its tags are the release version without
the `v`, such as `1.2.3`, the minor version, such as `1.2`, and `latest`.
[Check a release](#check-a-release) shows how to verify its signature. To build
the image yourself instead:

```sh
docker build -t midpoint-mcp-server .
```

The image is `scratch` with only the static binary and CA certificates, and it runs
as the unprivileged user 65534. A `-e NAME` with no value passes that variable from
your shell, which keeps the password off the command line. The examples below use
the published image, where `<version>` is a release version such as `1.2.3`. For an
image you built yourself, use `midpoint-mcp-server` in its place.

Personal mode over stdio needs `-i` to keep stdin open:

```sh
docker run --rm -i \
  -e MIDPOINT_URL=https://midpoint.example.com/midpoint \
  -e MIDPOINT_USERNAME \
  -e MIDPOINT_PASSWORD \
  ghcr.io/strazahq/midpoint-mcp-server:<version>
```

Shared mode over HTTP passes the flags after the image name:

```sh
docker run --rm -p 3001:3001 \
  -e MIDPOINT_URL=https://midpoint.example.com/midpoint \
  -e MIDPOINT_USERNAME \
  -e MIDPOINT_PASSWORD \
  -e MIDPOINT_MCP_OIDC_ISSUER=https://idp.example.com/realms/corp \
  -e MIDPOINT_MCP_OIDC_AUDIENCE=midpoint-mcp \
  ghcr.io/strazahq/midpoint-mcp-server:<version> --http 0.0.0.0:3001
```

If the container reaches the identity provider under another host name than the
issuer in the tokens, set `MIDPOINT_MCP_OIDC_DISCOVERY_URL` to the discovery
document's address on that network. To use a settings file, mount it and point
`MIDPOINT_MCP_CONFIG` at it, for example
`-v "$PWD/midpoint-mcp.config.json:/config.json:ro" -e MIDPOINT_MCP_CONFIG=/config.json`.
The file must be readable by user 65534. Personal mode over HTTP can only bind a
loopback address, so inside a container it needs host networking (`--network host`).

### Kubernetes

[deploy/kubernetes/README.md](deploy/kubernetes/README.md) runs shared mode on
Kubernetes with plain manifests. They run one hardened replica that reads its
settings from a Secret named `midpoint-mcp-server`, which you create, and include
an example Ingress that terminates TLS. Each release also attaches `midpoint-mcp-server-kubernetes.yaml`,
the same manifests with the image pinned to that release's version.

### HTTP transport

`--http <address>` serves the streamable HTTP transport at the path `/mcp` instead
of stdio:

```sh
midpoint-mcp-server --http :3001   # serves http://127.0.0.1:3001/mcp
```

An address without a host binds `127.0.0.1`, and a bare port such as `3001` works
too. In personal mode the server refuses to start on an address that is not a
loopback address or `localhost`. Without per-request authentication, anyone who
reached it would act as the configured login, and no flag overrides this. In
shared mode every request needs a valid token, so any address is allowed.

The server speaks plain HTTP and has no TLS settings. Put a TLS-terminating proxy
in front of it whenever it listens beyond the local machine, so access tokens never
travel in clear text.

### Let a gateway list tools before sign-in

Some gateways and catalogs list a server's tools before any user has signed in. In
shared mode that request normally gets `401`. Setting
`MIDPOINT_MCP_ANONYMOUS_DISCOVERY=true` lets a caller without a token use exactly
these methods: `initialize`, `notifications/initialized`, `ping`, `tools/list`,
`resources/list`, and `resources/read` for a `ui://midpoint/` address.

A tokenless caller gets only static content: tool names, descriptions, input
schemas and the view templates, the same for everyone. None of these methods
reaches midPoint. These rules apply:

- A request with any `Authorization` header is always verified, whatever it asks
  for. Verification is what binds a session to a user.
- A JSON-RPC batch goes without a token only when every message in it is one of the
  methods above. One `tools/call` in the batch makes the whole request need a token.
- A body that does not parse, or is larger than 64 KiB, needs a token.
- A session opened without a token still accepts a later `tools/call` with a token,
  and that call runs as the token's user.
- The `ping` method above is the protocol ping. The `ping` tool, which calls
  midPoint, is reached through `tools/call` and needs a token.

The setting is off by default and does nothing outside shared mode. When it is on,
the startup log line says `anonymous discovery ON`. It is the only part of the
server that a network caller can reach without a token, so turn it on only when a
gateway needs it.

## Use the interactive views

Hosts that render [MCP Apps](https://github.com/modelcontextprotocol/ext-apps)
views get four interactive screens, embedded in the binary.
[docs/ui-contract.md](docs/ui-contract.md) specifies them.

| View | Resource | Opened by |
| --- | --- | --- |
| Approval inbox: open work items assigned to you, with context, to approve or reject | `ui://midpoint/approval-inbox` | `list_work_items`, `decide_work_item` |
| Get access: requestable roles for you or a direct report, with a request button | `ui://midpoint/request-access` | `list_requestable_roles`, `request_role` |
| My requests: the approval cases you started, where each stands, and withdrawing one | `ui://midpoint/my-requests` | `list_my_requests`, `get_case`, `cancel_request` |
| My team's access: your team and each person's access, and removing a direct report's role | `ui://midpoint/access-review` | `list_my_team`, `get_user_assignments`, `unassign_role` |

A session counts as a UI session when its client advertises the
`io.modelcontextprotocol/ui` extension with the `text/html;profile=mcp-app` MIME
type. Only a UI session sees `_meta.ui` on tools and the `ui://midpoint/*` resources
in `resources/list`. In a UI session, tools that no view uses are marked as visible
to the model only. Any other session's tool list is unchanged. A view document can
be read in any session.

The results of the twelve tools that views render or call carry three fields in
every session: `tool`, `acting`, which is the identity midPoint ran the call as, and
`server`, which reports the write gate, features and versions. They are the ten
tools in the table above, plus `list_my_managers` and `whoami`.

## Find the right tool

Every tool runs as the identity described in [Choose how to run it](#choose-how-to-run-it),
so midPoint only returns and changes what that identity may see and change. Tools
marked "Write gate" return a dry-run preview of the exact request until
`MIDPOINT_MCP_ALLOW_WRITES=true`. The other tools only read. List tools return 20
results by default and at most 100.

### Identity

| Tool | What it does |
| --- | --- |
| `ping` | Checks the connection and reports the identity midPoint authenticated. |
| `whoami` | Reports the identity midPoint acts as, the mode (`personal` or `resource-server`), whether the call is impersonated, and that identity's org links. Call it first when a `list_my_*` tool comes back empty unexpectedly. |

### Users, roles and resources

| Tool | What it does |
| --- | --- |
| `search_users` | Finds users by a substring of name, full name or email, or by exact oid. |
| `get_user` | Reads one user by oid. |
| `get_user_assignments` | Lists a user's direct assignments and effective role membership, each marked direct or inherited. |
| `list_roles`, `get_role` | Lists roles, or reads one role by oid. |
| `list_resources`, `get_resource` | Lists connected systems, or reads one by oid with its connection status where midPoint reports it. |

### Requests and approvals

| Tool | What it does |
| --- | --- |
| `list_requestable_roles` | Lists the roles flagged `requestable` that the caller may see. `forUser` lists what a report could get but does not hold yet. `query` filters by name, display name or description, up to 100 characters. |
| `request_role` | Requests a role for the caller or a report, with optional `validFrom`, `validTo` and form `fields`. By default it refuses roles not flagged `requestable`. The result says `GRANTED` when no approval policy matched and midPoint applied the role at once. Write gate. |
| `list_my_requests` | Lists the approval cases the caller started. |
| `cancel_request` | Withdraws the caller's own open request, after checking its requester and state. midPoint must allow the caller to cancel the case. Write gate. |
| `list_work_items` | Lists the caller's approval inbox. |
| `get_case` | Reads an approval case with its work items. |
| `decide_work_item` | Approves or rejects a work item in the caller's inbox, with an optional comment, and reports the outcome midPoint recorded. It refuses an item that is not open or not the caller's, even with writes off. Write gate. |

### Managers and teams

| Tool | What it does |
| --- | --- |
| `list_my_team` | Lists the caller's direct reports: the members of the orgs the caller manages. |
| `list_my_managers` | Lists the managers of the orgs the caller belongs to. |
| `list_my_teammates` | Lists the other members of the orgs the caller belongs to. |

Team results name the identity they answered for and the org links they used, so an
empty result shows why it is empty. The `team.*` keys of the settings file decide
which org links count. A manager who is not a superuser sees reports only with a
read authorization over them, because being an org manager does not grant it.
midPoint's usual answer is an authorization whose object selector uses
`orgRelation` with `subjectRelation` set to the manager relation.

### Reports

| Tool | What it does |
| --- | --- |
| `search_objects` | Searches users, roles, orgs, services, shadows or resources with a midPoint query-language filter, for ad hoc reports such as orphaned accounts. |
| `search_audit` | Queries the audit trail over a time range, the last 30 days by default, by initiator, target, event type, outcome or channel. midPoint 4.10 has no REST audit endpoint, so this runs a server-side script. It needs script-execution authorization and does not work in shared mode. |

### Administration

| Tool | What it does |
| --- | --- |
| `create_user` | Creates a user. Write gate. |
| `enable_user`, `disable_user` | Enables or disables a user. Write gate. |
| `assign_role`, `unassign_role` | Adds or removes a user's role assignment. Use `assign_role` for a deliberate grant. Write gate. |
| `recompute_user` | Makes midPoint recompute a user, so it applies its policies again and propagates the changes. Write gate. |

## Build and test

- `go test ./...` runs the unit tests against recorded REST responses. It needs no
  midPoint.
- `go test -tags=integration ./...` adds live tests against a real midPoint, such as
  a 4.10 container. They read the `MIDPOINT_*` variables and skip when those are
  unset.
- `node test/views/run.mjs` checks a view document in headless Chromium. It needs
  Node 18 or later and Playwright, as [test/views/README.md](test/views/README.md)
  explains.
- CI runs `gofmt`, `go vet` and the unit tests on pushes to `main` and on pull
  requests. When `deploy/kubernetes` changes, another workflow renders the
  manifests and validates them with kubeconform. Pushing a tag such as `v1.2.3`
  runs the same checks and publishes the release described in
  [Check a release](#check-a-release). The release is refused unless
  `CHANGELOG.md` has a non-empty section headed `## [1.2.3]`, and that section
  becomes the release notes.

## Versions

The server's own version is its git tag, written `vMAJOR.MINOR.PATCH`, and it
follows [semantic versioning](https://semver.org). `--version` prints it, and the
server reports it to MCP clients as its version in the `initialize` result and in
the `server` field of the view tools' results. A binary built from source without
the release's build flags reports `0.0.1-dev`, and an image built without
`--build-arg VERSION=<tag>` reports `docker`. Image tags leave out the `v`, so
release `v1.2.3` is the image tag `1.2.3`.

The MCP protocol revision is separate from the server's version. It is named by a
date, such as `2025-06-18`, and the official MCP Go SDK negotiates it with each
client during `initialize`. The server uses go-sdk v1.6.1, which supports the
revisions from `2024-11-05` to `2025-11-25`. It answers with the client's revision
when it supports that one, and with `2025-11-25` otherwise.

## License

Apache-2.0. See [LICENSE](LICENSE).
