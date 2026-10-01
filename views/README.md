# View documents

Each MCP Apps view is one self-contained HTML document in this directory,
embedded into the server binary at build time and served as a
`ui://midpoint/*` resource. The catalog in `views.go` names the file for each
view; a view is served once its file is here.

The rules are in [`docs/ui-contract.md`](../docs/ui-contract.md) section 3.2,
and `views_test.go` checks every `.html` file here against them:

- at most 150 KB, uncompressed;
- inline `<style>`, `<script>` and SVG only: no `<link>`, `@import`,
  `<iframe>`, absolute `src=` or `href=` URLs, `fetch(`, `XMLHttpRequest`,
  `WebSocket` or `EventSource`;
- the same bytes for every caller: no templating, no deployment settings.
