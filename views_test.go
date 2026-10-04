package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

// maxViewBytes is the per-document budget of contract 3.2 rule 3.
const maxViewBytes = 150 * 1024

// viewNetworkConstructs are what a view document must not contain (contract
// 3.2 rules 1 and 4): anything that would load or send something over the
// network, or nest a frame.
var viewNetworkConstructs = []struct {
	name string
	re   *regexp.Regexp
}{
	{"<link", regexp.MustCompile(`(?i)<link\b`)},
	{"@import", regexp.MustCompile(`(?i)@import\b`)},
	{"<iframe", regexp.MustCompile(`(?i)<iframe\b`)},
	// An absolute URL is a scheme or a protocol-relative "//"; data: is the
	// one scheme the default CSP lets a view use.
	{"absolute src= or href=", regexp.MustCompile(`(?i)\b(src|href)\s*=\s*["']?\s*(//|(?:[a-z][a-z0-9+.-]*:)(?:[^"'\s>]*))`)},
	{"fetch(", regexp.MustCompile(`\bfetch\s*\(`)},
	{"XMLHttpRequest", regexp.MustCompile(`XMLHttpRequest`)},
	{"WebSocket", regexp.MustCompile(`WebSocket`)},
	{"EventSource", regexp.MustCompile(`EventSource`)},
}

var dataURL = regexp.MustCompile(`(?i)\b(src|href)\s*=\s*["']?\s*data:`)

// checkViewDocument returns every rule a view document breaks.
func checkViewDocument(doc []byte) []string {
	var broken []string
	if len(doc) > maxViewBytes {
		broken = append(broken, "larger than 150 KB")
	}
	for _, c := range viewNetworkConstructs {
		for _, m := range c.re.FindAll(doc, -1) {
			if c.name == "absolute src= or href=" && dataURL.Match(m) {
				continue
			}
			broken = append(broken, c.name+": "+string(m))
		}
	}
	return broken
}

