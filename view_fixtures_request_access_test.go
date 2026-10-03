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
	mp := approverPersona(t)
	self := fxBstone
	mp.self = testdataFile(t, "user_requestee.json")
	mp.users[fxJdoe] = fixtureUser(fxJdoe, "jdoe", "Jane Doe")
	mp.users[fxDlee] = testdataFile(t, "self_approver.json")
	if manager {
		self = fxJdoe
		mp.self = managerPersona(t).self
	}
	var calls []recordedReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/ws/rest/schemas":
			if r.Header.Get(midpoint.SwitchToPrincipalHeader) != "" {
				t.Error("schema read impersonated")
			}
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, testdataFile(t, "request_schema_db.xml"))
			return
		case "/ws/schema":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<schemaFiles xmlns="http://midpoint.evolveum.com/xml/ns/public/common/common-3"/>`)
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
			w.WriteHeader(202)
			return
		}
		mp.serve(w, r)
	}))
	t.Cleanup(srv.Close)
	cfg := midpoint.Config{BaseURL: srv.URL, AllowWrites: writes}
	if form {
		cfg.File.Requests.JustificationItem = fixtureJustificationItem
		for _, n := range []string{"justification", "projectCode", "ticket", "acknowledged", "neededOn", "handover"} {
			cfg.File.Requests.FormItems = append(cfg.File.Requests.FormItems, "{http://example.com/xml/ns/access-request}"+n)
		}
	}
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
	}{
		{name: "catalog", tool: "list_requestable_roles", writes: true},
		{name: "manager", tool: "list_requestable_roles", manager: true, writes: true},
		{name: "report", tool: "list_requestable_roles", args: map[string]any{"forUser": fxBstone, "limit": 100}, manager: true, writes: true},
		{name: "limited", tool: "list_requestable_roles", args: map[string]any{"limit": 2}, writes: true},
		{name: "form", tool: "list_requestable_roles", form: true, writes: true},
		{name: "dry-catalog", tool: "list_requestable_roles"},
		{name: "team", tool: "list_my_team", manager: true, writes: true},
		{name: "managers", tool: "list_my_managers", writes: true},
		{name: "identity", tool: "whoami", writes: true},
		{name: "pending", tool: "request_role", args: map[string]any{"roleOid": fxDbAdmin, "roleName": "db-admin"}, writes: true},
		{name: "granted", tool: "request_role", args: map[string]any{"roleOid": fxDbAdmin, "roleName": "db-admin"}, writes: true, granted: true},
		{name: "preview", tool: "request_role", args: map[string]any{"roleOid": fxDbAdmin, "roleName": "db-admin"}},
		{name: "case", tool: "get_case", args: map[string]any{"oid": fxCaseTwoStep}, writes: true},
		{name: "invalid-field", tool: "request_role", args: map[string]any{"roleOid": fxDbAdmin, "roleName": "db-admin"}, form: true, writes: true, isError: true},
		{name: "invalid-validity", tool: "request_role", args: map[string]any{"roleOid": fxDbAdmin, "roleName": "db-admin", "validTo": "yesterday"}, writes: true, isError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs, _ := requestAccessSession(t, tc.manager, tc.form, tc.writes, tc.granted)
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
