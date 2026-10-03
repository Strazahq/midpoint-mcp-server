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
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

// Access review fixtures are actual UI-session tool results. The recorded
// user_requestee answer supplies assignments and membership path metadata.
// access_review_user adds a directly assigned db-admin, an end date, an account
// construction and a service; access_review_objects supplies readable display
// names and descriptions in recorded REST object shapes. The removal case is
// the existing 4.10.3 recording. No live unassignment/read-back was recorded.
type accessFixture struct {
	name, tool, outcome                     string
	args                                    map[string]any
	writes, personal, shared, empty, noOrgs bool
	status                                  int
}

func accessFixtures() []accessFixture {
	args := map[string]any{"oid": fxBstone}
	revoke := map[string]any{"userOid": fxBstone, "userName": "bstone", "roleOid": fxDbAdmin, "roleName": "db-admin"}
	return []accessFixture{
		{name: "person", tool: "get_user_assignments", args: args, writes: true},
		{name: "person-personal", tool: "get_user_assignments", args: args, writes: true, personal: true},
		{name: "person-preview", tool: "get_user_assignments", args: args},
		{name: "second-person", tool: "get_user_assignments", args: map[string]any{"oid": fxMkovac}, writes: true},
		{name: "self", tool: "get_user_assignments", args: map[string]any{"oid": fxJdoe}, writes: true},
		{name: "team-preview", tool: "list_my_team", args: map[string]any{"limit": 100}},
		{name: "team-personal", tool: "list_my_team", args: map[string]any{"limit": 100}, writes: true, personal: true},
		{name: "team", tool: "list_my_team", args: map[string]any{"limit": 100}, writes: true},
		{name: "no-orgs", tool: "list_my_team", args: map[string]any{}, writes: true, noOrgs: true},
		{name: "none-visible", tool: "list_my_team", args: map[string]any{}, writes: true, empty: true},
		{name: "empty-personal", tool: "list_my_team", args: map[string]any{}, writes: true, personal: true, empty: true},
		{name: "removed", tool: "unassign_role", args: revoke, writes: true, outcome: "removed"},
		{name: "pending", tool: "unassign_role", args: revoke, writes: true, outcome: "pending-approval"},
		{name: "still", tool: "unassign_role", args: revoke, writes: true, outcome: "still-assigned"},
		{name: "preview", tool: "unassign_role", args: revoke, outcome: "preview"},
		{name: "after", tool: "get_user_assignments", args: args, writes: true, outcome: "removed"},
		{name: "whoami", tool: "whoami", args: map[string]any{}, writes: true},
		{name: "whoami-shared", tool: "whoami", args: map[string]any{}, writes: true, personal: true, shared: true},
		{name: "not-assigned", tool: "unassign_role", args: revoke, writes: true, outcome: "absent"},
		{name: "not-authorized", tool: "unassign_role", args: revoke, writes: true, status: 403},
		{name: "not-found", tool: "get_user_assignments", args: args, writes: true, status: 404},
		{name: "unavailable", tool: "get_user_assignments", args: args, writes: true, status: 503},
		{name: "shared", tool: "list_my_team", args: map[string]any{}, writes: true, personal: true, shared: true},
	}
}

