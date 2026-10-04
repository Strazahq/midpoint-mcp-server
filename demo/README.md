# Views demo

`index.html` is one self-contained page that shows the server's four MCP Apps
views (`views/*.html`) the way a chat app renders them: a short chat, the tool
call, the view inline, and a permission prompt whenever the view asks to run
a tool that changes midPoint. Open it in a browser straight from disk; it
needs no server and makes no network requests.

It is a **simulated MCP Apps host, not a real chat client**. There is no
model and no MCP server behind it: the assistant's lines are scripted, and
every tool call is answered from recorded test data.

## What it shows

- Eight scenarios, picked on the left:

  | Scenario | Person | View | Opened by | Writes it can answer |
  | --- | --- | --- | --- | --- |
  | Approve or reject a request | Dana Lee | Requests to approve | `inbox.approver` | approve: `decide.approve-next`, reject: `decide.reject-open` |
  | A manager's inbox | Jane Doe | Requests to approve | `inbox.manager` | approve Finance reports: `decide.approve-open` |
  | Claim a request offered to a group | Dana Lee | Requests to approve | `inbox.offered` | `claim.claimed` (list re-read: `inbox.claimed`), `release.released` |
  | Ask for a role | Bob Stone | Get access | `request-access.rules` | Database administrator: `request-access.pending` |
  | Check on and withdraw a request | Bob Stone | My requests | `my-requests.list` | `my-requests.withdrawn` (list re-read: `my-requests.closed`) |
  | Ask to approve requests for a role | Jane Doe | Get access | `request-access.rules-manager` | Release manager as approver: `request-access.pending-approver` |
  | Review a team member's access | Jane Doe | My team's access | `access-review.team` | remove Database admin: `access-review.removed` (re-read: `access-review.after`) |
  | Request access for a team member | Jane Doe | Get access | `request-access.rules-report` | none recorded for Bob: adapted from `request-access.pending` |

- **Request rules.** The three Get access scenarios run as a server that
  can read midPoint's request rules: "Request for" lists the people the rules
  name, each role's "Why you can request this" names the rules that allow
  it, and Release manager is offered to Jane Doe only as its approver,
  because that is the relation her rule names.
- **Read-only calls** the view makes (`whoami`, `get_case`, `list_my_team`,
  `get_user_assignments`, …) are answered straight away and noted under the
  view.
- **Writes** (`decide_work_item`, `claim_work_item`, `release_work_item`,
  `request_role`, `cancel_request`, `unassign_role`) stop at a prompt:
  "Allow the midpoint server to run …?" with the arguments listed, the
  midPoint names (`userName`, `roleName`) marked next to the IDs. Allow answers
  with the recorded result; Deny answers with a JSON-RPC error, which the view
  shows as the host refusing. A write the recordings don't cover exactly is
  answered with the recording of the closest call to the same tool, rewritten
  to the call's IDs, names and acting person, and the host note says so.
- A message a view sends to the chat (Track this request, Request access for
  Bob Stone) continues the chat in the scenario that answers it.
- The scenarios are listed on the left like a chat app's conversations,
  titled by what the person asks and grouped by role (a drawer on narrow
  screens). One line above the chat says who you are and what to try, with the
  full directions under "How to try"; "Next" under the chat leads on.
- **Text the assistant reads**, a tab next to each view, shows the plain text
  of that result (`content[0].text`); host notes show what the view passes back
  with `ui/update-model-context`.
- Messages the view sends to the chat (`ui/message`), links it asks to open,
  and its Expand (fullscreen) request show up as host notes.
- View size (phone 420, chat window 640, computer 860 px, the default) as three icons on the
  view's edge, and one theme button that cycles automatic, light and dark; the
  theme reaches the views as `host-context-changed`.

## The data

All answers are the view-harness fixtures in
[`test/views/fixtures`](../test/views/fixtures): results this server's own
code produced against a fake midPoint serving answers recorded on midPoint
4.10.3 test data. The people and organisations are fictional. Dates are the
recordings', so "today" and "2 days ago" in the views drift as time passes.

## How it works

The host is adapted from the view harness's fake host,
[`test/views/host.js`](../test/views/host.js). Each view document is bundled
byte for byte and loaded into an `<iframe sandbox="allow-scripts">` via
`srcdoc`, so it runs with an opaque origin. The host answers `ui/initialize`
with `hostContext` and `hostCapabilities`, waits for
`ui/notifications/initialized`, delivers the entry call's `tool-input` and
`tool-result`, answers `tools/call`, `ui/message`, `ui/update-model-context`,
`ui/open-link` and `ui/request-display-mode`, and sizes the frame from
`ui/notifications/size-changed`. The page's Content-Security-Policy, which the
views inherit, allows no network access at all.

## Rebuild

After changing a view, a fixture or anything in this directory:

```sh
node demo/build.mjs
```

Node 18 or later, no dependencies. `build.mjs` holds the scenarios (which
fixture answers which call); `src/` holds the page's markup, style and host
code. The build fails if a scenario names a fixture that is missing or
answers a different tool.
