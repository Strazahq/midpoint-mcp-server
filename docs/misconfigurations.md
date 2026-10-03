# Avoid the misconfigurations that break shared mode

These are the mistakes that let one caller act as another in midPoint, that widen
what a stolen credential can do, or that lock everyone out. They come from reading
the server's code and the identity providers' documentation. They are ordered from
most to least dangerous. The first nine weaken security, and the last four lock
people out.

Each entry says what the mistake is, what goes wrong, how to spot it, and how to fix
it. Quoted texts are the exact strings the server or its libraries produce, so you
can search for them. A refused token gets HTTP 401 with the reason in the response
body. The server does not log refused tokens, so read the reason where your MCP
gateway or client shows the response. [Identity providers](identity-providers.md)
describes the correct setups, and [Authorization](authorization.md) describes the
service account.

## Give the server an audience that no other application receives

The mistake is a value in `MIDPOINT_MCP_OIDC_AUDIENCE` that tokens for other
applications also carry. On Keycloak this happens when the Audience mapper sits in
a realm default client scope or in a shared scope such as `roles` or `basic`. It
also happens when the audience is the id of a client whose roles many tokens carry,
such as Keycloak's built-in `account` client, because Keycloak's Audience Resolve
mapper adds a client's id to `aud` whenever a token carries one of that client's
roles. On Entra a related gap opens when the server's enterprise application has
Assignment required set to No. Microsoft documents that any application can then
obtain an access token for the API.

The server checks only that `aud` contains the configured value. It does not check
which client the token was issued to. Any application that receives a person's
token with that audience can present it to this server and act in midPoint as that
person, with all of that person's midPoint rights. So can anyone who steals such a
token from that application.

To spot it, decode an access token that an unrelated application in the same realm
or tenant received. If its `aud` contains the server's audience, the value is not
exclusive. On Keycloak, list the client scopes that contain an Audience mapper for
the value and check where they are linked. On Entra, check Assignment required on
the server's enterprise application.

The fix on Keycloak is a dedicated client with no roles and a dedicated client
scope linked only to the clients that should call the server. The fix on Entra is
Assignment required set to Yes, with only the intended groups and applications
assigned. Never reuse another application's audience for this server.

## Keep people's archetypes out of the client archetype list

The mistake is an oid in `MIDPOINT_MCP_OIDC_CLIENT_ARCHETYPES` that belongs to an
archetype people hold, such as an employee archetype.

A client's own token is matched on `name`, and the archetype list is the only check
that keeps it away from people. Whoever can create a client at the identity
provider can give it a person's name, for example `alice`. If people's archetype is
in the list, alice's midPoint user passes the check and the client runs as alice.
On Keycloak, anyone allowed to manage clients in the realm can do this.

To spot it, search midPoint users with `archetypeRef matches (oid = "<listed oid>")`
for each listed oid. The results must contain agents only.

The fix is a dedicated agent archetype that no person holds, listed alone. Govern
who can be assigned that archetype as carefully as an administrator role.

## Choose a client claim that only client tokens carry and that names the client

The mistake has two forms. In the first, `MIDPOINT_MCP_OIDC_CLIENT_CORRELATION_CLAIM`
names a claim that every token carries, such as `azp` on Keycloak and Entra or
`appid` in Entra version 1 tokens. In the second, it names a claim whose value is
the same for every client, such as Entra's `idtyp`, whose value is `app` on every
app-only token.

In the first form, every person's token counts as a client's. Its client claim
holds the id of the MCP gateway or client the person signed in through, and the
server matches that id against `name` on agent users. If an agent user carries that
name and a listed archetype, every person who signs in through that client runs as
that agent. They get the agent's midPoint rights, and midPoint's audit names the
agent instead of the person. If no such user exists, every person is locked out
with `no midPoint user that holds an archetype listed in
MIDPOINT_MCP_OIDC_CLIENT_ARCHETYPES`. In the second form, every client's token
matches the one agent user named after the shared value, so all clients act as one
identity, or none of them works.

To spot it, decode a person's token and an agent's token. The claim must be absent
from the person's token and must hold the agent's own id in the agent's token. Also
check that no other mapper puts a claim with the same name on a person's token, for
example a user attribute mapper.

