# View harness

A fake MCP Apps host in headless Chromium that loads a view document, feeds
it real tool results and checks it against
[`docs/ui-contract.md`](../../docs/ui-contract.md): the acceptance criteria of
7.1 (the approval inbox), the shared criteria, and the parts of chapters 3, 5
and 6 the inbox relies on, with the owner decisions draft.9 adds (D37 and
later).

It is not part of `go test ./...` and needs no Node or browser there.

## Run

```sh
node test/views/run.mjs                      # checks views/approval-inbox.html
node test/views/run.mjs --view path/to/view.html --only ac07 --jobs 4
node test/views/run.mjs --list               # every check and the criterion it covers
node test/views/run.mjs --screenshots /tmp/shots --json /tmp/report.json
```

Each check prints `PASS` or `FAIL` with its reasons. Exit code 0 when all
pass, 1 when any fails, 2 when the harness could not run. A full run takes
about a minute.

Requirements: Node 18 or later, and Playwright with its Chromium. Nothing is
installed in this repository (no `package.json`, no `node_modules`). The
runner uses `PLAYWRIGHT_MODULE` when set (the path of a `playwright` or
`playwright-core` package directory), else the newest copy in the npx cache
(`~/.npm/_npx/*/node_modules/`) whose Chromium is installed, else a regular
install. `npx playwright install chromium`, run once anywhere, fills both.

## How it works

- `host.html` and `host.js` are the host. The page creates a sandboxed iframe
  (`sandbox="allow-scripts"`, an opaque origin) and speaks the host side of
  the MCP Apps bridge over `postMessage`: it answers `ui/initialize` with a
  `hostContext` and `hostCapabilities` (togglable per scenario), sends nothing
  before `ui/notifications/initialized`, then sends `tool-input` and
  `tool-result`, answers the view's `tools/call` from fixtures (optionally
  delayed or held), answers `ui/open-link`, `ui/message`,
  `ui/update-model-context` and `ui/request-display-mode`, and records every
  message both ways. It notes protocol breaches (a call without the
  capability, a tool or argument outside the inbox's allowlist).
- Every request is routed: the host page and the view document are served
  from fake origins, the view with the default CSP of contract 3.2; anything
  else the view asks for is recorded and refused. `instrument.js` runs in the
  view's frame before its scripts and records timers, network APIs, storage,
  popups, CSP violations and uncaught errors.
- `checks.mjs` holds the checks. They find elements by role, accessible name
  and visible text, never by CSS class or id. `strings.mjs` holds every
  expected string from the catalog (section 10) in one place; change wording
  there.
- `run.mjs` runs the checks, a few at a time, then the run-wide ones (no
  network, no console errors, no storage, no long timers, protocol) over every
  view the run opened.

## Fixtures

`fixtures/*.json` are real results: this server's own code, in a UI session,
against a fake midPoint serving the answers recorded on midPoint 4.10.3
(`internal/midpoint/testdata`). Each file is
`{"call": {name, arguments}, "about": …, "result": <the CallToolResult>}`.
Regenerate them after a change to what the inbox's tools return:

```sh
MIDPOINT_MCP_WRITE_VIEW_FIXTURES=1 go test -run TestWriteViewFixtures .
```

The generator (`view_fixtures_test.go`) names, next to each fixture, the
few midPoint answers it modifies because nothing was recorded for them.

Edge cases the recordings can't produce (deadlines relative to now, a
missing step count, intermediary slots, links, long or marked-up text, and
so on) are derived in `derive.mjs` from a copy of a real fixture, each by a
named mutation. Expected names and labels are computed from the data with
the naming rules of contract 4.5.
