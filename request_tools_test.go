package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
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
	registerRequestTools(server, client, allowWrites, serverInfo{})

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
		{"decide_work_item", map[string]any{"caseOid": "case-1", "workItemId": "1", "decision": "approve"}},
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
		{"decide_work_item", map[string]any{"caseOid": "case-1", "workItemId": "1", "decision": "approve"}},
		{"decide_work_item", map[string]any{"caseOid": "case-1", "workItemId": "1", "decision": "reject"}},
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
	registerRequestTools(server, midpoint.NewClient(cfg), true, serverInfo{})
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

// decideMidpoint serves one open case whose work item @id 1 is the caller's and
// @id 2 someone else's. A completion is recorded on the work item and closes the
// case, so the re-read after a decision shows it, as midPoint does.
type decideMidpoint struct {
	srv     *httptest.Server
	mu      sync.Mutex
	reqs    []recordedReq
	outcome string // recorded on work item 1 by a completion
	// staysOpen keeps the case open after a completion, with a later step's
	// work item for another approver, as a multi-step approval does.
	staysOpen bool
}

const decideOutcomeNS = "http://midpoint.evolveum.com/xml/ns/public/model/approval/outcome#"

func newDecideMidpoint(t *testing.T) *decideMidpoint {
	t.Helper()
	m := &decideMidpoint{}
	record := func(r *http.Request) string {
		b, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		defer m.mu.Unlock()
		m.reqs = append(m.reqs, recordedReq{r.Method, r.URL.Path, string(b)})
		return string(b)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/rest/self", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_, _ = io.WriteString(w, `{"user":{"oid":"u-self","name":"selfuser"}}`)
	})
	mux.HandleFunc("GET /ws/rest/cases/case-1", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		m.mu.Lock()
		state, output, later := "open", "", ""
		if m.outcome != "" {
			state, output = "closed", `,"output":{"outcome":"`+m.outcome+`"},"closeTimestamp":"2026-07-01T10:00:00.000Z"`
			if m.staysOpen {
				state = "open"
				later = `,{"@id":3,"assigneeRef":{"oid":"u-next","type":"c:UserType","targetName":"Next Approver"},"stageNumber":2}`
			}
		}
		m.mu.Unlock()
		_, _ = io.WriteString(w, `{"case":{"oid":"case-1","name":"Approving Superuser for Jane","state":"`+state+`",
			"objectRef":{"oid":"u-jane","type":"c:UserType","targetName":"Jane Doe"},
			"targetRef":{"oid":"role-su","type":"c:RoleType","targetName":"Superuser"},
			"requestorRef":{"oid":"u-jane","type":"c:UserType","targetName":"Jane Doe"},
			"workItem":[
				{"@id":1,"assigneeRef":{"oid":"u-self","type":"c:UserType","targetName":"selfuser"},"stageNumber":1`+output+`},
				{"@id":2,"assigneeRef":{"oid":"u-other","type":"c:UserType","targetName":"Someone Else"},"stageNumber":1}`+later+`
			]}}`)
	})
	mux.HandleFunc("POST /ws/rest/cases/{caseOid}/workItems/{wid}/complete", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Output struct {
				Outcome string `json:"outcome"`
			} `json:"output"`
		}
		_ = json.Unmarshal([]byte(record(r)), &body)
		m.mu.Lock()
		m.outcome = body.Output.Outcome
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

func (m *decideMidpoint) requests() []recordedReq {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.reqs)
}

func (m *decideMidpoint) completions() []recordedReq {
	var out []recordedReq
	for _, r := range m.requests() {
		if r.method == http.MethodPost && hasSuffix(r.path, "/complete") {
			out = append(out, r)
		}
	}
	return out
}