The fix on Keycloak is `client_id`, which only client credentials tokens carry. On
Entra no claim fits, so leave both client settings empty, as
[Identity providers](identity-providers.md#know-the-limit-for-entra-app-only-tokens)
explains.

## Correlate on a claim that the person cannot change

The mistake is correlating on a claim that its owner, an administrator, or a rename
can change. On Entra that includes the server's default claim,
`preferred_username`, and also `email` and `upn`. An Entra setup that sets only the
issuer and the audience uses `preferred_username` without saying so. On Keycloak it
includes the username when users may edit it, and `email` when users may update
their email.

The server trusts the claim's value and matches it against midPoint. If the value
changes to another person's midPoint value, the token runs as that person. Microsoft
states that `preferred_username` is mutable and must not be used for authorization
decisions, that `email` is not guaranteed to be correct, and that such claims can be
controlled by tenant administrators or sometimes by users. Microsoft also says a
user principal name often changes over a user's lifetime. After such a change the
token matches nobody, or matches whoever holds the new value in midPoint.

To spot it, read `MIDPOINT_MCP_OIDC_CORRELATION_CLAIM`. An empty value means
`preferred_username`.

The fix on Entra is `oid`, matched against an extension item that an inbound
mapping fills from the Entra account's object id. The fix on Keycloak is
`preferred_username` with the realm's Edit username and Email as username settings
turned off and midPoint writing the username, or `sub` matched against an item that
holds the Keycloak user id.

## Keep the correlation attribute unique and out of users' reach

The mistake is a correlation attribute that is not unique in midPoint, such as
`emailAddress`, or one that some authorization lets users or helpdesk staff modify.

If two users hold the same value, the server refuses the token of the person it
belongs to with `ambiguous correlation`. Anyone who can set their own attribute to
another person's value locks that person out the same way. Anyone who can modify the
attribute on other users can do worse. They can write their own claim value onto a
victim's midPoint user and clear it from their own, and their token then runs as
the victim.

To spot it, look for roles that grant `modify` on the attribute, including broad
self-service modify rights and helpdesk roles. Refusals that contain `ambiguous
correlation` show duplicates that already exist.

The fix is `name`, which midPoint keeps unique, or an extension item that only an
inbound mapping writes and that no authorization lets anyone modify.

## Verify TLS between the server and midPoint

The mistake is `MIDPOINT_INSECURE_TLS=true` on a deployment that matters, or a
`MIDPOINT_URL` that starts with `http://`.

The server sends the service account's user name and password with HTTP Basic
authentication on every request. Without verified TLS, anyone on the network path
between the server and midPoint can read them. With those credentials and the
`#proxy` grant, an attacker can impersonate any in-scope user directly at midPoint,
with no token at all.

To spot it, read the two settings. The server's startup line shows the midPoint URL
after `midPoint:`. Insecure TLS is not announced in the log.

The fix is an `https` URL and a certificate chain that the server's host trusts.
Use `MIDPOINT_INSECURE_TLS` only against a development instance.

## Put TLS in front of the server

The mistake is exposing the `--http` port directly on a network.

The server serves plain HTTP, and every request carries a bearer token in the
`Authorization` header. Anyone who can read the traffic can replay the token until
it expires and act in midPoint as its owner.

To spot it, check what can reach the port. The startup line always reads `serving
on http://<address>/mcp`, so the question is whether the port is reachable without
going through a TLS proxy.

The fix is a reverse proxy that terminates TLS, with the server bound to an address
only that proxy reaches. Binding `127.0.0.1` is always allowed when the proxy runs
on the same host.

## Keep the impersonation scope narrow and the service account without rights of its own

The mistake is a `#proxy` selector that is a bare `UserType`, which covers every
user including administrators, or one that lists an administrator archetype. A
related mistake is a service account that is a superuser or holds broad model
rights.

The `#proxy` scope is the last check between a correlated token and the user it runs
as. If any mistake above lands a token on an administrator, a wide scope lets the
request run as that administrator. A superuser service account can impersonate
anyone, because the superuser role's `#all` covers `#proxy` on midPoint 4.10.3. Its
own rights also turn a leaked password into full control of midPoint.

To spot it, call `GET /ws/rest/self` as the service account with
`Switch-To-Principal` set to an administrator's oid. The answer must be 403.

The fix is the rs-service role with an archetype-scoped `#proxy` grant, plus only the
read authorization that correlation needs. Keep the agent archetype inside the
scope too, or agents' tool calls fail at midPoint even though their tokens are
accepted.

## Turn writes on only on purpose

The mistake is `MIDPOINT_MCP_ALLOW_WRITES=true` in a deployment that meant to offer
read-only tools.

With writes on, the write tools change midPoint instead of returning a preview:
`create_user`, `enable_user`, `disable_user`, `assign_role`, `unassign_role`,
`recompute_user`, `request_role`, `decide_work_item` and `cancel_request`. In shared
mode each runs as the caller, so midPoint's authorizations still bound it. But an
assistant acting for the caller can now make every change those rights allow,
approvals included, without a preview for a person to read first.

To spot it, read the startup line. It says `writes: ENABLED` when writes are on and
`writes: disabled (dry-run previews)` when they are off. While writes are off, every
write tool's result starts with `DRY RUN`.

The fix is to leave the setting unset unless the deployment is meant to change
midPoint.

## Let the service account read the users it correlates

The mistake is a service account that holds exactly the role in
`examples/role-mcp-rs-service.xml` and nothing else.

Correlation runs as the service account, without `Switch-To-Principal`. That role
grants no model rights, and with it alone a user search returns an empty list. Every
token is then refused with `invalid token: no midPoint user matches`, and every
person and agent is locked out. This follows from the code and from that role's own
verification notes. It has not yet been fired live with this server.

To spot it, run the correlation check in
[Verify a deployment](authorization.md#verify-a-deployment). An empty list means the
account cannot see the user.

The fix is the read authorization in
[Let the service account find users](authorization.md#let-the-service-account-find-users),
limited to the `#proxy` archetypes and to the items the searches filter on.

## Match the issuer, the discovery URL and the key set

The mistake is a `MIDPOINT_MCP_OIDC_ISSUER` that differs from the `iss` in the
tokens, a `MIDPOINT_MCP_OIDC_DISCOVERY_URL` that serves another provider's document,
or a key set URL the server cannot reach. Common causes are a trailing slash, `http`
instead of `https`, a missing port, a Keycloak hostname that differs from the
address the server uses, and Entra's multi-tenant `common` or `organizations`
authority.

Depending on the cause, the server refuses to start or refuses every token. These
are the texts to search for.

| When | Text |
| --- | --- |
| Startup, discovery at the issuer | `did not match the issuer URL returned by provider` |
| Startup, discovery URL set | `the discovery document at ... names issuer` |
| Startup, discovery URL unreachable | `fetching the discovery document at ... failed` |
| Every request | `invalid token: oidc: id token issued by a different provider` |
| Every request, key set unreachable | `invalid token: failed to verify signature` |

The server fetches the key set on the first request, not at startup. A server that
starts cleanly can therefore still refuse every token.

To spot it, compare the `issuer` field of the discovery document with the `iss` of
a decoded token. They must be identical.

The fix is to copy the issuer from a real token. Set the discovery URL only when the
server reaches the provider at another address, and make sure the `jwks_uri` in the
document is reachable from the server too.

## Ask Entra for version 2 tokens

The mistake is leaving `api.requestedAccessTokenVersion` on the server's Entra
registration at `null` or `1` while the server expects version 2 tokens. Microsoft
documents that `null` means version 1.

A version 1 token has the issuer `https://sts.windows.net/<tenant-id>/`, so the
server refuses it with `invalid token: oidc: id token issued by a different
provider`. Its `aud` can be any of the API's Application ID URIs or its client id,
so it can fail with `invalid token: oidc: expected audience`. It also carries no
`preferred_username` unless that is added as an optional claim. Every person is
locked out.

To spot it, decode a token. `ver` is `1.0` and `iss` starts with
`https://sts.windows.net/`.

The fix is to set `api.requestedAccessTokenVersion` to `2` in the manifest of the
server's registration. The server accepts one issuer, so it cannot take both token
versions at once.

## Do not correlate on Entra's sub

The mistake is `MIDPOINT_MCP_OIDC_CORRELATION_CLAIM=sub` on Entra, or relying on the
server's first correlation step, which matches `sub` against `externalId`.

Microsoft documents Entra's `sub` as pairwise, unique to each application. It is not
the object id that midPoint's Entra connector reads, so no midPoint user holds it,
and every token is refused with `invalid token: no midPoint user matches`. The first
step never matches on a stock midPoint 4.10 either, because its standard user
schema has no `externalId` item.

To spot it, compare a decoded token's `sub` with the user's object id in Entra. They
differ.

The fix is `oid`, matched against an extension item filled from the Entra account's
object id.
