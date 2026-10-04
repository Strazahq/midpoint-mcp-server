package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

// requestAccessSession produces real MCP results from recorded approval answers
// plus neutral, authored role search and extension-schema answers. No live
// request submission recording exists: PATCH answers 202, as midPoint does
// when an assignment delta starts approval. The cases themselves are recordings.
func requestAccessSession(t *testing.T, manager, form, writes, granted bool) (*mcp.ClientSession, *[]recordedReq) {
	t.Helper()
	return requestAccessSessionWith(t, requestAccessOpts{manager: manager, form: form, writes: writes, granted: granted})
}

// requestAccessOpts shapes the fake midPoint of requestAccessSession. A
// refusal answers the request PATCH with its status and a recorded midPoint
// answer (D42).
type requestAccessOpts struct {
	manager, form, writes, granted bool
	refuseStatus                   int
	refuseFile                     string
	// rules lets the server's account read midPoint's request rules (D44):
	// Bob holds End user; Jane, his manager, also Team lead (people in orgs
	// she manages, without dates) and App approver (app roles as approver).
	rules bool
}

// Request rule roles of the fixtures, written as midPoint returns them.
const (
	fxEndUser     = "00000000-0000-0000-0000-000000000008"
	fxTeamLead    = "20000000-0000-0000-0000-0000000000a1"
	fxAppApprover = "20000000-0000-0000-0000-0000000000a2"
	fxRelease     = "20000000-0000-0000-0000-0000000000d1"
	fxAppArch     = "40000000-0000-0000-0000-0000000000a1"
	fxAssignURI   = "http://midpoint.evolveum.com/xml/ns/public/security/authorization-model-3#assign"
)

var fxRuleRoles = map[string]string{
	fxEndUser: `{"oid":"` + fxEndUser + `","@type":"c:RoleType","name":"End user","authorization":{"name":"assign-requestable-roles","action":"` + fxAssignURI + `","phase":"request",
		"object":{"special":"self"},"target":{"type":"#RoleType","filter":{"text":"requestable = true"}},"relation":"org:default"}}`,
	fxTeamLead: `{"oid":"` + fxTeamLead + `","@type":"c:RoleType","name":"team-lead","displayName":"Team lead","authorization":{"name":"assign-to-my-org","action":"` + fxAssignURI + `","phase":"request",
		"object":{"type":"#UserType","orgRelation":{"subjectRelation":"org:manager"}},"target":{"type":"#RoleType","filter":{"text":"requestable = true"}},"relation":"org:default",
		"exceptItem":"assignment/activation"}}`,
	fxAppApprover: `{"oid":"` + fxAppApprover + `","@type":"c:RoleType","name":"app-approver","displayName":"App approver","authorization":{"name":"approve-app-roles","action":"` + fxAssignURI + `","phase":"request",
		"object":{"special":"self"},"target":{"type":"#RoleType","archetypeRef":{"oid":"` + fxAppArch + `"}},"relation":"org:approver"}}`,
}

