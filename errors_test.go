package main

import (
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

// contractFallback is the text match of contract 6.8 that views use for
// servers without codes, in its order: first match wins. A coded error's text
// must classify the same way, so old and new views agree.
var contractFallback = []struct {
	code    string
	matches []string
}{
	{midpoint.CodeSharedCredential, []string{"shared/technical account"}},
	{midpoint.CodeNotRequestable, []string{"is not flagged requestable"}},
	{midpoint.CodeNotYourRequest, []string{"only the requester can withdraw"}},
	{midpoint.CodeRequestClosed, []string{", not open,"}},
	{midpoint.CodeAlreadyDecided, []string{"is already closed"}},
	{midpoint.CodeNotInInbox, []string{"has no work item", "is assigned to"}},
	{midpoint.CodeNotAssigned, []string{"has no direct assignment to"}},
	{midpoint.CodeAuditUnavailable, []string{"executeScript", "execute-script"}},
	{midpoint.CodeInvalidInput, []string{`validating "arguments"`, "decision must be"}},
	{midpoint.CodeInvalidField, []string{"invalid request field"}},
	{midpoint.CodeInvalidValidity, []string{"invalid validity"}},
	{midpoint.CodeNotAuthorized, []string{"unexpected status 401", "unexpected status 403"}},
	{midpoint.CodeNotFound, []string{"unexpected status 404"}},
	{midpoint.CodeMidpointUnavailable, []string{"unexpected status 5", "calling midPoint"}},
}

// fallbackCode classifies an error result's text as contract 6.8 does.
func fallbackCode(tool, text string) string {
	for _, f := range contractFallback {
		if f.code == midpoint.CodeAuditUnavailable && tool != "search_audit" {
			continue
		}
		for _, m := range f.matches {
			if strings.Contains(text, m) {
				return f.code
			}
		}
	}
	return midpoint.CodeInternal
}

// callToolCode calls a tool expecting an error result, and returns its text and
// the error payload from its _meta.
func callToolCode(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, map[string]any) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if !res.IsError {
		t.Fatalf("CallTool(%s) succeeded, want an error result", name)
	}
	if res.StructuredContent != nil {
		t.Errorf("%s: error result carries structuredContent %v", name, res.StructuredContent)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	payload, _ := res.Meta[errorMetaKey].(map[string]any)
	return b.String(), payload
}

// route is one canned answer of codeMidpoint.
type route struct {
	status int
	body   string
}

// codeMidpoint is a fake midPoint that answers /self as selfuser, unless
// routes say otherwise, and each route with its canned answer.
func codeMidpoint(t *testing.T, routes map[string]route) *httptest.Server {
	t.Helper()
	all := map[string]route{"GET /ws/rest/self": {200, `{"user":{"oid":"u-self","name":"selfuser"}}`}}
	for p, r := range routes {
		all[p] = r
	}
	mux := http.NewServeMux()
	for pattern, r := range all {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(r.status)
			_, _ = io.WriteString(w, r.body)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// decideCase is a GET /cases/case-1 answer with the given state and work items.
func decideCase(state string, items ...string) route {
	return route{200, `{"case":{"oid":"case-1","name":"Approving Superuser for Jane","state":"` + state + `",
		"objectRef":{"oid":"u-jane","type":"c:UserType","targetName":"Jane Doe"},
		"targetRef":{"oid":"role-su","type":"c:RoleType","targetName":"Superuser"},
		"workItem":[` + strings.Join(items, ",") + `]}}`}
}

const (
	myWorkItem     = `{"@id":1,"assigneeRef":{"oid":"u-self","type":"c:UserType","targetName":"selfuser"},"stageNumber":1}`
	myDoneWorkItem = `{"@id":1,"assigneeRef":{"oid":"u-self","type":"c:UserType","targetName":"selfuser"},"stageNumber":1,
		"output":{"outcome":"` + decideOutcomeNS + `approve"},"closeTimestamp":"2026-07-01T10:00:00.000Z"}`
	othersWorkItem = `{"@id":2,"assigneeRef":{"oid":"u-other","type":"c:UserType","targetName":"Someone Else"},"stageNumber":1}`
)

// Each code comes from its real cause, through the server main assembles, and
// the error text is what it was before codes existed.
func TestErrorCodes(t *testing.T) {
	decide := func(id string) map[string]any {
		return map[string]any{"caseOid": "case-1", "userName": "Jane Doe", "roleName": "Superuser", "workItemId": id, "decision": "approve"}
	}
	for _, tc := range []struct {
		name   string
		routes map[string]route
		shared bool // identity.credentialIsShared
		tool   string
		args   map[string]any
		code   string
		text   string // the exact text, where it is pinned
	}{
		{name: "401", routes: map[string]route{"GET /ws/rest/self": {401, ""}},
			tool: "ping", code: midpoint.CodeNotAuthorized, text: "midPoint /self: unexpected status 401 Unauthorized"},
		{name: "403", routes: map[string]route{"GET /ws/rest/self": {403, ""}},
			tool: "ping", code: midpoint.CodeNotAuthorized, text: "midPoint /self: unexpected status 403 Forbidden"},
		{name: "404", routes: map[string]route{"GET /ws/rest/cases/case-x": {404, ""}},
			tool: "get_case", args: map[string]any{"oid": "case-x"},
			code: midpoint.CodeNotFound, text: "midPoint /cases/case-x: unexpected status 404 Not Found"},
		{name: "500 behind the acting identity", routes: map[string]route{"GET /ws/rest/self": {500, ""}},
			tool: "list_work_items", code: midpoint.CodeMidpointUnavailable,
			text: "resolving the acting identity: midPoint /self: unexpected status 500 Internal Server Error"},
		{name: "400", routes: map[string]route{"GET /ws/rest/self": {400, ""}},
			tool: "ping", code: midpoint.CodeInternal, text: "midPoint /self: unexpected status 400 Bad Request"},
		{name: "shared credential", shared: true,
			tool: "list_work_items", code: midpoint.CodeSharedCredential, text: midpoint.ErrNoCallerIdentity.Error()},
		{name: "not requestable", routes: map[string]route{
			"GET /ws/rest/roles/role-priv": {200, `{"role":{"oid":"role-priv","name":"Privileged"}}`}},
			tool: "request_role", args: map[string]any{"roleOid": "role-priv", "roleName": "Privileged"}, code: midpoint.CodeNotRequestable},
		{name: "case closed", routes: map[string]route{"GET /ws/rest/cases/case-1": decideCase("closed", myDoneWorkItem)},
			tool: "decide_work_item", args: decide("1"), code: midpoint.CodeRequestClosed},
		{name: "work item closed", routes: map[string]route{"GET /ws/rest/cases/case-1": decideCase("open", myDoneWorkItem, othersWorkItem)},
			tool: "decide_work_item", args: decide("1"), code: midpoint.CodeAlreadyDecided},
		{name: "someone else's work item", routes: map[string]route{"GET /ws/rest/cases/case-1": decideCase("open", myWorkItem, othersWorkItem)},
			tool: "decide_work_item", args: decide("2"), code: midpoint.CodeNotInInbox},
		{name: "no such work item", routes: map[string]route{"GET /ws/rest/cases/case-1": decideCase("open", myWorkItem)},
			tool: "decide_work_item", args: decide("9"), code: midpoint.CodeNotInInbox},
		{name: "unknown decision",
			tool: "decide_work_item", args: map[string]any{"caseOid": "case-1", "userName": "Jane Doe", "roleName": "Superuser", "workItemId": "1", "decision": "maybe"},
			code: midpoint.CodeInvalidInput, text: `decision must be "approve" or "reject", got "maybe"`},
		{name: "missing argument",
			tool: "decide_work_item", args: map[string]any{"caseOid": "case-1", "workItemId": "1"},
			code: midpoint.CodeInvalidInput},
		{name: "wrong argument type",
			tool: "list_work_items", args: map[string]any{"limit": "ten"}, code: midpoint.CodeInvalidInput},
		{name: "not assigned", routes: map[string]route{
			"GET /ws/rest/users/u-jane": {200, `{"user":{"oid":"u-jane","name":"jane","assignment":[]}}`}},
			tool: "unassign_role", args: map[string]any{"userOid": "u-jane", "userName": "jane", "roleOid": "role-su", "roleName": "Superuser"},
			code: midpoint.CodeNotAssigned, text: "user u-jane has no direct assignment to role-su"},
		{name: "audit script refused", routes: map[string]route{"POST /ws/rest/rpc/executeScript": {403, ""}},
			tool: "search_audit", code: midpoint.CodeAuditUnavailable,
			text: "midPoint /rpc/executeScript: unexpected status 403 Forbidden"},
		{name: "undecodable answer", routes: map[string]route{"GET /ws/rest/self": {200, "not json"}},
			tool: "ping", code: midpoint.CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := midpoint.Config{BaseURL: codeMidpoint(t, tc.routes).URL, Username: "u", Password: "p"}
			cfg.File.Identity.CredentialIsShared = tc.shared
			cs := connectSession(t, newMCPServerWithViews(midpoint.NewClient(cfg), cfg, testViews()), false)

			args := tc.args
			if args == nil {
				args = map[string]any{}
			}
			text, payload := callToolCode(t, cs, tc.tool, args)
			if want := map[string]any{"v": float64(1), "code": tc.code}; !maps.Equal(payload, want) {
				t.Errorf("error payload = %v, want %v (text %q)", payload, want, text)
			}
			if tc.text != "" && text != tc.text {
				t.Errorf("text = %q, want %q", text, tc.text)
			}
			if got := fallbackCode(tc.tool, text); got != tc.code {
				t.Errorf("text %q falls back to %q, not %q", text, got, tc.code)
			}
		})
	}
}

// midPoint that can't be reached is midpoint-unavailable.
func TestErrorCodeTransportFailure(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close() // the dial fails
	cfg := midpoint.Config{BaseURL: srv.URL, Username: "u", Password: "p"}
	cs := connectSession(t, newMCPServerWithViews(midpoint.NewClient(cfg), cfg, testViews()), false)

	for _, tool := range []string{"ping", "whoami"} {
		text, payload := callToolCode(t, cs, tool, map[string]any{})
		if payload["code"] != midpoint.CodeMidpointUnavailable {
			t.Errorf("%s: payload = %v, want %s (text %q)", tool, payload, midpoint.CodeMidpointUnavailable, text)
		}
		if !strings.Contains(text, "calling midPoint GET /self: ") {
			t.Errorf("%s: text = %q", tool, text)
		}
	}
}

// Results that are not errors carry no error payload, and a JSON-RPC error
// stays one.
func TestErrorCodeOnlyOnErrors(t *testing.T) {
	cfg := midpoint.Config{BaseURL: codeMidpoint(t, nil).URL, Username: "u", Password: "p"}
	cs := connectSession(t, newMCPServerWithViews(midpoint.NewClient(cfg), cfg, testViews()), false)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "ping", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("ping: %v %v", err, res)
	}
	if _, ok := res.Meta[errorMetaKey]; ok {
		t.Errorf("successful result carries an error payload: %v", res.Meta)
	}
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "no_such_tool"}); err == nil {
		t.Error("calling an unknown tool returned a result, want a JSON-RPC error")
	}
}