func accessFixtureResult(t *testing.T, fx accessFixture) *mcp.CallToolResult {
	t.Helper()
	var user map[string]any
	if err := json.Unmarshal([]byte(testdataFile(t, "access_review_user.json")), &user); err != nil {
		t.Fatal(err)
	}
	subject := user["user"].(map[string]any)
	self := managerPersona(t).self
	if fx.noOrgs {
		self = fixtureUser(fxJdoe, "jdoe", "Jane Doe")
	}
	second := map[string]any{"oid": fxMkovac, "name": "mkovac", "fullName": "Mia Kovac", "activation": map[string]any{"effectiveStatus": "disabled"}, "parentOrgRef": map[string]any{"oid": fxDevOps, "relation": "org:default", "targetName": "dev-ops"}}
	remove := func() {
		list := []any{}
		for _, a := range subject["assignment"].([]any) {
			if a.(map[string]any)["@id"] != float64(9) {
				list = append(list, a)
			}
		}
		subject["assignment"] = list
		memberships := []any{}
		for _, m := range subject["roleMembershipRef"].([]any) {
			if m.(map[string]any)["oid"] != fxDbAdmin {
				memberships = append(memberships, m)
			}
		}
		subject["roleMembershipRef"] = memberships
	}
	if fx.outcome == "absent" || fx.name == "after" {
		remove()
	}
	var objects struct {
		Object struct {
			Object []map[string]any `json:"object"`
		} `json:"object"`
	}
	if err := json.Unmarshal([]byte(testdataFile(t, "access_review_objects.json")), &objects); err != nil {
		t.Fatal(err)
	}
	records := map[string]any{}
	for _, o := range objects.Object.Object {
		typ := strings.TrimSuffix(strings.TrimPrefix(o["@type"].(string), "c:"), "Type")
		coll := map[string]string{"Role": "roles", "Org": "orgs", "Service": "services"}[typ]
		records["/"+coll+"/"+o["oid"].(string)] = map[string]any{strings.ToLower(typ): o}
	}
	var mu sync.Mutex
	patches := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if !fx.personal && r.Header.Get("Switch-To-Principal") != fxJdoe {
			t.Error("access review request lost the acting principal")
		}
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/ws/rest")
		write := func(v any) {
			if err := json.NewEncoder(w).Encode(v); err != nil {
				t.Error(err)
			}
		}
		switch {
		case path == "/self" || path == "/users/"+fxJdoe:
			_, _ = io.WriteString(w, self)
		case path == "/users/search":
			users := []any{}
			if !fx.empty {
				users = append(users, subject, second)
			}
			write(map[string]any{"object": map[string]any{"object": users}})
		case r.Method == http.MethodPatch:
			patches++
			raw, _ := io.ReadAll(r.Body)
			var body any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Error(err)
			}
			const expected = `{"objectModification":{"itemDelta":[{"modificationType":"delete","path":"assignment","value":{"@id":9}}]}}`
			var want any
			_ = json.Unmarshal([]byte(expected), &want)
			actual, _ := json.Marshal(body)
			exp, _ := json.Marshal(want)
			if !bytes.Equal(actual, exp) || strings.Contains(string(raw), "@metadata") {
				t.Errorf("unexpected assignment delete: %s", raw)
			}
			if fx.status != 0 {
				w.WriteHeader(fx.status)
				return
			}
			if fx.outcome == "removed" {
				remove()
			}
			w.WriteHeader(http.StatusNoContent)
		case path == "/users/"+fxBstone:
			if fx.status != 0 && fx.tool == "get_user_assignments" {
				w.WriteHeader(fx.status)
				return
			}
			write(user)
		case path == "/users/"+fxMkovac:
			write(map[string]any{"user": second})
		case path == "/cases/search":
			if fx.outcome == "pending-approval" {
				_, _ = io.WriteString(w, testdataFile(t, "cases_search_removal.json"))
			} else {
				_, _ = io.WriteString(w, `{"object":{"object":[]}}`)
			}
		default:
			if o, ok := records[path]; ok {
				write(o)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		}
	}))
	t.Cleanup(srv.Close)
	cfg := midpoint.Config{BaseURL: srv.URL, AllowWrites: fx.writes}
	cfg.File.Identity.CredentialIsShared = fx.shared
	server := newMCPServerWithViews(midpoint.NewClient(cfg), cfg, embeddedViews())
	if !fx.personal {
		server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
			return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
				return next(midpoint.WithPrincipal(ctx, fxJdoe), method, req)
			}
		})
	}
	cs := connectSession(t, server, true)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: fx.tool, Arguments: fx.args})
	if err != nil {
		t.Fatal(err)
	}
	wantError := fx.status != 0 || fx.name == "not-assigned" || fx.name == "shared"
	if res.IsError != wantError {
		t.Fatalf("isError=%v want %v: %+v", res.IsError, wantError, res.Content)
	}
	if !wantError {
		sc := res.StructuredContent.(map[string]any)
		if sc["tool"] != fx.tool {
			t.Error("wrong producer")
		}
		if fx.tool == "unassign_role" && sc["revocation"].(map[string]any)["outcome"] != fx.outcome {
			t.Errorf("revocation=%v", sc["revocation"])
		}
	}
	mu.Lock()
	defer mu.Unlock()
	expectedPatches := 0
	if fx.tool == "unassign_role" && fx.writes && fx.name != "not-assigned" {
		expectedPatches = 1
	}
	if patches != expectedPatches {
		t.Errorf("PATCH calls=%d want %d", patches, expectedPatches)
	}
	return res
}

func TestAccessReviewToolResults(t *testing.T) {
	for _, fx := range accessFixtures() {
		t.Run(fx.name, func(t *testing.T) { accessFixtureResult(t, fx) })
	}
}

func TestWriteAccessReviewViewFixtures(t *testing.T) {
	if os.Getenv(envWriteViewFixtures) != "1" {
		t.Skip("set " + envWriteViewFixtures + "=1 to regenerate access-review fixtures")
	}
	for _, fx := range accessFixtures() {
		t.Run(fx.name, func(t *testing.T) {
			res := accessFixtureResult(t, fx)
			file := map[string]any{"call": map[string]any{"name": fx.tool, "arguments": fx.args}, "about": "Server UI session, recorded-style access review REST answers; scenario " + fx.name + ". See view_fixtures_access_review_test.go for provenance.", "result": res}
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetIndent("", "  ")
			enc.SetEscapeHTML(false)
			if err := enc.Encode(file); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(buf.String(), "127.0.0.1") {
				t.Fatal("fixture leaked fake server address")
			}
			if err := os.WriteFile(filepath.Join(viewFixtureDir, "access-review."+fx.name+".json"), buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
		})
	}
}