// callToolText calls a tool expecting success and returns its structured output
// and its text.
func callToolText(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (map[string]any, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if res.IsError {
		t.Fatalf("CallTool(%s) tool error: %v", name, res.Content)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal structured: %v", err)
	}
	return out, b.String()
}

func TestDecideWorkItem(t *testing.T) {
	for _, tc := range []struct {
		decision, comment, outcome, verb string
	}{
		{"approve", "", "approve", "Approved"},
		{"reject", "not needed for this project", "reject", "Rejected"},
		{" Approve ", "ok by me", "approve", "Approved"},
	} {
		t.Run(tc.decision+"/"+tc.comment, func(t *testing.T) {
			mp := newDecideMidpoint(t)
			cs := connectRequests(t, mp.srv, true) // gate ON

			args := map[string]any{"caseOid": "case-1", "workItemId": "1", "decision": tc.decision}
			if tc.comment != "" {
				args["comment"] = tc.comment
			}
			out := callTool(t, cs, "decide_work_item", args)

			// Exactly one completion, carrying the outcome URI and the comment.
			done := mp.completions()
			if len(done) != 1 || done[0].path != "/ws/rest/cases/case-1/workItems/1/complete" {
				t.Fatalf("completions = %+v, want one POST to work item 1", done)
			}
			output := map[string]any{"@type": "c:AbstractWorkItemOutputType", "outcome": decideOutcomeNS + tc.outcome}
			if tc.comment != "" {
				output["comment"] = tc.comment
			}
			var got any
			if err := json.Unmarshal([]byte(done[0].body), &got); err != nil {
				t.Fatalf("completion body is not JSON: %v (%s)", err, done[0].body)
			}
			if want := map[string]any{"output": output}; !reflect.DeepEqual(got, want) {
				t.Errorf("completion body\n got: %s\nwant: %v", done[0].body, want)
			}

			// The result names the case, the recorded outcome, and the identity.
			if out["applied"] != true || out["dryRun"] != false {
				t.Errorf("applied=%v dryRun=%v", out["applied"], out["dryRun"])
			}
			if out["caseOid"] != "case-1" || out["case"] != "Approving Superuser for Jane" || out["workItemId"] != "1" {
				t.Errorf("case fields = %v / %v / %v", out["caseOid"], out["case"], out["workItemId"])
			}
			if out["decision"] != tc.outcome || out["recordedOutcome"] != tc.outcome || out["caseState"] != "closed" {
				t.Errorf("decision=%v recordedOutcome=%v caseState=%v", out["decision"], out["recordedOutcome"], out["caseState"])
			}
			if tc.comment != "" && out["comment"] != tc.comment {
				t.Errorf("comment = %v, want %q", out["comment"], tc.comment)
			}
			subj, _ := out["subject"].(map[string]any)
			if subj["name"] != "selfuser" || subj["oid"] != "u-self" || subj["mode"] != midpoint.ModePersonal {
				t.Errorf("subject = %v, want selfuser in personal mode", subj)
			}
			// The case closed: nobody is next.
			if next, ok := out["nextApprovers"].([]any); !ok || len(next) != 0 {
				t.Errorf("nextApprovers = %v, want []", out["nextApprovers"])
			}
		})
	}
}

// A decision that leaves the case open names who still has to decide, read
// back from the case after the decision.
func TestDecideWorkItemNextApprovers(t *testing.T) {
	mp := newDecideMidpoint(t)
	mp.staysOpen = true
	cs := connectRequests(t, mp.srv, true) // gate ON

	out := callTool(t, cs, "decide_work_item",
		map[string]any{"caseOid": "case-1", "workItemId": "1", "decision": "approve"})
	if out["caseState"] != "open" || out["recordedOutcome"] != "approve" {
		t.Fatalf("caseState=%v recordedOutcome=%v", out["caseState"], out["recordedOutcome"])
	}
	// This fake serves no users, so both are named from the case and marked
	// unreadable.
	want := []any{
		map[string]any{"oid": "u-other", "type": "User", "name": "Someone Else", "readable": false},
		map[string]any{"oid": "u-next", "type": "User", "name": "Next Approver", "readable": false},
	}
	if !reflect.DeepEqual(out["nextApprovers"], want) {
		t.Errorf("nextApprovers = %v, want %v", out["nextApprovers"], want)
	}
}

func TestDecideWorkItemText(t *testing.T) {
	mp := newDecideMidpoint(t)
	cs := connectRequests(t, mp.srv, true) // gate ON

	_, msg := callToolText(t, cs, "decide_work_item",
		map[string]any{"caseOid": "case-1", "workItemId": "1", "decision": "approve"})
	for _, want := range []string{"Approved work item 1", `"Approving Superuser for Jane" (case-1)`,
		"Superuser for Jane Doe", "as selfuser (personal mode)", "recorded outcome approve", "now closed"} {
		if !strings.Contains(msg, want) {
			t.Errorf("text %q does not mention %q", msg, want)
		}
	}
}

// A work item in the case that is not the caller's is refused before any
// write, with the write gate open or closed.
func TestDecideWorkItemRefusesOthersWorkItem(t *testing.T) {
	for _, gate := range []bool{true, false} {
		mp := newDecideMidpoint(t)
		cs := connectRequests(t, mp.srv, gate)

		msg := callToolErr(t, cs, "decide_work_item",
			map[string]any{"caseOid": "case-1", "workItemId": "2", "decision": "approve"})
		for _, want := range []string{"refused", "assigned to Someone Else", "not to selfuser"} {
			if !strings.Contains(msg, want) {
				t.Errorf("gate=%v: refusal %q does not mention %q", gate, msg, want)
			}
		}
		if done := mp.completions(); len(done) != 0 {
			t.Errorf("gate=%v: refused decision still completed: %+v", gate, done)
		}
	}
}

// Once decided, the work item is closed; deciding it again is refused rather
// than sent (midPoint would answer 204 and only log a warning).
func TestDecideWorkItemRefusesClosedWorkItem(t *testing.T) {
	mp := newDecideMidpoint(t)
	cs := connectRequests(t, mp.srv, true) // gate ON

	callTool(t, cs, "decide_work_item", map[string]any{"caseOid": "case-1", "workItemId": "1", "decision": "approve"})
	msg := callToolErr(t, cs, "decide_work_item", map[string]any{"caseOid": "case-1", "workItemId": "1", "decision": "reject"})
	if !strings.Contains(msg, "refused") || !strings.Contains(msg, "not open") {
		t.Errorf("refusal = %q, want the case reported as not open", msg)
	}
	if done := mp.completions(); len(done) != 1 {
		t.Errorf("got %d completions, want only the first decision", len(done))
	}
}

func TestDecideWorkItemGateOff(t *testing.T) {
	mp := newDecideMidpoint(t)
	cs := connectRequests(t, mp.srv, false) // gate OFF

	out, msg := callToolText(t, cs, "decide_work_item",
		map[string]any{"caseOid": "case-1", "workItemId": "1", "decision": "reject", "comment": "no"})
	if out["dryRun"] != true || out["applied"] != false {
		t.Errorf("dryRun=%v applied=%v, want a preview", out["dryRun"], out["applied"])
	}
	if out["method"] != http.MethodPost || out["endpoint"] != "/ws/rest/cases/case-1/workItems/1/complete" {
		t.Errorf("preview = %v %v", out["method"], out["endpoint"])
	}
	if subj, _ := out["subject"].(map[string]any); subj["name"] != "selfuser" {
		t.Errorf("preview subject = %v, want selfuser", out["subject"])
	}
	if !strings.Contains(msg, "DRY RUN") || !strings.Contains(msg, "Would reject work item 1") || !strings.Contains(msg, midpoint.EnvAllowWrites) {
		t.Errorf("preview text = %q", msg)
	}
	if done := mp.completions(); len(done) != 0 {
		t.Errorf("write gate off, but a completion was sent: %+v", done)
	}
	if next, ok := out["nextApprovers"].([]any); !ok || len(next) != 0 {
		t.Errorf("dry run nextApprovers = %v, want []", out["nextApprovers"])
	}
}

func TestDecideWorkItemRejectsUnknownDecision(t *testing.T) {
	mp := newDecideMidpoint(t)
	cs := connectRequests(t, mp.srv, true) // gate ON

	msg := callToolErr(t, cs, "decide_work_item",
		map[string]any{"caseOid": "case-1", "workItemId": "1", "decision": "maybe"})
	if !strings.Contains(msg, `"approve" or "reject"`) {
		t.Errorf("refusal = %q", msg)
	}
	// Only the acting-identity read every view tool makes may happen: the
	// case is never read and nothing is decided.
	for _, r := range mp.requests() {
		if r.method != http.MethodGet || r.path != "/ws/rest/self" {
			t.Errorf("an invalid decision reached midPoint: %+v", r)
		}
	}
}

// testdataFile reads a midPoint answer recorded on 4.10.3 (made neutral) from
// the client package's testdata.
func testdataFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("internal", "midpoint", "testdata", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}

// The recorded answers through an MCP session: the enrichment fills, the
// results still validate against the tools' outputSchema, and the server
// block says requests carry a reason.
func TestInboxDataThroughMCP(t *testing.T) {
	serve := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/rest/self", serve(testdataFile(t, "self_approver.json")))
	mux.HandleFunc("POST /ws/rest/cases/search", serve(testdataFile(t, "cases_search_approver.json")))
	mux.HandleFunc("GET /ws/rest/cases/{oid}", serve(testdataFile(t, "case_get_closed.json")))
	// Every user read answers the requestee and every role read the
	// requested role: enough to fill each field.
	mux.HandleFunc("GET /ws/rest/users/{oid}", serve(testdataFile(t, "user_requestee.json")))
	mux.HandleFunc("GET /ws/rest/roles/{oid}", serve(testdataFile(t, "role_requested.json")))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := midpoint.Config{BaseURL: srv.URL, Username: "u", Password: "p"}
	cfg.File.Requests.JustificationItem = "{http://example.com/xml/ns/access-request}justification"
	cs := connectViewRequests(t, cfg)

	out, text := callToolText(t, cs, "list_work_items", map[string]any{})
	if server, _ := out["server"].(map[string]any); server["requestReason"] != true {
		t.Errorf("server = %v, want requestReason true", out["server"])
	}
	items, _ := out["workItems"].([]any)
	if len(items) != 1 {
		t.Fatalf("workItems = %v", out["workItems"])
	}
	wc, _ := items[0].(map[string]any)["context"].(map[string]any)
	if wc["justification"] != "Needed for the quarter-end close." || wc["reason"] != "roleApprover" {
		t.Errorf("context = %v", wc)
	}
	access, _ := wc["requesteeAccess"].(map[string]any)
	roles, _ := access["roles"].([]any)
	if len(roles) != 4 {
		t.Fatalf("requesteeAccess = %v", access)
	}
	if via, _ := roles[3].(map[string]any)["via"].(map[string]any); via["name"] != "build-runner" {
		t.Errorf("included role = %v, want via build-runner", roles[3])
	}
	if !strings.Contains(text, "[untrusted justification") {
		t.Errorf("text has no justification line:\n%s", text)
	}

	out = callTool(t, cs, "get_case", map[string]any{"oid": "40000000-0000-0000-0000-000000000001"})
	stages, _ := out["stages"].([]any)
	wis, _ := out["workItems"].([]any)
	if len(stages) != 2 || len(wis) != 3 {
		t.Fatalf("stages = %v, workItems = %v", out["stages"], out["workItems"])
	}
	delegated, _ := wis[1].(map[string]any)
	if as, _ := delegated["assignees"].([]any); len(as) != 2 || delegated["performer"] == nil || delegated["comment"] == "" {
		t.Errorf("delegated work item = %v", delegated)
	}
	if _, ok := out["nextApprovers"]; ok {
		t.Error("get_case returns nextApprovers")
	}
}
