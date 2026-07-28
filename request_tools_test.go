package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const caseJSONBody = `{
	"oid":"case-1","name":"req","state":"open",
	"objectRef":{"oid":"u-self","type":"c:UserType","targetName":"selfuser"},
	"targetRef":{"oid":"role-su","type":"c:RoleType","targetName":"Superuser"},
	"requestorRef":{"oid":"u-self","type":"c:UserType","targetName":"selfuser"},
	"workItem":[{"@id":1,"assigneeRef":{"oid":"u-self","type":"c:UserType","targetName":"selfuser"},"stageNumber":1}]
}`

func mockMidpointCases(t *testing.T) (*httptest.Server, *[]recordedReq) {
	t.Helper()
	var reqs []recordedReq
	rec := func(status int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			reqs = append(reqs, recordedReq{r.Method, r.URL.Path, string(b)})
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/rest/self", rec(200, `{"user":{"oid":"u-self","name":"selfuser"}}`))
	mux.HandleFunc("PATCH /ws/rest/users/{oid}", rec(202, ""))
	// request_role checks the catalog first: role-su is offered for request,
	// role-priv is not.
	mux.HandleFunc("GET /ws/rest/roles/role-su", rec(200, `{"role":{"oid":"role-su","name":"Superuser","requestable":true}}`))
	mux.HandleFunc("GET /ws/rest/roles/role-priv", rec(200, `{"role":{"oid":"role-priv","name":"Privileged"}}`))
	mux.HandleFunc("POST /ws/rest/cases/search", rec(200, `{"object":[`+caseJSONBody+`]}`))
	mux.HandleFunc("GET /ws/rest/cases/{oid}", rec(200, `{"case":`+caseJSONBody+`}`))
	mux.HandleFunc("POST /ws/rest/cases/{caseOid}/workItems/{wid}/complete", rec(204, ""))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &reqs
}

func connectRequests(t *testing.T, srv *httptest.Server, allowWrites bool) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	client := midpoint.NewClient(midpoint.Config{BaseURL: srv.URL, Username: "u", Password: "p"})
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "t"}, nil)
	registerRequestTools(server, client, allowWrites)

	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	mc := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "t"}, nil)
	cs, err := mc.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestRequestToolsRoundTrip(t *testing.T) {
	srv, _ := mockMidpointCases(t)
	cs := connectRequests(t, srv, true) // gate ON

	calls := []struct {
		tool string
		args map[string]any
	}{
		{"request_role", map[string]any{"roleOid": "role-su"}},
		{"list_my_requests", map[string]any{}},
		{"list_work_items", map[string]any{}},
		{"get_case", map[string]any{"oid": "case-1"}},
		{"approve_work_item", map[string]any{"caseOid": "case-1", "workItemId": "1"}},
		{"reject_work_item", map[string]any{"caseOid": "case-1", "workItemId": "1"}},
	}
	for _, c := range calls {
		out := callTool(t, cs, c.tool, c.args)
		if out == nil {
			t.Errorf("%s: nil structured output", c.tool)
		}
	}
}

func TestRequestRoleSurfacesCase(t *testing.T) {
	srv, reqs := mockMidpointCases(t)
	cs := connectRequests(t, srv, true) // gate ON

	out := callTool(t, cs, "request_role", map[string]any{"roleOid": "role-su"})
	if out["applied"] != true {
		t.Errorf("applied = %v, want true", out["applied"])
	}
	if res, _ := out["result"].(string); res == "" || res[:7] != "pending" {
		t.Errorf("result = %q, want it to surface the pending approval case", out["result"])
	}
	// request_role targets the authenticated user (resolved via /self) with a PATCH.
	if findReq(*reqs, http.MethodPatch, "/ws/rest/users/u-self") == nil {
		t.Error("request_role did not PATCH the self user")
	}
}

func TestRequestWritesGateOff(t *testing.T) {
	srv, reqs := mockMidpointCases(t)
	cs := connectRequests(t, srv, false) // gate OFF

	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"request_role", map[string]any{"roleOid": "role-su"}},
		{"approve_work_item", map[string]any{"caseOid": "case-1", "workItemId": "1"}},
		{"reject_work_item", map[string]any{"caseOid": "case-1", "workItemId": "1"}},
	} {
		out := callTool(t, cs, c.tool, c.args)
		if out["dryRun"] != true || out["applied"] != false {
			t.Errorf("%s: dryRun=%v applied=%v, want preview", c.tool, out["dryRun"], out["applied"])
		}
	}

	// No mutating request may have reached midPoint.
	for _, r := range *reqs {
		if r.method == http.MethodPatch || (r.method == http.MethodPost && hasSuffix(r.path, "/complete")) {
			t.Errorf("write gate off, but a mutating request was made: %s %s", r.method, r.path)
		}
	}
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// callToolErr calls a tool expecting it to fail, and returns the error text.
func callToolErr(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if !res.IsError {
		t.Fatalf("CallTool(%s) succeeded, want refusal", name)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// A role midPoint does not offer for request must be refused rather than
// silently granted: request_role submits a plain assignment-add, and without an
// approval policy midPoint applies it immediately.
func TestRequestRoleRefusesNonRequestableRole(t *testing.T) {
	srv, reqs := mockMidpointCases(t)
	cs := connectRequests(t, srv, true) // gate ON

	msg := callToolErr(t, cs, "request_role", map[string]any{"roleOid": "role-priv"})
	for _, want := range []string{"not flagged requestable", "assign_role", "requireRequestable"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q does not mention %q", msg, want)
		}
	}
	if r := findReq(*reqs, http.MethodPatch, "/ws/rest/users/u-self"); r != nil {
		t.Error("refused request_role still PATCHed the user")
	}
}

// The refusal must also apply with the write gate off: a dry-run preview saying
// "would request" is the same false promise as executing it.
func TestRequestRoleRefusesNonRequestableInDryRun(t *testing.T) {
	srv, _ := mockMidpointCases(t)
	cs := connectRequests(t, srv, false) // gate OFF

	msg := callToolErr(t, cs, "request_role", map[string]any{"roleOid": "role-priv"})
	if !strings.Contains(msg, "not flagged requestable") {
		t.Errorf("dry-run refusal = %q", msg)
	}
}

// The guardrail is a default, not a law: a deployment that deliberately turns it
// off gets the old behavior back.
func TestRequestRoleGuardrailCanBeDisabled(t *testing.T) {
	srv, reqs := mockMidpointCases(t)
	off := false
	cfg := midpoint.Config{BaseURL: srv.URL, Username: "u", Password: "p"}
	cfg.File.Requests.RequireRequestable = &off

	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "t"}, nil)
	registerRequestTools(server, midpoint.NewClient(cfg), true)
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	mc := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "t"}, nil)
	cs, err := mc.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })

	if out := callTool(t, cs, "request_role", map[string]any{"roleOid": "role-priv"}); out["applied"] != true {
		t.Errorf("applied = %v, want true with the guardrail disabled", out["applied"])
	}
	if findReq(*reqs, http.MethodPatch, "/ws/rest/users/u-self") == nil {
		t.Error("guardrail disabled but no PATCH was made")
	}
}
