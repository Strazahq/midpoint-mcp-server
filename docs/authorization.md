# Authorize the midPoint account behind the server

The server talks to midPoint over REST as one midPoint account. What that account
may do is the blast radius of the whole integration, so choosing it is the most
important deployment decision. This document describes two shapes and ships an
importable midPoint role for each.

| Profile | When to use it | Role | Standing privilege of the account |
| --- | --- | --- | --- |
| rs-service | Shared mode, where every call runs as the correlated user | [`examples/role-mcp-rs-service.xml`](../examples/role-mcp-rs-service.xml) | REST entry and `#proxy`, no model rights, plus the read authorization for correlation described below |
| direct-service | Personal mode with a shared technical account, where every call runs as that account | [`examples/role-mcp-direct-service.xml`](../examples/role-mcp-direct-service.xml) | Exactly the object types and operations the tools use |

Unless a statement says otherwise, the behaviour described here was verified
against midPoint 4.10.3 with throwaway accounts.

## Know the five facts that shape both roles

1. REST authorization in midPoint 4.10 is granted per endpoint. Besides
   `authorization-rest-3#all` there is one action per REST operation: `#getSelf`,
   `#getObject`, `#getObjects`, `#searchObjects`, `#addObject`, `#modifyObject`,
   `#deleteObject`, `#completeWorkItem`, `#executeScript`, `#testResource`,
   `#importFromResource`, `#notifyChange`, `#compareObject`, `#getExtensionSchema`,
   `#resetCredential`, `#claimWorkItem`, `#releaseWorkItem`, `#delegateWorkItem`,
   `#cancelCase`, and the task, log, thread and value policy actions. If the account
   holds only the actions this server calls, every other REST operation is refused
   at the security filter before midPoint looks at any object. A `DELETE` gets 403.
2. midPoint checks the REST action against the authenticated account, even while
   that account impersonates someone. With `Switch-To-Principal`, the REST action
   is checked for the service account and the model authorizations are checked for
   the impersonated user. An account that holds only `#proxy` gets 403 on every
   endpoint, `/self` included. Impersonation is a way to act as someone once you
   are in, not a way in.
3. `#proxy` can be scoped by archetype. In 4.10.3 an object selector accepts
   `archetypeRef`, which is repeatable, and an object matches if it holds any of the
   listed archetypes. Impersonating a user with an archetype in scope succeeds, and
   impersonating a user outside the scope gets 403 at the security filter.
