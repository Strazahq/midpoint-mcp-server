# Set up an identity provider for shared mode

Shared mode is one server, reached over HTTP, that serves many people and agents.
Each caller presents its own OAuth bearer token, and every request runs in midPoint
as the midPoint user that token belongs to. The server is in shared mode when both
`MIDPOINT_MCP_OIDC_ISSUER` and `MIDPOINT_MCP_OIDC_AUDIENCE` are set and it is started
with `--http`. One person on their own machine can use personal mode over stdio
instead, which needs no identity provider. The [README](../README.md) describes both
modes.

This guide explains the model, lists the requirements, and then walks through two
complete setups. In both, midPoint is the identity governance system that owns the
users and their roles. In the first, Keycloak is the identity provider. In the
second, Microsoft Entra ID is. [Misconfigurations](misconfigurations.md) lists the
mistakes that let someone act as another person or lock everyone out, and
[Authorization](authorization.md) covers the midPoint service account.

## Understand the four checks on every request

The server is an OAuth resource server. It never signs anyone in and never holds a
client secret. It reads the identity provider's public discovery document and
signing keys, and it answers four questions for every request.

1. Is the token signed by the identity provider you trust? The server reads the
   discovery document at `MIDPOINT_MCP_OIDC_ISSUER` followed by
   `/.well-known/openid-configuration`, or at `MIDPOINT_MCP_OIDC_DISCOVERY_URL` when
   that is set. It checks the token's signature against the keys that document
   points to. The token's `iss` must equal `MIDPOINT_MCP_OIDC_ISSUER` byte for byte,
   and an expired token is refused.
2. Was the token issued for this server? The token's `aud` claim must contain
   `MIDPOINT_MCP_OIDC_AUDIENCE`. The server does not look at `azp` or any other
   client claim for this check. The audience value is the only thing that separates
   tokens meant for this server from tokens meant for other applications.
3. Which midPoint user is it? The server searches midPoint for exactly one user
   that matches the token. This search is called correlation, and the next section
   describes it step by step.
4. What may that user do? Every midPoint call made for the request carries the
   `Switch-To-Principal` header with that user's oid, so midPoint applies that
   user's own authorizations. The service account's `#proxy` authorization limits
   which users can be impersonated at all. The server adds no permission model of
   its own.

