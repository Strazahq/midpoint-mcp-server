# Authorization — what the midPoint account behind the server may do

This server talks to midPoint over REST as **one** midPoint account. Which account,
and what that account is allowed to do, is the single most important deployment
decision you make: it is the blast radius of the whole integration.

There are two supported shapes, and this document ships a complete, importable
midPoint role for each:

| Profile | Deployment | Role example | Standing privilege of the account |
| --- | --- | --- | --- |
| **rs-service** | resource-server mode (OIDC, `--http`) — every call runs as the correlated end user | [`examples/role-mcp-rs-service.xml`](../examples/role-mcp-rs-service.xml) | **near zero**: REST entry + `#proxy`, no model rights at all |
| **direct-service** | personal/stdio mode and any deployment using a *shared technical account* | [`examples/role-mcp-direct-service.xml`](../examples/role-mcp-direct-service.xml) | enumerated: exactly the object types and operations the tools use |

> Everything below was verified against **midPoint 4.10.3** with throwaway accounts.
> Where 4.10.3 behaves differently from what the documentation (including this
> project's older notes) claimed, the verified behaviour is stated and the old claim
> is called out.

## The five facts that shape both profiles

1. **REST authorization in 4.10 is per endpoint, not all-or-nothing.**
   Besides `authorization-rest-3#all` there is one action per REST operation:
   `#getSelf`, `#getObject`, `#getObjects`, `#searchObjects`, `#addObject`,
   `#modifyObject`, `#deleteObject`, `#completeWorkItem`, `#executeScript`,
   `#testResource`, `#importFromResource`, `#notifyChange`, `#compareObject`,
   `#getExtensionSchema`, `#resetCredential`, `#claimWorkItem`, `#releaseWorkItem`,
   `#delegateWorkItem`, `#cancelCase`, the task/log/thread ones, and the
   value-policy ones. Granting only the handful this server calls means every other
   REST verb is refused **before** midPoint even looks at the object — a `DELETE`
   comes back `403` from the security filter.

2. **REST entry is evaluated against the *authenticated* account, even while
   impersonating.** With `Switch-To-Principal`, midPoint checks the REST action
   against the service account and the *object/model* authorizations against the
   impersonated user. A `#proxy`-only account is refused (`403`) on every endpoint,
   including `/self` — impersonation is not a way in, it is a way to *act as*
   someone once you are already in.

3. **`#proxy` can be scoped by archetype.** `ObjectSelectorType` accepts
   `archetypeRef` (repeatable, OR semantics) in 4.10.3, so the impersonatable
   population can be limited to real people (and, if you want, machine identities)
   while administrators, contractors, service and personal-agent identities stay out
   of reach. Verified: in-scope archetype → impersonation succeeds; out-of-scope
   archetype → `403` at the security filter.

4. **`#proxy` is per (subject, impersonated object) — it cannot be scoped to a
   downstream operation.** There is no way to express "may impersonate, but only for
   approvals". See [Why not "`#proxy` only for approvals"](#why-not-proxy-only-for-approvals).

5. **`search_audit` cannot be least-privileged on 4.10.** It runs a Groovy bulk
   action (midPoint 4.10 has no audit REST endpoint), and bulk-action scripting is
   gated by the deployment's *expression profile*, not by any grantable action. See
   [The search_audit exception](#the-search_audit-exception).

Correction to an earlier claim in this repo's docs: **superuser's `#all` DOES cover
`authorization-rest-3#proxy`** on 4.10.3 — a superuser can impersonate without any
extra grant. The explicit `#proxy` role still matters, because a service account
must not *be* a superuser; the moment you take superuser away (which is the point of
this document) the explicit grant becomes load-bearing.

## Profile: rs-service (OIDC resource-server mode)

**This is the recommended enterprise deployment.** The bearer token identifies the
human, the server correlates them to a midPoint user, and every call is executed as
that user via `Switch-To-Principal`. midPoint's own authorizations for that human are
the access-control decision — the server adds no permission model of its own, and
audit attributes every change to the person who asked for it.

The service account therefore needs **no model rights whatsoever**. It needs:

- the REST endpoints the tools call (`#getSelf`, `#getObject`, `#searchObjects`,
  `#addObject`, `#modifyObject`, `#completeWorkItem`), and
- `#proxy`, scoped to the archetypes it may act for.

That is the entire role. Verified on 4.10.3 with an account holding exactly that:

| Check | Result |
| --- | --- |
| `GET /self` with `Switch-To-Principal: <employee oid>` | `200`, returns that employee |
| `GET /self` **without** the header | `500 Access denied` — the account cannot even read itself |
| `POST /users/search` **without** the header | `200` with an **empty** list — zero visibility |
| `GET /users/<oid>` **without** the header | `403` |
| `Switch-To-Principal: <external contractor oid>` | `403` — outside the archetype scope |

An account that can see nothing and change nothing on its own, and whose only power
is to borrow the authorizations of a bounded set of end users, is a much smaller
target than any enumerated permission set could be. If its credentials leak, the
attacker still has to present a valid OIDC token for a specific in-scope human to do
anything at all.

**The end users must be authorized.** Because the request runs as them, a user with
no midPoint authorizations cannot even read `/self`. Assign real users midPoint's
built-in **End user** role or your deployment's equivalent; that is also what enables
the self-service tools (`list_requestable_roles`, `request_role`, work items).

## Profile: direct-service (shared technical account)

In personal/stdio mode the credentials are usually the operator's own, and midPoint
sees the real person — nothing extra is needed. But many deployments run stdio (or
loopback HTTP) with a **shared technical account**, and then *every* call executes as
that account. It must not be a superuser.

[`examples/role-mcp-direct-service.xml`](../examples/role-mcp-direct-service.xml) is
the enumerated least-privilege alternative: exactly the REST endpoints and model
operations the 25 tools use, and nothing else. Highlights of what it deliberately
does **not** grant:

- no `#deleteObject` — no tool deletes anything, so `DELETE /users/<oid>` is refused
  at the REST layer;
- `#modify` on `UserType` is restricted with `<item>activation</item>` and
  `<item>assignment</item>` — the account can enable/disable and (un)assign, but a
  `PATCH` of `fullName`, `emailAddress` or `credentials` is refused (verified: `403`);
- no read of `TaskType`, `SystemConfigurationType`, `SecurityPolicyType`, … — the
  configuration layer is invisible (verified: `GET /tasks/<oid>` → `403`,
  `PATCH /systemConfigurations/…` → `403`);
- no script execution (see below).

### Endpoint / authorization map

Everything the server sends, and what each call needs. `rest-3` =
`…/security/authorization-rest-3#`, `model-3` = `…/security/authorization-model-3#`.

| Tools | Request | `rest-3` action | `model-3` action (object) |
| --- | --- | --- | --- |
| `ping`, and the `/self` step of `list_my_*`, `list_work_items`, `list_my_requests` | `GET /self` | `getSelf` | `read` (UserType) |
| `search_users`, `list_roles`, `list_requestable_roles`, `list_resources`, `search_objects`, `list_my_team`, `list_my_managers`, `list_my_requests`, `list_work_items` | `POST /{users,roles,resources,orgs,services,shadows,cases}/search` | `searchObjects` | `read` (each type searched) |
| `get_user`, `get_user_assignments`, `get_role`, `get_resource`, `get_case` | `GET /{users,roles,resources,cases}/{oid}` | `getObject` | `read` (that type; plus RoleType/OrgType/ArchetypeType/ServiceType for `?options=resolveNames` to fill `targetName`) |
| `create_user` | `POST /users` | `addObject` | `add` (UserType) |
| `enable_user`, `disable_user` | `PATCH /users/{oid}` | `modifyObject` | `modify` (UserType, item `activation`) |
| `assign_role`, `unassign_role`, `request_role` | `PATCH /users/{oid}` | `modifyObject` | `modify` (UserType, item `assignment`) + `assign`/`unassign` (UserType → target RoleType) |
| `recompute_user` | `PATCH /users/{oid}?options=reconcile` | `modifyObject` | `modify` (UserType; empty delta) |
| `approve_work_item`, `reject_work_item` | `POST /cases/{oid}/workItems/{id}/complete` | `completeWorkItem` | `completeWorkItem` (CaseType) |
| (no direct call — midPoint's projector, as a consequence of the two rows above) | — | — | `add`/`modify`/`delete` (ShadowType) whenever the affected users are provisioned |
| `search_audit` | `POST /rpc/executeScript` | `executeScript` | `executeScript` + `auditRead` + `read` (SystemConfigurationType) **and a deployment expression profile — see below** |

**The half-applied-write trap.** `enable_user` / `disable_user` change the focus, and
midPoint's projector then pushes that downstream as the *same* account. If the role
grants `modify` on `UserType` but not on `ShadowType`, midPoint **commits the focus
change and then fails provisioning** (`not authorized for operation …#modify on
shadow:<oid>`): the user is disabled in midPoint and still enabled in the connected
systems — worse than a clean refusal. Verified on 4.10.3. Any deployment whose tools
may touch provisioned users needs the `provision-shadows` authorization; midPoint's
own built-in *End user* role has the same shape for self-shadows.

Notes on scoping the enumerated role further:

- `assign`/`unassign` are scoped with `<target><type>RoleType</type></target>`. A
  deployment that only wants self-service can narrow the target with a filter (e.g.
  `requestable = true`), exactly like midPoint's built-in *End user* role does.
- `ShadowType`, `ServiceType` and `OrgType` reads exist only for `search_objects`
  reporting; drop them if you do not use it.
- `ArchetypeType` read exists only so `?options=resolveNames` can print an
  archetype's name in assignment listings; dropping it degrades a display name, it
  breaks nothing.

### The `search_audit` exception

midPoint 4.10 exposes **no** audit REST endpoint, so `search_audit` reaches the audit
trail through a Groovy `execute-script` bulk action. On 4.10.3 that is gated twice:

1. by authorizations you *can* grant (`rest-3#executeScript`, `model-3#executeScript`,
   `authorization-bulk-3#all`, `model-3#auditRead`, plus `read` of
   `SystemConfigurationType` because the script pipeline seeds from it), and
2. by the deployment's **bulk-actions expression profile**, which you cannot grant to
   a principal at all.

With no `expressions/defaults/bulkActions` configured, midPoint uses the built-in
`##legacyUnprivilegedBulkActions` profile and refuses the script evaluator:

```
Access to script expression evaluator not allowed
(expression profile: ##legacyUnprivilegedBulkActions) in script
```

Verified on 4.10.3, the important part: adding `authorization-model-3#all` to the
account is **not** enough — it is still refused. Only the real superuser action
`authorization-3#all` gets through. In other words, **on 4.10 `search_audit` requires
a superuser-equivalent account**; there is no least-privilege grant that enables it.

A deployment that wants `search_audit` from a non-superuser account has exactly one
knob: set `SystemConfigurationType/expressions/defaults/bulkActions` to a permissive
expression profile. Understand what that buys the account: arbitrary Groovy inside
midPoint, with privilege elevation if the profile allows it — which is a *larger*
grant than the superuser role you were trying to remove. Our recommendation:

- **leave it off.** The other 24 tools work fine under the enumerated role; the audit
  block is shipped commented-out in the example so enabling it is a deliberate,
  reviewed act;
- if you need audit through an assistant, run that one workload under separately
  managed admin credentials, or read the audit trail from midPoint's own UI/report
  engine, rather than widening the account 24 tools share.

## Why not "`#proxy` only for approvals"

The most common question from deployers is: *can the service account impersonate
users only for approval operations, and use its own rights for everything else?*

midPoint cannot express that, and the shape you would get if it could is worse than
what you have:

- **`#proxy` is per subject + impersonated object.** Its selector describes *who may
  be impersonated* (type, archetype, org, filter), not *what may be done afterwards*.
  There is no "action" dimension on the proxy authorization, so "impersonate for
  `completeWorkItem` only" is not expressible.
- **It would force broad standing privilege.** If only approvals ran as the human,
  every read and every write would run as the service account — which would then need
  standing `read`/`add`/`modify`/`assign` over the whole user population. That is the
  opposite of the goal: instead of an account that can do nothing by itself, you get
  an account that can read and change everyone, all the time.
- **It would corrupt attribution.** midPoint's audit records the acting principal.
  With mixed lanes, approvals would be attributed to the human while the *changes*
  those approvals authorize would be attributed to a robot. An IGA audit trail whose
  writes all say `mcp-service` is not an audit trail.

The enterprise answer is the inverse: keep impersonation **total** — every call runs
as the correlated human — and make the service account's own privilege as close to
zero as midPoint allows. `#proxy` then *is* essentially the only power the account
has, and it is bounded twice over: by the archetype scope on the grant, and by each
end user's own authorizations.

## Verifying a deployment

```sh
MP=https://midpoint.example.com/midpoint
SVC=mcp-service:...            # the service account
ALICE=<oid of an in-scope user>
EXT=<oid of an out-of-scope user, e.g. a contractor>

# impersonation works for an in-scope archetype
curl -s -u "$SVC" -H "Switch-To-Principal: $ALICE" "$MP/ws/rest/self"       # 200, alice

# ... and is refused outside the scope
curl -s -o /dev/null -w '%{http_code}\n' -u "$SVC" \
     -H "Switch-To-Principal: $EXT" "$MP/ws/rest/self"                      # 403

# rs-service profile: the account is blind on its own
curl -s -o /dev/null -w '%{http_code}\n' -u "$SVC" "$MP/ws/rest/self"       # 500 Access denied

# no tool deletes anything, so this must fail at the REST layer
curl -s -o /dev/null -w '%{http_code}\n' -u "$SVC" -X DELETE \
     "$MP/ws/rest/users/$ALICE"                                             # 403

# the configuration layer must be invisible/untouchable
curl -s -o /dev/null -w '%{http_code}\n' -u "$SVC" -X PATCH \
     -H 'Content-Type: application/json' \
     -d '{"objectModification":{"itemDelta":[{"modificationType":"replace","path":"description","value":"x"}]}}' \
     "$MP/ws/rest/systemConfigurations/00000000-0000-0000-0000-000000000001"  # 403
```

Two failure signatures are worth telling apart: an **HTML** `403` body is the REST
security filter refusing the endpoint (the account lacks the `rest-3` action), while a
**JSON** `403`/`500` with `not authorized for operation …` / `Access denied` is
midPoint's model layer refusing the object or operation.