// TestViewDocuments walks the embedded view directory, so a view added later
// is checked without touching this test.
func TestViewDocuments(t *testing.T) {
	catalogFiles := map[string]bool{}
	for _, d := range viewCatalog {
		catalogFiles[d.File] = true
	}
	err := fs.WalkDir(viewFiles, "views", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := strings.TrimPrefix(p, "views/")
		if name == "README.md" {
			return nil
		}
		if path.Ext(name) != ".html" {
			t.Errorf("%s: only .html view documents belong in the view directory", p)
			return nil
		}
		if !catalogFiles[name] {
			t.Errorf("%s: not in viewCatalog, so it would never be served", p)
		}
		doc, err := fs.ReadFile(viewFiles, p)
		if err != nil {
			return err
		}
		for _, b := range checkViewDocument(doc) {
			t.Errorf("%s: %s", p, b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCheckViewDocument(t *testing.T) {
	if b := checkViewDocument([]byte(testViewDocument)); len(b) != 0 {
		t.Errorf("a clean document was refused: %v", b)
	}
	clean := []string{
		`<img src="data:image/png;base64,AAAA">`,
		`<a href="#details">`,
		`<svg><use href="#icon-check"/></svg>`,
	}
	for _, c := range clean {
		if b := checkViewDocument([]byte(c)); len(b) != 0 {
			t.Errorf("%s refused: %v", c, b)
		}
	}
	dirty := []string{
		`<link rel="stylesheet" href="x.css">`,
		`<style>@import url(x.css);</style>`,
		`<iframe srcdoc="x"></iframe>`,
		`<img src="https://example.org/x.png">`,
		`<img src=http://example.org/x.png>`,
		`<script src="//cdn.example.org/x.js"></script>`,
		`<a href="javascript:alert(1)">`,
		`<script>fetch("/x")</script>`,
		`<script>new XMLHttpRequest()</script>`,
		`<script>new WebSocket("wss://x")</script>`,
		`<script>new EventSource("/x")</script>`,
		strings.Repeat("x", maxViewBytes+1),
	}
	for _, d := range dirty {
		if b := checkViewDocument([]byte(d)); len(b) == 0 {
			t.Errorf("%.60s… passed", d)
		}
	}
}

const testViewDocument = `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Inbox</title>` +
	`<style>body{font-family:system-ui}</style></head><body><main></main>` +
	`<script>window.parent.postMessage({}, "*")</script></body></html>`

// testViews serves only the inbox, as a stand-in document.
func testViews() views {
	return loadViews(fstest.MapFS{"approval-inbox.html": {Data: []byte(testViewDocument)}})
}

// connectSession connects a client to srv, advertising MCP Apps support when ui
// is set.
func connectSession(t *testing.T, srv *mcp.Server, ui bool) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	var opts *mcp.ClientOptions
	if ui {
		caps := &mcp.ClientCapabilities{}
		caps.AddExtension(uiExtension, map[string]any{"mimeTypes": []string{viewMIMEType}})
		opts = &mcp.ClientOptions{Capabilities: caps}
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "t"}, opts).Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// offlineClient is a client whose midPoint is never reached: listing tools and
// resources makes no REST call.
func offlineClient() (*midpoint.Client, midpoint.Config) {
	cfg := midpoint.Config{BaseURL: "http://127.0.0.1:1", Username: "u", Password: "p"}
	return midpoint.NewClient(cfg), cfg
}

func listToolsJSON(t *testing.T, cs *mcp.ClientSession) []byte {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A session whose client doesn't render views sees the tool list it saw
// before views existed, byte for byte.
func TestNonUISessionToolListUnchanged(t *testing.T) {
	client, cfg := offlineClient()
	before := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: version}, nil)
	registerTools(before, client, cfg)
	want := listToolsJSON(t, connectSession(t, before, false))

	got := listToolsJSON(t, connectSession(t, newMCPServerWithViews(client, cfg, testViews()), false))
	if string(got) != string(want) {
		t.Errorf("non-UI tools/list changed:\n got %s\nwant %s", got, want)
	}
	if strings.Contains(string(got), `"_meta"`) {
		t.Error("non-UI tools/list carries _meta")
	}
}

func TestUISessionToolLinkage(t *testing.T) {
	client, cfg := offlineClient()
	cs := connectSession(t, newMCPServerWithViews(client, cfg, testViews()), true)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	inbox := map[string]any{"resourceUri": "ui://midpoint/approval-inbox"}
	modelOnly := map[string]any{"visibility": []any{"model"}}
	want := map[string]map[string]any{
		"list_work_items":  inbox,
		"decide_work_item": inbox,
		// Their views aren't served yet: callable from views, but no link.
		"list_requestable_roles": nil, "request_role": nil, "list_my_requests": nil, "get_case": nil, "cancel_request": nil,
		"get_user_assignments": nil, "unassign_role": nil, "list_my_team": nil,
		"list_my_managers": nil, "whoami": nil, "claim_work_item": nil, "release_work_item": nil, "list_request_targets": nil,
		// Every other tool is the agent's alone.
		"ping": modelOnly, "search_users": modelOnly, "get_user": modelOnly, "list_roles": modelOnly,
		"get_role": modelOnly, "list_resources": modelOnly, "get_resource": modelOnly,
		"create_user": modelOnly, "enable_user": modelOnly, "disable_user": modelOnly,
		"assign_role": modelOnly, "recompute_user": modelOnly, "search_objects": modelOnly,
		"list_my_teammates": modelOnly, "search_audit": modelOnly,
		"get_my_access": modelOnly, "list_expiring_access": modelOnly, "list_recent_errors": modelOnly,
		"list_tasks": modelOnly, "get_task": modelOnly, "run_task": modelOnly, "suspend_task": modelOnly,
		"resume_task": modelOnly, "test_resource": modelOnly,
	}
	seen := map[string]bool{}
	for _, tool := range res.Tools {
		seen[tool.Name] = true
		w, ok := want[tool.Name]
		if !ok {
			t.Errorf("%s: not in this test; decide whether views may call it (contract 3.3)", tool.Name)
			continue
		}
		var got map[string]any
		if tool.Meta != nil {
			got, _ = tool.Meta["ui"].(map[string]any)
		}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("%s: _meta.ui = %v, want %v", tool.Name, got, w)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("%s: expected in tools/list", name)
		}
	}
	// The linkage tables name only real tools.
	for name := range toolViews {
		if !seen[name] {
			t.Errorf("toolViews names unknown tool %s", name)
		}
	}
	for name := range viewCalledTools {
		if !seen[name] {
			t.Errorf("viewCalledTools names unknown tool %s", name)
		}
	}
}

func TestRendersViews(t *testing.T) {
	withExt := func(settings map[string]any) *mcp.InitializeParams {
		caps := &mcp.ClientCapabilities{}
		caps.AddExtension(uiExtension, settings)
		return &mcp.InitializeParams{Capabilities: caps}
	}
	tests := []struct {
		name   string
		params *mcp.InitializeParams
		want   bool
	}{
		{"no params", nil, false},
		{"no capabilities", &mcp.InitializeParams{}, false},
		{"no extension", &mcp.InitializeParams{Capabilities: &mcp.ClientCapabilities{}}, false},
		{"extension without types", withExt(nil), false},
		{"other type only", withExt(map[string]any{"mimeTypes": []any{"text/html"}}), false},
		{"view type, decoded", withExt(map[string]any{"mimeTypes": []any{"text/html", viewMIMEType}}), true},
		{"view type, in process", withExt(map[string]any{"mimeTypes": []string{viewMIMEType}}), true},
		{"types not a list", withExt(map[string]any{"mimeTypes": viewMIMEType}), false},
	}
	for _, tc := range tests {
		if got := rendersViews(tc.params); got != tc.want {
			t.Errorf("%s: rendersViews = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestViewResources(t *testing.T) {
	client, cfg := offlineClient()
	srv := newMCPServerWithViews(client, cfg, testViews())
	ctx := context.Background()

	for _, ui := range []bool{true, false} {
		cs := connectSession(t, srv, ui)
		list, err := cs.ListResources(ctx, nil)
		if err != nil {
			t.Fatalf("ui=%v ListResources: %v", ui, err)
		}
		var uris []string
		for _, r := range list.Resources {
			uris = append(uris, r.URI)
			if r.MIMEType != viewMIMEType || r.Name != "approval_inbox" {
				t.Errorf("ui=%v listed %+v", ui, r)
			}
			if !reflect.DeepEqual(r.Meta, viewResourceMeta()) {
				t.Errorf("ui=%v listed _meta %v", ui, r.Meta)
			}
		}
		wantURIs := []string{"ui://midpoint/approval-inbox"}
		if !ui {
			wantURIs = nil
		}
		if !slices.Equal(uris, wantURIs) {
			t.Errorf("ui=%v resources/list = %v, want %v", ui, uris, wantURIs)
		}

		// Readable in every session: the document is the same for everyone.
		read, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "ui://midpoint/approval-inbox"})
		if err != nil {
			t.Fatalf("ui=%v ReadResource: %v", ui, err)
		}
		if len(read.Contents) != 1 {
			t.Fatalf("ui=%v read %d contents", ui, len(read.Contents))
		}
		c := read.Contents[0]
		if c.Text != testViewDocument || c.MIMEType != viewMIMEType || !reflect.DeepEqual(c.Meta, viewResourceMeta()) {
			t.Errorf("ui=%v read %+v", ui, c)
		}
		if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "ui://midpoint/request-access"}); err == nil {
			t.Errorf("ui=%v: a view with no document was served", ui)
		}
	}
}

