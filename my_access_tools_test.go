package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

// mockMidpointMyAccess serves a manager ("me", managing org-mgd) with one
// report, each with assignments that end at different times, and the roles.
func mockMidpointMyAccess(t *testing.T) *httptest.Server {
	t.Helper()
	day := func(n int) string {
		return time.Now().Add(time.Duration(n)*24*time.Hour + time.Hour).UTC().Format(time.RFC3339)
	}
	users := map[string]string{
		"me": `{"user":{"oid":"me","name":"jdoe","fullName":"Jane Doe",
			"parentOrgRef":[{"oid":"org-mgd","type":"c:OrgType","relation":"org:manager"}],
			"assignment":[{"@id":1,"targetRef":{"oid":"role-fin","type":"c:RoleType"},"activation":{"effectiveStatus":"enabled","validTo":"` + day(3) + `"}},
				{"@id":2,"targetRef":{"oid":"role-db","type":"c:RoleType"},"activation":{"effectiveStatus":"enabled"},
				 "@metadata":{"process":{"requestorRef":{"oid":"me","type":"c:UserType"},"createApproverRef":{"oid":"r1","type":"c:UserType"},"createApprovalComment":"ok"}}}]}}`,
		"r1": `{"user":{"oid":"r1","name":"bstone","fullName":"Bob Stone",
			"assignment":[{"@id":1,"targetRef":{"oid":"role-db","type":"c:RoleType"},"activation":{"effectiveStatus":"enabled","validTo":"` + day(12) + `"}},
				{"@id":2,"targetRef":{"oid":"role-fin","type":"c:RoleType"},"activation":{"effectiveStatus":"enabled","validTo":"` + day(90) + `"}},
				{"@id":3,"targetRef":{"oid":"role-old","type":"c:RoleType"},"activation":{"effectiveStatus":"disabled","validTo":"2020-01-01T00:00:00Z"}}]}}`,
	}
	roles := map[string]string{
		"role-fin": `{"role":{"oid":"role-fin","name":"finance-reports","displayName":"Finance reports"}}`,
		"role-db":  `{"role":{"oid":"role-db","name":"db-admin","displayName":"Database admin"}}`,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/rest/self", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, users["me"]) })
	mux.HandleFunc("GET /ws/rest/users/{oid}", func(w http.ResponseWriter, r *http.Request) {
		if b, ok := users[r.PathValue("oid")]; ok {
			_, _ = io.WriteString(w, b)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	})
	mux.HandleFunc("GET /ws/rest/roles/{oid}", func(w http.ResponseWriter, r *http.Request) {
		if b, ok := roles[r.PathValue("oid")]; ok {
			_, _ = io.WriteString(w, b)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("POST /ws/rest/users/search", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "org-mgd") {
			_, _ = io.WriteString(w, `{"object":[{"oid":"me","name":"jdoe"},{"oid":"r1","name":"bstone","fullName":"Bob Stone"},{"oid":"r9","name":"hidden"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"object":[]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func connectMyAccess(t *testing.T, srv *httptest.Server) *mcp.ClientSession {
	t.Helper()
	client := midpoint.NewClient(midpoint.Config{BaseURL: srv.URL, Username: "u", Password: "p"})
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "t"}, nil)
	registerMyAccessTools(server, client)
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), t1, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "t"}, nil).Connect(context.Background(), t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestGetMyAccess(t *testing.T) {
	cs := connectMyAccess(t, mockMidpointMyAccess(t))
	out, text := callToolText(t, cs, "get_my_access", map[string]any{})
	if !strings.HasPrefix(text, "You (jdoe) have 2 direct assignment(s)") {
		t.Fatalf("text = %q", text)
	}
	as := out["assignments"].([]any)
	origin := as[1].(map[string]any)["origin"].(map[string]any)
	if origin["approvedBy"].([]any)[0].(map[string]any)["displayName"] != "Bob Stone" {
		t.Errorf("origin = %v", origin)
	}
	if !strings.Contains(text, "approvedBy=bstone") || !strings.Contains(text, "[untrusted") || !strings.Contains(text, "ok") {
		t.Errorf("text lacks the origin or the untrusted comment: %q", text)
	}
}

func TestListExpiringAccess(t *testing.T) {
	cs := connectMyAccess(t, mockMidpointMyAccess(t))
	out, text := callToolText(t, cs, "list_expiring_access", map[string]any{})
	items := out["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v", items)
	}
	first, second := items[0].(map[string]any), items[1].(map[string]any)
	if first["user"].(map[string]any)["name"] != "jdoe" || first["target"].(map[string]any)["displayName"] != "Finance reports" || first["daysLeft"] != float64(3) {
		t.Errorf("first = %v", first)
	}
	if second["user"].(map[string]any)["name"] != "bstone" || second["target"].(map[string]any)["name"] != "db-admin" || second["daysLeft"] != float64(12) {
		t.Errorf("second = %v", second)
	}
	if out["people"] != float64(3) || len(out["unreadable"].([]any)) != 1 {
		t.Errorf("people = %v, unreadable = %v", out["people"], out["unreadable"])
	}
	for _, want := range []string{"2 assignment(s) end within 30 days, checked for 3 people as jdoe.", `- "Jane Doe: Finance reports" validTo=`, `- "Bob Stone: Database admin" validTo=`, "Not readable as you:", "r9"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q: %q", want, text)
		}
	}

	out, _ = callToolText(t, cs, "list_expiring_access", map[string]any{"days": 100, "includeSelf": false})
	if items := out["items"].([]any); len(items) != 2 || out["people"] != float64(2) {
		t.Errorf("100 days without self: people %v, items %v", out["people"], items)
	}
	if msg := callToolErr(t, cs, "list_expiring_access", map[string]any{"days": 400}); !strings.Contains(msg, "between 1 and 365") {
		t.Errorf("days 400: %q", msg)
	}
}