A request that fails one of the first three checks gets HTTP 401 with a plain-text
body that starts with `invalid token:` and names the reason. A request without a
token gets 401 with the body `no bearer token`. The server writes nothing to its own
log for a refused token, so look for the reason in the response that your MCP
gateway or client receives. The [refusal table](#read-a-refusal) lists the common
reasons.

```
person or agent --signs in--> identity provider --signed access token--> MCP gateway or client
                                                                                |
                                                             Authorization: Bearer <token>
                                                                                v
midPoint <--Basic auth as the service account, Switch-To-Principal: <oid>-- this server
                                    checks signature, issuer, expiry and audience,
                                    then correlates the token to one midPoint user
```

### How correlation finds the midPoint user

Correlation runs as the service account itself, without the `Switch-To-Principal`
header, because the server does not yet know whom to impersonate. It tries two
searches in order and stops at the first one that finds exactly one user.

1. The token's `sub` against the user's `externalId`. midPoint 4.10's standard user
   schema has no item called `externalId`, so on a stock midPoint this search finds
   nobody. An error or more than one match in this step is not fatal. In every such
   case the server moves on to step 2.
2. The value of the correlation claim against the correlation attribute. The claim
   is `MIDPOINT_MCP_OIDC_CORRELATION_CLAIM`, by default `preferred_username`. The
   attribute is `MIDPOINT_MCP_OIDC_CORRELATION_ATTRIBUTE`, by default `name`. A
   string claim is used as it is. A numeric claim is turned into text, and a whole
   number is written without a decimal point, so an employee number matches
   midPoint's string value. If two or more users match, the token is refused instead of
   guessing. If none match, or the token does not carry the claim, the token is
   refused.

A token that an OAuth client obtains for itself, as an agent does with the client
credentials grant, can follow a separate path. It does so only when
`MIDPOINT_MCP_OIDC_CLIENT_CORRELATION_CLAIM` is set and the token carries that
claim. Three things then change.

1. The client claim's value replaces the correlation claim's value. It is always
   matched against the user's `name`, whatever
   `MIDPOINT_MCP_OIDC_CORRELATION_ATTRIBUTE` says.
2. Every search for that token, the `externalId` search included, also requires the
   user to hold one of the archetypes listed in `MIDPOINT_MCP_OIDC_CLIENT_ARCHETYPES`.
   A client that someone named like a person therefore cannot run as that person,
   because a person does not hold an agent archetype.
3. A token whose client claim is present but empty, or is not a string or a number,
   is refused. It is not retried as a person's token.

The presence of the client claim is the only thing that marks a token as a
client's. The claim must therefore be one that a person's token never carries, and
its value must name the client. A token without the claim is treated as a person's
token and is not limited by archetype.

Because both searches run as the service account, the service account must be able
to search and read the users it may correlate, on the attributes the searches filter
on. [Authorization](authorization.md#let-the-service-account-find-users) shows the
authorization this needs.

## Check the requirements

The identity provider must do these things.

1. Issue access tokens as signed JWTs that carry `exp`. The server accepts RS256,
   RS384, RS512, ES256, ES384, ES512, PS256, PS384, PS512 and EdDSA signatures.
2. Publish an OpenID Connect discovery document whose `issuer` equals the `iss` in
   its tokens, with a key set URL that the server can reach.
3. Put a value in `aud` that only tokens meant for this server carry.
4. Put a stable claim in each person's token that midPoint also holds for that
   person.
5. For agents, put a claim on a client's own token that a person's token never
   carries and whose value names the client.

midPoint must provide these things.

1. A service account used by this server and nothing else. It holds the role in
   [`examples/role-mcp-rs-service.xml`](../examples/role-mcp-rs-service.xml) plus a
   read authorization for correlation, as [Authorization](authorization.md)
   explains.
2. Every person and agent as a midPoint user that holds the correlation value in an
   attribute that is unique and that only midPoint's own mappings write.
3. Archetypes that separate people from agents, all inside the scope of the service
   account's `#proxy` authorization.
4. Authorizations of their own for the users the server impersonates, for example
   midPoint's built-in End user role. A user with no authorizations cannot even
   read their own `/self`.

The network must provide these things.

1. A reverse proxy that terminates TLS in front of the server. The server itself
   serves plain HTTP at `/mcp`.
2. Outbound HTTPS from the server to the identity provider's discovery document and
   key set, and to midPoint.

These are the settings the server reads. A boolean setting is on only when its
value is `true`, in any letter case.

| Setting | Default | What it does |
| --- | --- | --- |
| `MIDPOINT_URL` | required | The midPoint deployment root, for example `https://midpoint.example.com/midpoint`. The server appends `/ws/rest`. |
| `MIDPOINT_USERNAME` | required | The service account's user name, sent with HTTP Basic authentication. |
| `MIDPOINT_PASSWORD` | required | The service account's password. It never appears in an error message. |
| `MIDPOINT_MCP_OIDC_ISSUER` | empty | The issuer. It must equal the token's `iss` exactly. Together with the audience it turns on shared mode. Setting one of the two without the other stops startup. |
| `MIDPOINT_MCP_OIDC_AUDIENCE` | empty | The value that must appear in the token's `aud`. |
| `MIDPOINT_MCP_OIDC_DISCOVERY_URL` | empty | The full URL of the discovery document, used exactly as given. Use it when the server reaches the identity provider at another address than the one in its tokens. The document must still name the issuer exactly. It requires the issuer, must be an absolute http or https URL, and must not carry a user name or password. |
| `MIDPOINT_MCP_OIDC_CORRELATION_CLAIM` | `preferred_username` | The token claim matched against the correlation attribute. |
| `MIDPOINT_MCP_OIDC_CORRELATION_ATTRIBUTE` | `name` | The midPoint user attribute that holds the claim's value. It must start with a letter and contain only letters, digits and single `/` separators, for example `extension/entraObjectId`. |
| `MIDPOINT_MCP_OIDC_CLIENT_CORRELATION_CLAIM` | empty | A claim that only a client's own token carries. Setting it without the archetype list stops startup. |
| `MIDPOINT_MCP_OIDC_CLIENT_ARCHETYPES` | empty | Comma-separated oids of the archetypes a client's token may run as. Each oid may contain only letters, digits and `-`. Setting it without the client claim stops startup. |
| `MIDPOINT_MCP_ANONYMOUS_DISCOVERY` | `false` | When `true` in shared mode, a caller without a token may call `initialize`, `notifications/initialized`, `ping`, `tools/list`, `resources/list`, and `resources/read` for the `ui://midpoint/` view templates. `tools/call` always needs a token. |
| `MIDPOINT_MCP_ALLOW_WRITES` | `false` | When `true`, write tools change midPoint. Otherwise they return a preview. |
| `MIDPOINT_INSECURE_TLS` | `false` | When `true`, the server skips certificate checks toward midPoint. Use it only against a development instance. |

The server refuses to bind a non-loopback address unless both the issuer and the
audience are set. This keeps midPoint data unreachable without a validated token.

## Walk through Keycloak with midPoint as the governance system

In this setup midPoint owns every person and agent and their roles. midPoint
provisions each person as a Keycloak user whose username is the person's midPoint
`name`. midPoint provisions each agent as a Keycloak client whose client id is the
agent's midPoint `name`. People sign in through an MCP gateway or client. Agents get
their own tokens with the client credentials grant.

The server correlates a person's `preferred_username`, which carries the Keycloak
username, against midPoint's `name`. midPoint keeps `name` unique, and midPoint is
the system that writes it to Keycloak. An agent's token carries `client_id`, which
the server matches against `name` on agent users only.

### Configure Keycloak

1. Create a client that stands for this server, for example `midpoint-mcp`. Turn off
   every flow on it, because nothing signs in with it. Do not give it client roles.
   Keycloak's Audience Resolve mapper adds a client's id to `aud` whenever a token
   carries one of that client's roles, which would spread the audience to tokens
   you did not plan for.
2. Create a client scope, for example `midpoint-mcp-audience`. Add a mapper of type
   Audience to it, set Included Client Audience to `midpoint-mcp`, and turn on Add
   to access token. Keycloak stops adding a client audience once that client is
   disabled, so disabling `midpoint-mcp` cuts off new tokens for this server.
3. Link that client scope as a default client scope only to the clients that should
   call this server: the client people sign in through, and each agent client. Do
   not make it a realm default client scope, and do not add the mapper to a shared
   scope such as `roles` or `basic`. Either would put the audience on tokens for
   every client in the realm.
4. In the realm's login settings, keep Edit username and Email as username turned
   off. Keycloak's guide notes that these two settings change how the username
   attribute behaves. Leaving them off keeps the username at the value midPoint
   wrote.
5. For each agent, midPoint creates a client with Client authentication and Service
   accounts roles turned on and the standard flow turned off. Keycloak adds the
   `client_id` claim to the client credentials tokens of such a client. Since
   Keycloak 26.1 it does so through the `service_account` client scope, which it
   assigns when service accounts are turned on. A person's token does not carry
   `client_id`. This was verified on Keycloak 26.
6. Do not give these clients lightweight access tokens. A lightweight access token
   keeps `aud`, `preferred_username` and `sub` only if their mappers have Add to
   lightweight access token turned on.
7. Decode a real token of each kind before you start the server. A person's token
   must have `midpoint-mcp` in `aud`, the person's midPoint name in
   `preferred_username`, and no `client_id`. An agent's token must have
   `midpoint-mcp` in `aud` and the agent's midPoint name in `client_id`.

### Configure midPoint

1. Define a Keycloak resource with a connector that manages users and clients
   through the Keycloak Admin REST API. Add a strong outbound mapping from the
   user's `name` to the Keycloak username.
2. Give people an archetype such as employee, and give agents a separate archetype
   such as ai-agent. For agent users, add a strong outbound mapping from `name` to
   the Keycloak client id, so the client id always equals the agent's midPoint name.
3. Import [`examples/role-mcp-rs-service.xml`](../examples/role-mcp-rs-service.xml)
   with your archetype oids in its `#proxy` selector. Add the read authorization
   from [Authorization](authorization.md#let-the-service-account-find-users) with
   the items `name` and `archetypeRef`, and assign both to the service account.
4. Assign the End user role, or your own equivalent, to people and agents, so each
   has authorizations of its own when the server impersonates it.

Correlating on `sub` instead survives a username change, at the cost of one more
midPoint attribute. Keycloak fills `sub` through the Subject mapper of its default
`basic` client scope. If your connector exposes a Keycloak user identifier that
equals the `sub` in a decoded token, map it inbound to a single-valued extension
item such as `extension/keycloakId`. Then set
`MIDPOINT_MCP_OIDC_CORRELATION_CLAIM=sub` and
`MIDPOINT_MCP_OIDC_CORRELATION_ATTRIBUTE=extension/keycloakId`, and add that item to
the service account's read authorization.

### Start the server

[`examples/keycloak.env`](../examples/keycloak.env) holds every setting except the
password, each with a comment. The settings that matter for Keycloak are these.

| Setting | Value | Why |
| --- | --- | --- |
| `MIDPOINT_MCP_OIDC_ISSUER` | `https://<keycloak-host>/realms/<realm>` | Keycloak's issuer for the realm, as it appears in `iss`. |
| `MIDPOINT_MCP_OIDC_AUDIENCE` | `midpoint-mcp` | The audience that the client scope from step 2 adds. |
| `MIDPOINT_MCP_OIDC_CORRELATION_CLAIM` | `preferred_username` | It carries the Keycloak username that midPoint wrote. |
| `MIDPOINT_MCP_OIDC_CORRELATION_ATTRIBUTE` | `name` | midPoint keeps it unique. |
| `MIDPOINT_MCP_OIDC_CLIENT_CORRELATION_CLAIM` | `client_id` | Only client credentials tokens carry it. |
| `MIDPOINT_MCP_OIDC_CLIENT_ARCHETYPES` | `<agent-archetype-oid>` | Agent tokens may only run as agent users. |

The issuer must match `iss` exactly, including scheme, host and port. Keycloak
derives `iss` from its configured hostname. If the server cannot reach Keycloak at
that hostname, for example inside a container network, set
`MIDPOINT_MCP_OIDC_DISCOVERY_URL` to
`http://<internal-host>/realms/<realm>/.well-known/openid-configuration`. The
document must still name the public issuer. The server fetches the signing keys from
the `jwks_uri` in that document on the first request, not at startup, so that URL
must be reachable too. Either let the server resolve the public hostname, or use
Keycloak's `hostname-backchannel-dynamic` option, which resolves backchannel
endpoints such as the key set URL from the incoming request.

## Walk through Microsoft Entra ID with midPoint as the governance system

In this setup midPoint owns every person and their roles. midPoint creates their
Entra accounts or correlates existing ones, and stores each account's Entra object
id on the midPoint user. The server correlates the token's `oid` claim against that
stored value.

The `oid` claim fits because Microsoft documents it as the immutable identifier of
the user or service principal, the same for every application in the tenant.
Microsoft Graph returns the same value as the user's `id`. The other candidates do
not fit. Entra's `sub` is pairwise, unique to each application, so it differs from
the value Graph returns. Microsoft says `preferred_username` is mutable and must not
be used for authorization decisions, and that `email` is not guaranteed to be
correct. The server's default claim is `preferred_username`, so an Entra setup must
set the correlation claim explicitly.

### Configure Entra

1. Register an application for this server, for example "midPoint MCP", for
   accounts in your organization only. It needs no redirect URI and no client
   secret.
2. In its manifest, set `api.requestedAccessTokenVersion` to `2`. This setting on
   the API's registration decides the access token version, whatever endpoint or
   client requests the token. The default `null` means version 1, whose issuer and
   audience have a different form.
3. Under Expose an API, set the Application ID URI to the recommended
   `api://<server-client-id>` and add a scope, for example `access`.
4. In Enterprise applications, open the server's entry, set Assignment required to
   Yes, and assign the groups of people who may use the server. With the setting on
   No, every user can sign in and any application can obtain a token for this API.
   Microsoft notes that users with the Global Administrator role can sign in
   regardless of this setting, so their own midPoint users decide what they can do.
5. In the registration of the MCP gateway or client that people sign in through,
   add the delegated permission for the scope from step 3. The client requests
   `api://<server-client-id>/access` together with `openid profile`. Microsoft's
   claims reference says that a client receives `oid` for users when it requests
   the `profile` scope.
6. Decode a real token before you start the server. It must have `ver` `2.0`, `iss`
   `https://login.microsoftonline.com/<tenant-id>/v2.0`, `aud` equal to the server
   registration's client id, which is a GUID, and an `oid`.

In a version 2 access token, `aud` is always the API's client id, never the
Application ID URI.

### Configure midPoint

1. Add a single-valued string item to your user schema extension, for example
   `entraObjectId`.
2. Define an Entra resource with the Microsoft Graph API connector that Evolveum
   publishes for midPoint. That connector reads accounts through Microsoft Graph,
   uses the Graph `id` as the account's unique identifier, and also exposes it as
   the read-only attribute `id`.
3. Either let midPoint create the Entra accounts, in which case Entra assigns the id
   and midPoint reads it back, or correlate existing accounts with a correlation
   rule, for example on `userPrincipalName` or an employee number.
4. Add a strong inbound mapping from the account's `id` to the extension item, so
   midPoint writes the value only from the linked account. Adapt this fragment to
   your resource definition.

   ```xml
   <attribute>
       <ref>ri:id</ref>
       <inbound>
           <strength>strong</strength>
           <target>
               <path>extension/entraObjectId</path>
           </target>
       </inbound>
   </attribute>
   ```

5. Check that no authorization lets users modify `extension/entraObjectId`, on
   themselves or on anyone else. Broad self-service modify rights count.
6. Import [`examples/role-mcp-rs-service.xml`](../examples/role-mcp-rs-service.xml)
   with your archetype oids. Add the read authorization from
   [Authorization](authorization.md#let-the-service-account-find-users) with the
   items `name`, `archetypeRef` and `extension/entraObjectId`, and assign both to
   the service account.
7. Assign the End user role, or your own equivalent, to the people.

### Start the server

[`examples/entra.env`](../examples/entra.env) holds every setting except the
password, each with a comment. The settings that matter for Entra are these.

| Setting | Value | Why |
| --- | --- | --- |
| `MIDPOINT_MCP_OIDC_ISSUER` | `https://login.microsoftonline.com/<tenant-id>/v2.0` | The version 2 issuer of your tenant. |
| `MIDPOINT_MCP_OIDC_AUDIENCE` | `<server-client-id>` | Version 2 tokens carry the API's client id in `aud`. |
| `MIDPOINT_MCP_OIDC_CORRELATION_CLAIM` | `oid` | It is immutable and equals the `id` that Graph returns. |
| `MIDPOINT_MCP_OIDC_CORRELATION_ATTRIBUTE` | `extension/entraObjectId` | The item the inbound mapping fills. |
| `MIDPOINT_MCP_OIDC_CLIENT_CORRELATION_CLAIM` | empty | Entra has no claim that fits, as the next section explains. |
| `MIDPOINT_MCP_OIDC_CLIENT_ARCHETYPES` | empty | It must stay empty while the client claim is empty. |

Use the tenant-specific issuer. The multi-tenant `common` and `organizations`
discovery documents publish the issuer as a template,
`https://login.microsoftonline.com/{tenantid}/v2.0`, which never equals the
configured issuer, so the server would refuse to start.

### Know the limit for Entra app-only tokens

The server cannot give each Entra app-only client its own identity through the
client correlation settings. The claims that name the calling application, `azp` in
version 2 tokens and `appid` in version 1 tokens, are present on user tokens too.
Setting either one as the client claim would mark every person's token as a
client's. By default the optional `idtyp` claim is present only on app-only tokens,
but its value is `app` for every application, and the server uses the client
claim's value as the name to match. Every app-only token would then match the same
midPoint user.

The safe configuration is the one above, with both client settings empty. An
app-only token then takes the person path and is matched on its `oid`, which for an
app-only token is the object id of the calling service principal. No person's
midPoint user holds that value, so the token matches nobody and is refused. If one
agent must act through an app-only token, store its service principal's object id
in the same extension item on the agent's own midPoint user, and keep that user's
archetype inside the `#proxy` scope. The archetype check does not run on this path.
That is acceptable only because Entra assigns object ids itself, so an application
cannot choose one that equals a person's.

## Use another OpenID Connect provider

The same pattern works with any provider.

1. Register this server as an API or resource with an audience of its own, and let
   only the clients that should call it request tokens for that audience.
2. Set `MIDPOINT_MCP_OIDC_ISSUER` to the `issuer` field of the provider's discovery
   document, and `MIDPOINT_MCP_OIDC_AUDIENCE` to the audience.
3. Pick an immutable claim that midPoint can hold for each user, and set the
   correlation claim and attribute to it.
4. Set the client correlation claim only if the provider has a claim that appears
   on client credentials tokens alone and whose value names the client. A claim
   that every token carries, such as `azp`, does not qualify.

## Read a refusal

| Where it shows | Text to search for | Cause |
| --- | --- | --- |
| Startup | `refusing to bind --http to non-loopback host` | Shared mode is not configured. Set both the issuer and the audience. |
| Startup | `MIDPOINT_MCP_OIDC_ISSUER and MIDPOINT_MCP_OIDC_AUDIENCE must be set together` | Only one of the two is set. |
| Startup | `did not match the issuer URL returned by provider` | The discovery document at the issuer names another issuer, for example because of a trailing slash or a multi-tenant Entra authority. |
| Startup | `the discovery document at ... names issuer` | The document at `MIDPOINT_MCP_OIDC_DISCOVERY_URL` names another issuer than `MIDPOINT_MCP_OIDC_ISSUER`. |
| 401 body | `no bearer token` | The request carried no `Authorization: Bearer` header. |
| 401 body | `invalid token: oidc: id token issued by a different provider` | The token's `iss` differs from the issuer. On Entra this is usually a version 1 token. |
| 401 body | `invalid token: oidc: expected audience` | The token's `aud` lacks the audience. On Keycloak the audience mapper is missing. On Entra the token is version 1, or the audience is set to the Application ID URI. |
| 401 body | `invalid token: oidc: token is expired` | The token expired, or the server's clock is wrong. |
| 401 body | `invalid token: failed to verify signature` | The server cannot fetch the key set, or another provider or realm signed the token. |
| 401 body | `invalid token: calling midPoint` | The correlation search could not reach midPoint. |
| 401 body | `invalid token: midPoint /users/search: unexpected status` | midPoint refused the correlation search. A 401 status means the service account's credentials are wrong. A 403 status means the account lacks the REST action `searchObjects`. |
| 401 body | `invalid token: no midPoint user matches` | No user holds the token's value in the correlation attribute, or the service account cannot read users. |
| 401 body | `invalid token: ambiguous correlation` | Two or more users hold the same value. |
| 401 body | `invalid token: no midPoint user that holds an archetype listed in MIDPOINT_MCP_OIDC_CLIENT_ARCHETYPES` | The client's id names no user with an agent archetype. A client named like a person is refused here on purpose. |
| 401 body | `is present but is not a non-empty string or number` | The client claim is on the token with an unusable value. |
| Tool result | midPoint answers `Access denied` or 403 | The token was accepted, but the user is outside the `#proxy` scope or lacks midPoint authorization for the operation. |