// fxRuleSearch answers the searches behind the preview the way midPoint does.
func fxRuleSearch(path, body string) (string, bool) {
	has := func(s string) bool { return strings.Contains(body, s) }
	only := func(oid string) bool { return !has("inOid") || has(oid) }
	var objs []string
	switch path {
	case "/ws/rest/abstractRoles/search":
		for _, oid := range regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`).FindAllString(body, -1) {
			if r, ok := fxRuleRoles[oid]; ok {
				objs = append(objs, r)
			} else {
				objs = append(objs, `{"oid":"`+oid+`","@type":"c:RoleType","name":"`+oid[len(oid)-4:]+`"}`)
			}
		}
	case "/ws/rest/roles/search":
		if has("requestable = true") {
			if only(fxDbAdmin) {
				objs = append(objs, `{"oid":"`+fxDbAdmin+`","name":"db-admin","displayName":"Database administrator","description":"Manage production databases and maintain backups.","riskLevel":"high","requestable":true}`)
			}
			if only(fxFinance) {
				objs = append(objs, `{"oid":"`+fxFinance+`","name":"finance-reports","displayName":"Finance reports","description":"Read financial reports.","requestable":true}`)
			}
		}
		if has(fxAppArch) && only(fxRelease) {
			objs = append(objs, `{"oid":"`+fxRelease+`","name":"release-manager","displayName":"Release manager","description":"Approve and ship releases.","riskLevel":"medium"}`)
		}
	case "/ws/rest/users/search":
		bob := `{"oid":"` + fxBstone + `","name":"bstone","fullName":"Bob Stone"}`
		jane := `{"oid":"` + fxJdoe + `","name":"jdoe","fullName":"Jane Doe"}`
		switch {
		case has(". inOrg"):
			objs = []string{bob, jane}
		case has(fxBstone):
			objs = []string{bob}
		case has(fxJdoe):
			objs = []string{jane}
		}
	default:
		return "", false
	}
	return `{"object":{"object":[` + strings.Join(objs, ",") + `]}}`, true
}

func requestAccessSessionWith(t *testing.T, o requestAccessOpts) (*mcp.ClientSession, *[]recordedReq) {
	t.Helper()
	manager, form, writes, granted := o.manager, o.form, o.writes, o.granted
	mp := approverPersona(t)
	self := fxBstone
	mp.self = testdataFile(t, "user_requestee.json")
	mp.users[fxJdoe] = fixtureUser(fxJdoe, "jdoe", "Jane Doe")
	mp.users[fxDlee] = testdataFile(t, "self_approver.json")
	if manager {
		self = fxJdoe
		mp.self = managerPersona(t).self
		if !o.rules {
			// Like everyone, Jane holds End user; without the rules option the
			// server's account can't read it, so the catalog falls back.
			mp.self = `{"user":{"oid":"` + fxJdoe + `","name":"jdoe","fullName":"Jane Doe",
				"parentOrgRef":{"oid":"` + fxDevOps + `","relation":"org:manager","type":"c:OrgType","targetName":"dev-ops"},
				"roleMembershipRef":{"oid":"` + fxEndUser + `","relation":"org:default","type":"c:RoleType"}}}`
			mp.users[fxJdoe] = mp.self
		}
		if o.rules {
			mp.self = `{"user":{"oid":"` + fxJdoe + `","name":"jdoe","fullName":"Jane Doe",
				"parentOrgRef":{"oid":"` + fxDevOps + `","relation":"org:manager","type":"c:OrgType","targetName":"dev-ops"},
				"roleMembershipRef":[{"oid":"` + fxEndUser + `","relation":"org:default","type":"c:RoleType"},{"oid":"` + fxTeamLead + `","relation":"org:default","type":"c:RoleType"},
				 {"oid":"` + fxAppApprover + `","relation":"org:default","type":"c:RoleType"},{"oid":"` + fxDevOps + `","relation":"org:manager","type":"c:OrgType"}]}}`
			mp.users[fxJdoe] = mp.self
		}
	}
	if o.rules {
		mp.roles[fxRelease] = `{"role":{"oid":"` + fxRelease + `","name":"release-manager","displayName":"Release manager"}}`
	}
	var calls []recordedReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if o.rules && r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			if answer, ok := fxRuleSearch(r.URL.Path, string(b)); ok {
				if r.URL.Path == "/ws/rest/abstractRoles/search" && r.Header.Get(midpoint.SwitchToPrincipalHeader) != "" {
					t.Error("request rules read as the person")
				}
				_, _ = io.WriteString(w, answer)
				return
			}
			r.Body = io.NopCloser(strings.NewReader(string(b)))
		}
		switch r.URL.Path {
		case "/ws/rest/schemas", "/ws/schema":
			if r.Header.Get(midpoint.SwitchToPrincipalHeader) != "" {
				t.Error("schema read impersonated")
			}
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, schemaAnswer(r.URL.Path, !form))
			return
		case "/ws/rest/lookupTables/70000000-0000-0000-0000-000000000001":
			_, _ = io.WriteString(w, recording("lookup_table_regions.json"))
			return
		case "/ws/rest/roles/search":
			_, _ = io.WriteString(w, `{"object":{"object":[{"oid":"`+fxDbAdmin+`","name":"db-admin","displayName":"Database administrator","description":"Manage production databases and maintain backups.","riskLevel":"high","requestable":true},{"oid":"`+fxFinance+`","name":"finance-reports","displayName":"Finance reports","description":"Read financial reports.","requestable":true}]}}`)
			return
		case "/ws/rest/users/search":
			b, _ := io.ReadAll(r.Body)
			if strings.Contains(string(b), "manager") {
				_, _ = io.WriteString(w, `{"object":[{"oid":"`+fxJdoe+`","name":"jdoe","fullName":"Jane Doe"}]}`)
			} else {
				_, _ = io.WriteString(w, `{"object":[{"oid":"`+fxBstone+`","name":"bstone","fullName":"Bob Stone"}]}`)
			}
			return
		case "/ws/rest/cases/search":
			if granted {
				_, _ = io.WriteString(w, `{"object":[]}`)
				return
			}
		}
		if r.Method == "PATCH" {
			b, _ := io.ReadAll(r.Body)
			calls = append(calls, recordedReq{r.Method, r.URL.Path, string(b)})
			if o.refuseStatus != 0 {
				w.WriteHeader(o.refuseStatus)
				_, _ = io.WriteString(w, testdataFile(t, o.refuseFile))
				return
			}
			w.WriteHeader(202)
			return
		}
		mp.serve(w, r)
	}))
	t.Cleanup(srv.Close)
	cfg := midpoint.Config{BaseURL: srv.URL, AllowWrites: writes}
	client := midpoint.NewClient(cfg)
	if err := client.LoadRequestForm(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	server := newMCPServer(client, cfg)
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			return next(midpoint.WithPrincipal(ctx, self), method, req)
		}
	})
	return connectSession(t, server, true), &calls
}

func TestRequestAccessResults(t *testing.T) {
	for _, writes := range []bool{false, true} {
		for _, granted := range []bool{false, true} {
			cs, calls := requestAccessSession(t, false, true, writes, granted)
			out := callTool(t, cs, "list_requestable_roles", map[string]any{"limit": 2, "query": " database "})
			if out["limitReached"] != true || out["query"] != "database" || out["form"] == nil {
				t.Fatalf("catalog %v", out)
			}
			textRes, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_requestable_roles", Arguments: map[string]any{}})
			if err != nil {
				t.Fatal(err)
			}
			text := textRes.Content[0].(*mcp.TextContent).Text
			if !strings.HasPrefix(text, "Found 2 requestable role(s).\nRoles:\n") || !strings.Contains(text, "[untrusted description") || !strings.Contains(text, "Request form fields:\n") {
				t.Error(text)
			}
			args := map[string]any{"roleOid": fxDbAdmin, "roleName": "db-admin", "validFrom": "2099-10-01T00:00:00+02:00", "validTo": "2099-10-31T23:59:59+01:00", "fields": map[string]any{"projectCode": "OPS-7", "acknowledged": false, "justification": "Review access"}}
			out = callTool(t, cs, "request_role", args)
			req := out["request"].(map[string]any)
			want := "preview"
			if writes {
				want = "pending-approval"
				if granted {
					want = "granted"
				}
			}
			if req["outcome"] != want || req["validity"] == nil || req["fields"] == nil || req["approvers"] == nil {
				t.Fatalf("request %v", req)
			}
			if req["user"].(map[string]any)["displayName"] != "Bob Stone" {
				t.Errorf("user %v", req["user"])
			}
			if writes && len(*calls) != 1 || !writes && len(*calls) != 0 {
				t.Errorf("writes %v", *calls)
			}
		}
	}
}

func TestRequestAccessRefusalsBeforeWrite(t *testing.T) {
	for _, writes := range []bool{false, true} {
		cs, calls := requestAccessSession(t, false, true, writes, false)
		for _, tc := range []struct {
			args        map[string]any
			code, field string
		}{
			{map[string]any{"roleOid": fxDbAdmin, "roleName": "db-admin"}, "invalid-field", "projectCode"},
			{map[string]any{"roleOid": fxDbAdmin, "roleName": "db-admin", "validTo": "yesterday"}, "invalid-validity", ""},
			{map[string]any{"roleOid": fxDbAdmin, "roleName": "db-admin", "fields": map[string]any{"extra": "ignored?"}}, "invalid-field", "extra"},
		} {
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "request_role", Arguments: tc.args})
			if err != nil {
				t.Fatal(err)
			}
			meta, _ := res.Meta[errorMetaKey].(map[string]any)
			if !res.IsError || meta["code"] != tc.code || (tc.field != "" && meta["field"] != tc.field) {
				t.Fatalf("error %+v", res)
			}
		}
		if len(*calls) > 0 {
			t.Errorf("refusal wrote %v", *calls)
		}
	}
}

func TestWriteRequestAccessViewFixtures(t *testing.T) {
	if os.Getenv(envWriteViewFixtures) == "" {
		t.Skip("set MIDPOINT_MCP_WRITE_VIEW_FIXTURES=1")
	}
	for _, tc := range []struct {
		name, tool                              string
		args                                    map[string]any
		manager, form, writes, granted, isError bool
		refuseStatus                            int
		refuseFile                              string
		rules                                   bool
	}{
		{name: "catalog", tool: "list_requestable_roles", writes: true},
		{name: "manager", tool: "list_requestable_roles", manager: true, writes: true},
		{name: "report", tool: "list_requestable_roles", args: map[string]any{"forUser": fxBstone, "limit": 100}, manager: true, writes: true},
		{name: "limited", tool: "list_requestable_roles", args: map[string]any{"limit": 2}, writes: true},
		{name: "form", tool: "list_requestable_roles", form: true, writes: true},
		{name: "dry-catalog", tool: "list_requestable_roles"},
		{name: "team", tool: "list_my_team", manager: true, writes: true},
		{name: "managers", tool: "list_my_managers", writes: true},
		{name: "managers-manager", tool: "list_my_managers", manager: true, writes: true},
		{name: "identity", tool: "whoami", writes: true},
		{name: "pending", tool: "request_role", args: map[string]any{"roleOid": fxDbAdmin, "roleName": "db-admin"}, writes: true},
		{name: "granted", tool: "request_role", args: map[string]any{"roleOid": fxDbAdmin, "roleName": "db-admin"}, writes: true, granted: true},
		{name: "preview", tool: "request_role", args: map[string]any{"roleOid": fxDbAdmin, "roleName": "db-admin"}},
		{name: "case", tool: "get_case", args: map[string]any{"oid": fxCaseTwoStep}, writes: true},
		{name: "invalid-field", tool: "request_role", args: map[string]any{"roleOid": fxDbAdmin, "roleName": "db-admin"}, form: true, writes: true, isError: true},
		{name: "refused", tool: "request_role", args: map[string]any{"roleOid": fxDbAdmin, "roleName": "db-admin"}, writes: true, isError: true,
			refuseStatus: 409, refuseFile: "request_refused_policy.json"},
		{name: "refused-not-authorized", tool: "request_role", args: map[string]any{"roleOid": fxDbAdmin, "roleName": "db-admin"}, writes: true, isError: true,
			refuseStatus: 403, refuseFile: "request_refused_authorization.json"},
		// D44: the catalog and "who for" from midPoint's request rules.
		{name: "rules", tool: "list_requestable_roles", writes: true, rules: true},
		{name: "rules-manager", tool: "list_requestable_roles", manager: true, writes: true, rules: true},
		{name: "rules-report", tool: "list_requestable_roles", args: map[string]any{"forUser": fxBstone, "limit": 100}, manager: true, writes: true, rules: true},
		{name: "targets", tool: "list_request_targets", manager: true, writes: true, rules: true},
		{name: "targets-self", tool: "list_request_targets", writes: true, rules: true},
		{name: "pending-approver", tool: "request_role", args: map[string]any{"roleOid": fxRelease, "roleName": "release-manager", "relation": "approver"}, manager: true, writes: true, rules: true},
		{name: "relation-refused", tool: "request_role", args: map[string]any{"roleOid": fxFinance, "roleName": "finance-reports", "relation": "approver"}, manager: true, writes: true, rules: true, isError: true},
		{name: "invalid-validity", tool: "request_role", args: map[string]any{"roleOid": fxDbAdmin, "roleName": "db-admin", "validTo": "yesterday"}, writes: true, isError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs, _ := requestAccessSessionWith(t, requestAccessOpts{manager: tc.manager, form: tc.form, writes: tc.writes, granted: tc.granted,
				refuseStatus: tc.refuseStatus, refuseFile: tc.refuseFile, rules: tc.rules})
			if tc.args == nil {
				tc.args = map[string]any{}
			}
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
			if err != nil {
				t.Fatal(err)
			}
			if res.IsError != tc.isError {
				t.Fatalf("error %+v", res)
			}
			fixture := struct {
				Call   map[string]any      `json:"call"`
				About  string              `json:"about"`
				Result *mcp.CallToolResult `json:"result"`
			}{map[string]any{"name": tc.tool, "arguments": tc.args}, "Server UI session against recorded approval answers and neutral authored catalog/schema answers: " + tc.name, res}
			var b bytes.Buffer
			e := json.NewEncoder(&b)
			e.SetEscapeHTML(false)
			e.SetIndent("", "  ")
			if err := e.Encode(fixture); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(b.String(), "127.0.0.1") {
				t.Fatal("address leaked")
			}
			if err := os.WriteFile(filepath.Join(viewFixtureDir, "request-access."+tc.name+".json"), b.Bytes(), 0644); err != nil {
				t.Fatal(err)
			}
		})
	}
}
