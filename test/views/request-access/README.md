# Get access checks

Run `node test/views/run.mjs --suite request-access` from the repository root.
The suite covers every acceptance criterion in contract 7.2 and the shared
bridge, theme, errors, slot, keyboard, sizing and sandbox requirements.

Regenerate only these fixtures with:

```sh
MIDPOINT_MCP_WRITE_VIEW_FIXTURES=1 go test -run '^TestWriteRequestAccessViewFixtures$' .
```

`view_fixtures_request_access_test.go` drives this server in a UI session.
Approval cases and people use the existing neutral recordings; catalog and
schema responses are authored in the same REST formats. It also tests the
real request handlers, including refusals before writes. The form-schema
fixtures in `internal/midpoint/testdata/request_schema*` cover both schema
sources. Browser-only edge cases are named mutations in `derive.mjs`.

The only scheduled work in the view is the eight-second slow notice and the
600 ms search debounce explicitly required by D34. Neither refreshes the view
without user input. The view never times out or retries a call.