// viewTools are the tools a view renders or calls (contract 4.2): their
// results lead with tool, acting and server.
var viewTools = []string{
	"cancel_request", "claim_work_item",
	"decide_work_item", "get_case", "get_user_assignments", "list_my_managers", "list_my_requests",
	"list_my_team", "list_request_targets", "list_requestable_roles", "list_work_items", "release_work_item", "request_role", "unassign_role", "whoami",
}

func TestViewToolsDeclareViewFields(t *testing.T) {
	client, cfg := offlineClient()
	cs := connectSession(t, newMCPServerWithViews(client, cfg, testViews()), false)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var got []string
	for _, tool := range res.Tools {
		b, _ := json.Marshal(tool.OutputSchema)
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(b, &schema); err != nil {
			t.Fatalf("%s: output schema: %v", tool.Name, err)
		}
		_, hasTool := schema.Properties["tool"]
		_, hasActing := schema.Properties["acting"]
		_, hasServer := schema.Properties["server"]
		if hasTool != hasActing || hasTool != hasServer {
			t.Errorf("%s: only some of tool, acting, server", tool.Name)
		}
		if hasTool {
			got = append(got, tool.Name)
			for _, f := range []string{"tool", "acting", "server"} {
				if !slices.Contains(schema.Required, f) {
					t.Errorf("%s: %s not required", tool.Name, f)
				}
			}
		}
	}
	sort.Strings(got)
	if !slices.Equal(got, viewTools) {
		t.Errorf("tools with view fields = %v, want %v", got, viewTools)
	}
	for name := range toolViews {
		if !slices.Contains(viewTools, name) {
			t.Errorf("%s renders a view but its result lacks the view fields", name)
		}
	}
	for name := range viewCalledTools {
		if !slices.Contains(viewTools, name) {
			t.Errorf("%s is called by views but its result lacks the view fields", name)
		}
	}
}