4. `#proxy` describes who may be impersonated, not what may be done afterwards. No
   authorization can say "impersonate only for approvals". The section
   [Understand why the account cannot impersonate only for approvals](#understand-why-the-account-cannot-impersonate-only-for-approvals)
   explains why that is the better design anyway.
5. `search_audit` cannot be least-privileged on 4.10. midPoint 4.10 has no audit
   REST endpoint, so the tool runs a Groovy bulk action, and bulk action scripting
   is gated by the deployment's expression profile, which no authorization grants.
   See [Handle the search_audit exception](#handle-the-search_audit-exception).

The superuser role's `#all` covers `authorization-rest-3#proxy` on 4.10.3, so a
superuser can impersonate without any extra grant. An earlier version of these docs
claimed the opposite. The explicit `#proxy` grant still matters, because the service
account must not be a superuser, and once superuser is gone the explicit grant is
what keeps impersonation working.

## Use the rs-service role in shared mode

In shared mode the bearer token identifies the caller, the server correlates the
token to a midPoint user, and every tool call runs as that user through
`Switch-To-Principal`. midPoint's authorizations for that user decide what happens.
The server adds no permission model of its own, and midPoint's audit attributes
every change to the person or agent who asked for it.

For tool calls the service account therefore needs no model rights. It needs the
REST actions for the endpoints the tools call, which are `#getSelf`, `#getObject`,
`#searchObjects`, `#addObject`, `#modifyObject` and `#completeWorkItem`. It also
needs `#proxy`, scoped to the archetypes it may act for. The example role holds
exactly these two authorizations. Verified on 4.10.3 with an account that held
exactly that role:

| Check | Result |
| --- | --- |
| `GET /self` with `Switch-To-Principal: <employee oid>` | 200, that employee |
| `GET /self` without the header | 500 Access denied. The account cannot read itself. |
| `POST /users/search` without the header | 200 with an empty list |
| `GET /users/<oid>` without the header | 403 |
| `Switch-To-Principal: <oid of a user outside the archetype scope>` | 403 |

If the account's credentials leak, the attacker can impersonate any user inside the
`#proxy` scope by sending `Switch-To-Principal` directly. Keep the scope as narrow
as the population the server serves, and keep the password out of files.

The users the server impersonates need authorizations of their own, because every
call runs as them. A user with no midPoint authorizations cannot even read `/self`.
Assign midPoint's built-in End user role or your own equivalent. That role is also
what makes the self-service tools such as `list_requestable_roles`, `request_role`
and the work item tools useful.

### Let the service account find users

Correlation is the search that maps a token to a midPoint user. It runs before any
impersonation, as the service account itself and without `Switch-To-Principal`,
because the server does not yet know whom to impersonate. The searches filter on
these user items:

- `externalId`, against the token's `sub`, but only if your schema defines such an
  item. midPoint 4.10's standard user schema does not, and the server moves on when
  this search fails.
- the correlation attribute, `name` by default or whatever
  `MIDPOINT_MCP_OIDC_CORRELATION_ATTRIBUTE` names.
- `name`, which a client's own token is always matched on.
- `archetypeRef`, which the archetype check for client tokens filters on.

The example role carries no model rights, and the check table above shows that its
`POST /users/search` returns an empty list. With that role alone, every correlation
search finds nobody and every token is refused with `no midPoint user matches`. Do
not deploy `examples/role-mcp-rs-service.xml` unchanged in shared mode. A
deployment that works with it today is getting read rights from another role.

The service account needs read access to the users it may correlate, limited to the
same archetypes as its `#proxy` authorization and to the items above. midPoint's
authorization model supports this shape. Its 4.10 documentation describes
`model-3#read` as a shortcut for the `get` and `search` actions. It says the search
authorization governs how a search filter can be formed and which objects are
returned. It describes `archetypeRef` as a selector that limits an authorization to
objects with that archetype, and `<item>` as a way to limit read access to selected
items. A minimal authorization in that shape looks like this, with your archetype
oids and the correlation attribute you configured.

```xml
<authorization>
    <name>read-users-for-correlation</name>
    <description>Lets midpoint-mcp-server find the user a token belongs to.</description>
    <action>http://midpoint.evolveum.com/xml/ns/public/security/authorization-model-3#read</action>
    <object>
        <type>UserType</type>
        <archetypeRef oid="11111111-2222-3333-4444-5555555500a1"/>
        <archetypeRef oid="11111111-2222-3333-4444-5555555500a2"/>
    </object>
    <item>name</item>
    <item>archetypeRef</item>
    <!-- Add the correlation attribute if it is not name, for example extension/entraObjectId. -->
</authorization>
```

This requirement follows from the code. The authorization above has not yet been
fired live against midPoint 4.10 with this server. After you add it, run the
correlation check in [Verify a deployment](#verify-a-deployment). If the search
still returns an empty list, remove the `<item>` lines, check again, and record what
your midPoint version needs. The cost of this grant is that the account can read
those items of in-scope users on its own, without a token.

## Use the direct-service role for a shared technical account

In personal mode the credentials are usually the operator's own, and midPoint sees
the real person. Some deployments run personal mode, over stdio or loopback HTTP,
with a shared technical account instead. Then every call runs as that account, and
it must not be a superuser.

[`examples/role-mcp-direct-service.xml`](../examples/role-mcp-direct-service.xml)
grants exactly the REST endpoints and model operations the tools use. It
deliberately leaves out the following.

- It grants no `#deleteObject`, because no tool deletes anything. `DELETE
  /users/<oid>` is refused at the REST layer.
- It restricts `#modify` on users to the items `activation` and `assignment`. The
  account can enable, disable, assign and unassign, but a `PATCH` of `fullName`,
  `emailAddress` or `credentials` gets 403.
- It grants no read of `TaskType`, `SystemConfigurationType` or
  `SecurityPolicyType`. `GET /tasks/<oid>` and `PATCH /systemConfigurations/...`
  both get 403.
- It grants no script execution, as [Handle the search_audit exception](#handle-the-search_audit-exception)
  explains.

### Map each tool to the authorizations it needs

`rest-3` stands for `http://midpoint.evolveum.com/xml/ns/public/security/authorization-rest-3#`
and `model-3` for `http://midpoint.evolveum.com/xml/ns/public/security/authorization-model-3#`.
In shared mode the `rest-3` actions are checked for the service account and the
`model-3` actions for the impersonated user, except in the correlation row.

| Tools | Request | `rest-3` action | `model-3` action and object |
| --- | --- | --- | --- |
| `ping`, `whoami`, and the `/self` step of the `list_my_*` tools, `list_work_items`, `list_my_requests` and `decide_work_item` | `GET /self` | `getSelf` | `read` on UserType |
| `search_users`, `list_roles`, `list_requestable_roles`, `list_resources`, `search_objects`, `list_my_team`, `list_my_managers`, `list_my_teammates`, `list_my_requests`, `list_work_items` | `POST /{users,roles,resources,orgs,services,shadows,cases}/search` | `searchObjects` | `read` on each type searched |
| `get_user`, `get_user_assignments`, `get_role`, `get_resource`, `get_case`, and the case checks and read-backs of `decide_work_item` and `cancel_request` | `GET /{users,roles,resources,cases}/{oid}` | `getObject` | `read` on that type, plus RoleType, OrgType, ArchetypeType and ServiceType so that `resolveNames` can fill `targetName` |
| `create_user` | `POST /users` | `addObject` | `add` on UserType |
| `enable_user`, `disable_user` | `PATCH /users/{oid}` | `modifyObject` | `modify` on UserType, item `activation` |
| `assign_role`, `unassign_role`, `request_role` | `PATCH /users/{oid}` | `modifyObject` | `modify` on UserType, item `assignment`, plus `assign` or `unassign` on UserType with target RoleType |
| `recompute_user` | `PATCH /users/{oid}?options=reconcile` | `modifyObject` | `modify` on UserType, with an empty change |
| `decide_work_item` | `POST /cases/{oid}/workItems/{id}/complete` | `completeWorkItem` | `completeWorkItem` on CaseType |
| `cancel_request` | `POST /cases/{oid}/cancel` | `cancelCase` | Not yet verified |
| No tool. midPoint's projector does this as a consequence of the activation and assignment rows. | none | none | `add`, `modify` and `delete` on ShadowType whenever the affected users are provisioned |
| `search_audit` | `POST /rpc/executeScript` | `executeScript` | `executeScript`, `auditRead`, and `read` on SystemConfigurationType, and a deployment expression profile |
| Correlation, in shared mode only, run as the service account itself | `POST /users/search` | `searchObjects` | `read` on the users in scope, see [Let the service account find users](#let-the-service-account-find-users) |

Neither example role grants `rest-3#cancelCase`, so with the roles as shipped
`cancel_request` is refused at the REST layer. Add the action to the `rest-entry`
authorization if requesters should withdraw their own requests. `cancel_request`
has not yet been fired against a live midPoint, so its model authorization is not
documented here.

When the settings file sets `requests.formItems`, the server also reads midPoint's
extension schemas at startup, as the service account itself, from `/ws/rest/schemas`
and `/ws/schema`. Neither example role was verified with that setting.

Beware the half-applied write. `enable_user` and `disable_user` change the user, and
midPoint's projector then pushes the change to connected systems as the same
account. If the role grants `modify` on UserType but not on ShadowType, midPoint
commits the user change and then fails provisioning with `not authorized for
operation ...#modify on shadow:<oid>`. The user ends up disabled in midPoint and
still enabled downstream, which is worse than a clean refusal. Verified on 4.10.3.
Grant the `provision-shadows` authorization whenever the tools may touch
provisioned users. midPoint's built-in End user role has the same shape for a
user's own shadows.

The enumerated role can be narrowed further.

- `assign` and `unassign` are scoped to the target type RoleType. A self-service
  deployment can narrow the target with a filter such as `requestable = true`, as
  midPoint's built-in End user role does.
- The reads of ShadowType, ServiceType and OrgType exist only for `search_objects`
  reports and the team tools. Drop them if you do not use those.
- The read of ArchetypeType exists only so that `resolveNames` can print an
  archetype's name in assignment listings. Dropping it blanks a display name and
  breaks nothing.

### Handle the search_audit exception

midPoint 4.10 has no audit REST endpoint, so `search_audit` reaches the audit trail
through a Groovy `execute-script` bulk action. On 4.10.3 two gates apply.

1. Authorizations you can grant: `rest-3#executeScript`, `model-3#executeScript`,
   `authorization-bulk-3#all`, `model-3#auditRead`, and `read` on
   SystemConfigurationType, because the script pipeline starts from it.
2. The deployment's bulk actions expression profile, which no principal can be
   granted.

With no `expressions/defaults/bulkActions` configured, midPoint uses the built-in
`##legacyUnprivilegedBulkActions` profile and refuses to run the script with this
message:

```
Access to script expression evaluator not allowed
(expression profile: ##legacyUnprivilegedBulkActions) in script
```

Adding `authorization-model-3#all` to the account does not help. Only the real
superuser action `authorization-3#all` gets through, so on 4.10 `search_audit`
needs a superuser-equivalent account. The one alternative is to set
`SystemConfigurationType/expressions/defaults/bulkActions` to a permissive profile,
which gives the account arbitrary Groovy inside midPoint. That is a larger grant
than the superuser role you were trying to remove. The tool also cannot work in
shared mode, because the impersonated user has no script authorization.

Leave it off. Every other tool works under the enumerated role, and the example
ships the audit block commented out so that enabling it is a deliberate, reviewed
act. If you need audit through an assistant, run that one workload under separately
managed admin credentials, or read the audit trail in midPoint's own interface.

## Understand why the account cannot impersonate only for approvals

Deployers often ask whether the service account can impersonate users only for
approvals and use its own rights for everything else. midPoint cannot express that,
and the result would be worse if it could.

- `#proxy` selects who may be impersonated by type, archetype, org or filter. It has
  no dimension for what is done afterwards, so "impersonate for `completeWorkItem`
  only" cannot be written.
- If only approvals ran as the person, every read and write would run as the
  service account. The account would then need standing `read`, `add`, `modify` and
  `assign` over the whole user population, all the time.
- midPoint's audit records the acting principal. Approvals would be attributed to
  the person while the changes they authorize would be attributed to the service
  account. An identity governance audit trail whose writes all name the service
  account does not tell you who did what.

The better answer is the inverse. Keep impersonation total, so every tool call runs
as the correlated user, and keep the service account's own rights as close to zero
as midPoint allows. Its power is then bounded twice: by the archetype scope of
`#proxy`, and by each user's own authorizations.

## Verify a deployment

```sh
MP=https://midpoint.example.com/midpoint
SVC=mcp-service:...            # the service account and its password, typed in and never stored
ALICE=<oid of an in-scope user>
EXT=<oid of an out-of-scope user, for example a contractor>

# impersonation works for an in-scope archetype
curl -s -u "$SVC" -H "Switch-To-Principal: $ALICE" "$MP/ws/rest/self"       # 200, alice

# ... and is refused outside the scope
curl -s -o /dev/null -w '%{http_code}\n' -u "$SVC" \
     -H "Switch-To-Principal: $EXT" "$MP/ws/rest/self"                      # 403

# shared mode: correlation finds an in-scope user without the header
curl -s -u "$SVC" -H 'Content-Type: application/json' \
     -d '{"query":{"filter":{"text":"name = \"alice\""}}}' \
     "$MP/ws/rest/users/search"                                             # alice, not an empty list

# rs-service profile: the account cannot read itself
curl -s -o /dev/null -w '%{http_code}\n' -u "$SVC" "$MP/ws/rest/self"       # 500 Access denied

# no tool deletes anything, so this must fail at the REST layer
curl -s -o /dev/null -w '%{http_code}\n' -u "$SVC" -X DELETE \
     "$MP/ws/rest/users/$ALICE"                                             # 403

# the configuration layer must be untouchable
curl -s -o /dev/null -w '%{http_code}\n' -u "$SVC" -X PATCH \
     -H 'Content-Type: application/json' \
     -d '{"objectModification":{"itemDelta":[{"modificationType":"replace","path":"description","value":"x"}]}}' \
     "$MP/ws/rest/systemConfigurations/00000000-0000-0000-0000-000000000001"  # 403
```

Two failure signatures are worth telling apart. An HTML 403 body is the REST
security filter refusing the endpoint, because the account lacks the `rest-3`
action. A JSON 403 or 500 with `not authorized for operation` or `Access denied` is
midPoint's model layer refusing the object or the operation.
