package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

// A person whose roles let them request the requestable roles as member and
// the app roles as approver, through the server as main assembles it, acting
// as that person (D44, D45).
func previewSession(t *testing.T, writes bool) (*mcp.ClientSession, *[]string) {
	t.Helper()
	const assign = "http://midpoint.evolveum.com/xml/ns/public/security/authorization-model-3#assign"
	roles := map[string]string{
		"r-enduser": `{"oid":"r-enduser","@type":"c:RoleType","name":"end-user","displayName":"End user","authorization":{"name":"assign-requestable-roles",
			"action":"` + assign + `","phase":"request","object":{"special":"self"},"target":{"type":"#RoleType","filter":{"text":"requestable = true"}},"relation":"org:default"}}`,
		"r-appapprover": `{"oid":"r-appapprover","@type":"c:RoleType","name":"app-approver","displayName":"App approver","authorization":{"name":"approve-app-roles",
			"action":"` + assign + `","phase":"request","object":{"special":"self"},"target":{"type":"#RoleType","archetypeRef":{"oid":"arch-app"}},"relation":"org:approver"}}`,
	}
	me := `{"oid":"u-amy","name":"amy","fullName":"Amy Example","roleMembershipRef":[{"oid":"r-enduser","relation":"org:default","type":"c:RoleType"},{"oid":"r-appapprover","relation":"org:default","type":"c:RoleType"}]}`
	finance := `{"oid":"role-finance","name":"finance-reports","displayName":"Finance reports","requestable":true}`
	release := `{"oid":"role-release","name":"release-manager","displayName":"Release manager"}`
	var mu sync.Mutex
	var patches []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		b, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodGet && (r.URL.Path == "/ws/rest/users/u-amy" || r.URL.Path == "/ws/rest/self"):
			_, _ = io.WriteString(w, `{"user":`+me+`}`)
		case r.Method == http.MethodGet && r.URL.Path == "/ws/rest/roles/role-finance":
			_, _ = io.WriteString(w, `{"role":`+finance+`}`)
		case r.Method == http.MethodGet && r.URL.Path == "/ws/rest/roles/role-release":
			_, _ = io.WriteString(w, `{"role":`+release+`}`)
		case r.Method == http.MethodPost && r.URL.Path == "/ws/rest/abstractRoles/search":
			var objs []string
			for oid, body := range roles {
				if strings.Contains(string(b), oid) {
					objs = append(objs, body)
				}
			}
			_, _ = io.WriteString(w, `{"object":{"object":[`+strings.Join(objs, ",")+`]}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/ws/rest/roles/search":
			var objs []string
			only := func(oid string) bool {
				return !strings.Contains(string(b), "inOid") || strings.Contains(string(b), oid)
			}
			if strings.Contains(string(b), "requestable = true") && only("role-finance") {
				objs = append(objs, finance)
			}
			if strings.Contains(string(b), "arch-app") && only("role-release") {
				objs = append(objs, release)
			}
			_, _ = io.WriteString(w, `{"object":{"object":[`+strings.Join(objs, ",")+`]}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/ws/rest/users/search":
			_, _ = io.WriteString(w, `{"object":{"object":[`+me+`]}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/ws/rest/cases/search":
			_, _ = io.WriteString(w, `{"object":{"object":[]}}`)
		case r.Method == http.MethodPatch:
			mu.Lock()
			patches = append(patches, string(b))
			mu.Unlock()
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	cfg := midpoint.Config{BaseURL: srv.URL, Username: "svc", Password: "p", AllowWrites: writes}
	server := newMCPServerWithViews(midpoint.NewClient(cfg), cfg, testViews())
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			return next(midpoint.WithPrincipal(ctx, "u-amy"), method, req)
		}
	})
	return connectSession(t, server, false), &patches
}

func TestRequestCatalogFromRules(t *testing.T) {
	cs, _ := previewSession(t, false)
	out, text := callToolText(t, cs, "list_requestable_roles", map[string]any{})
	if p, _ := out["preview"].(map[string]any); p["basis"] != "rules" {
		t.Fatalf("preview = %v", out["preview"])
	}
	roles, _ := out["roles"].([]any)
	if len(roles) != 2 {
		t.Fatalf("roles = %v", roles)
	}
	byName := map[string]map[string]any{}
	for _, r := range roles {
		m := r.(map[string]any)
		byName[m["name"].(string)] = m
	}
	if offers, _ := json.Marshal(byName["release-manager"]["offers"]); string(offers) != `[{"allFields":true,"because":["App approver › approve-app-roles"],"relation":"approver","validity":true}]` {
		t.Errorf("release offers %s", offers)
	}
	for _, want := range []string{
		"Found 2 role(s) your midPoint request rules let you request.",
		`- finance-reports oid=role-finance displayName="Finance reports" relations=default offers="default: fields=all, dates=true, because=End user › assign-requestable-roles"`,
		`- release-manager oid=role-release displayName="Release manager" relations=approver offers="approver: fields=all, dates=true, because=App approver › approve-app-roles"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text has no %q:\n%s", want, text)
		}
	}
	tg, ttext := callToolText(t, cs, "list_request_targets", map[string]any{})
	if people, _ := tg["people"].([]any); len(people) != 1 || !strings.Contains(ttext, `- amy oid=u-amy fullName="Amy Example" because="End user › assign-requestable-roles; App approver › approve-app-roles"`) {
		t.Errorf("targets %v\n%s", tg["people"], ttext)
	}
}

// request_role sends the relation the rules offer, and refuses one they don't.
func TestRequestRoleRelation(t *testing.T) {
	cs, patches := previewSession(t, true)
	out := callTool(t, cs, "request_role", map[string]any{"roleOid": "role-release", "roleName": "release-manager", "relation": "approver"})
	if req, _ := out["request"].(map[string]any); req["relation"] != "approver" {
		t.Errorf("request = %v", out["request"])
	}
	if len(*patches) != 1 || !strings.Contains((*patches)[0], `"relation":"org:approver"`) {
		t.Errorf("patch %v", *patches)
	}
	text, payload := callToolCode(t, cs, "request_role", map[string]any{"roleOid": "role-finance", "roleName": "finance-reports", "relation": "approver"})
	if payload["code"] != midpoint.CodeNotRequestable || !strings.Contains(text, "is not offered as approver for amy by midPoint's request rules, which offer only default") {
		t.Errorf("refusal %q %v", text, payload)
	}
	if len(*patches) != 1 {
		t.Errorf("a refused request was sent: %v", *patches)
	}
}