// connectViewRequests connects a client to the request tools against the case
// fake, with the server block cfg describes.
func connectViewRequests(t *testing.T, cfg midpoint.Config) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "t"}, nil)
	registerRequestTools(server, midpoint.NewClient(cfg), cfg.AllowWrites, newServerInfo(cfg))
	registerIdentityTools(server, midpoint.NewClient(cfg), newServerInfo(cfg))
	return connectSession(t, server, false)
}

func TestViewFieldsOnResults(t *testing.T) {
	srv, reqs := mockMidpointCases(t)
	cs := connectViewRequests(t, midpoint.Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	for _, call := range []struct {
		tool string
		args map[string]any
	}{
		{"list_work_items", map[string]any{}},
		{"get_case", map[string]any{"oid": "case-1"}},
		{"decide_work_item", map[string]any{"caseOid": "case-1", "userName": "selfuser", "roleName": "Superuser", "workItemId": "1", "decision": "approve"}},
		{"whoami", map[string]any{}},
	} {
		*reqs = nil
		out := callTool(t, cs, call.tool, call.args)
		if out["tool"] != call.tool {
			t.Errorf("%s: tool = %v", call.tool, out["tool"])
		}
		acting, _ := out["acting"].(map[string]any)
		wantActing := map[string]any{
			"oid": "u-self", "name": "selfuser", "mode": midpoint.ModePersonal,
			"impersonated": false, "sharedCredential": false, "orgs": []any{},
		}
		if !reflect.DeepEqual(acting, wantActing) {
			t.Errorf("%s: acting = %v, want %v", call.tool, acting, wantActing)
		}
		wantServer := map[string]any{
			"writesEnabled": false, "requireRequestable": true,
			"uiContract": uiContractVersion, "version": version,
		}
		if !reflect.DeepEqual(out["server"], wantServer) {
			t.Errorf("%s: server = %v, want %v", call.tool, out["server"], wantServer)
		}
		// The acting identity and the tool's own self lookup share one read.
		selfReads := 0
		for _, r := range *reqs {
			if r.method == http.MethodGet && r.path == "/ws/rest/self" {
				selfReads++
			}
		}
		if selfReads != 1 {
			t.Errorf("%s: %d reads of /self, want 1", call.tool, selfReads)
		}
	}
}

// acting describes a shared account as one instead of refusing.
func TestActingSharedCredential(t *testing.T) {
	srv, _ := mockMidpointCases(t)
	cfg := midpoint.Config{BaseURL: srv.URL, Username: "u", Password: "p"}
	cfg.File.Identity.CredentialIsShared = true
	cs := connectViewRequests(t, cfg)

	out := callTool(t, cs, "whoami", map[string]any{})
	acting, _ := out["acting"].(map[string]any)
	if acting["sharedCredential"] != true {
		t.Errorf("acting = %v, want sharedCredential true", acting)
	}
	// The self-scoped tool still refuses, as before.
	if msg := callToolErr(t, cs, "list_work_items", map[string]any{}); !strings.Contains(msg, "shared/technical account") {
		t.Errorf("list_work_items refusal = %q", msg)
	}
}

func TestReadableChat(t *testing.T) {
	client, cfg := offlineClient()
	cs := connectSession(t, newMCPServer(client, cfg), false)
	if got := cs.InitializeResult().Instructions; got != serverInstructions {
		t.Errorf("instructions = %q", got)
	}
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range res.Tools {
		if !strings.HasSuffix(tool.Description, readableChatNote) {
			t.Errorf("%s: description lacks the readable-chat note: %q", tool.Name, tool.Description)
		}
		if strings.Count(tool.Description, readableChatNote) != 1 {
			t.Errorf("%s: readable-chat note repeated", tool.Name)
		}
	}
}