// Any error without a code is internal, and a field travels with its code.
func TestErrorCodeUncodedAndField(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "t"}, nil)
	fail := func(err error) mcp.ToolHandlerFor[struct{}, pingOutput] {
		return func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, pingOutput, error) {
			return nil, pingOutput{}, err
		}
	}
	mcp.AddTool(server, &mcp.Tool{Name: "uncoded"}, fail(errors.New("boom")))
	mcp.AddTool(server, &mcp.Tool{Name: "field"}, fail(&midpoint.CodedError{
		Code: midpoint.CodeInvalidField, Field: "costCenter", Err: errors.New("invalid request field costCenter")}))
	server.AddReceivingMiddleware(errorCodes)
	cs := connectSession(t, server, false)

	text, payload := callToolCode(t, cs, "uncoded", map[string]any{})
	if text != "boom" || !maps.Equal(payload, map[string]any{"v": float64(1), "code": midpoint.CodeInternal}) {
		t.Errorf("uncoded: text %q, payload %v", text, payload)
	}
	text, payload = callToolCode(t, cs, "field", map[string]any{})
	want := map[string]any{"v": float64(1), "code": midpoint.CodeInvalidField, "field": "costCenter"}
	if text != "invalid request field costCenter" || !maps.Equal(payload, want) {
		t.Errorf("field: text %q, payload %v", text, payload)
	}
}
