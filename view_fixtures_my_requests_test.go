package main

import (
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

// requesterFixture serves recorded-style answers as the requester's identity.
// The recordings show approver-visible items only; the requester scenario adds
// the second approver's open item to the after-decision recording. Cancellation
// has not been recorded live: its closed answer models the documented REST path.
type requesterFixture struct {
	before, after             string
	writes, personal, shared  bool
	cancelStatus, afterStatus int
	posts, reads, selfReads   int
	body                      string
	principals                []string
}

func newRequesterFixture(t *testing.T) *requesterFixture {
	before := mutateCase(t, testdataFile(t, "case_get_after_decision.json"), func(c map[string]any) {
		items := caseWorkItems(c)
		c["workItem"] = []any{items[0], map[string]any{"@id": 7, "stageNumber": 1, "createTimestamp": "2026-10-01T10:53:58.967Z",
			"assigneeRef": map[string]any{"oid": fxMkovac, "type": "c:UserType", "targetName": "mkovac"}}}
	})
	after := mutateCase(t, before, func(c map[string]any) {
		c["state"] = "closed"
		c["closeTimestamp"] = "2026-10-01T12:00:00Z"
		delete(c, "outcome")
		for _, wi := range caseWorkItems(c) {
			if wi["closeTimestamp"] == nil {
				wi["closeTimestamp"] = "2026-10-01T12:00:00Z"
			}
		}
	})
	return &requesterFixture{before: before, after: after, writes: true, cancelStatus: 204}
}

func (f *requesterFixture) connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	base := managerPersona(t)
	base.self = testdataFile(t, "user_requestee.json")
	base.completed = map[string]bool{}
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		f.principals = append(f.principals, r.Header.Get("Switch-To-Principal"))
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case r.Method == "POST" && strings.HasSuffix(path, "/cancel"):
			f.posts++
			b, _ := io.ReadAll(r.Body)
			f.body = string(b)
			w.WriteHeader(f.cancelStatus)
		case r.Method == "GET" && path == "/ws/rest/cases/"+fxCaseTwoStep:
			f.reads++
			if f.posts > 0 && f.afterStatus != 0 {
				w.WriteHeader(f.afterStatus)
				return
			}
			body := f.before
			if f.posts > 0 {
				body = f.after
			}
			_, _ = io.WriteString(w, body)
		case path == "/ws/rest/cases/search":
			body := f.before
			if f.posts > 0 {
				body = f.after
			}
			var obj map[string]any
			_ = json.Unmarshal([]byte(body), &obj)
			_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{"object": []any{obj["case"]}}})
		default:
			if path == "/ws/rest/self" {
				f.selfReads++
			}
			base.serve(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	cfg := midpoint.Config{BaseURL: srv.URL, Username: "u", Password: "p", AllowWrites: f.writes}
	cfg.File.Requests.JustificationItem = fixtureJustificationItem
	cfg.File.Identity.CredentialIsShared = f.shared
	server := newMCPServerWithViews(midpoint.NewClient(cfg), cfg, testViews())
	if !f.personal {
		server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
			return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
				return next(midpoint.WithPrincipal(ctx, fxBstone), method, req)
			}
		})
	}
	return connectSession(t, server, true)
}

func TestWriteMyRequestsViewFixtures(t *testing.T) {
	if os.Getenv(envWriteViewFixtures) == "" {
		t.Skip("fixture generation disabled")
	}
	for _, name := range []string{"list", "case", "personal", "preview", "withdrawn", "unconfirmed", "closed", "not-your-request", "not-authorized", "request-closed", "shared", "whoami"} {
		t.Run(name, func(t *testing.T) {
			f := newRequesterFixture(t)
			tool := "cancel_request"
			args := map[string]any{"caseOid": fxCaseTwoStep}
			wantError := false
			switch name {
			case "list", "personal", "closed":
				tool = "list_my_requests"
				args = map[string]any{}
				f.personal = name == "personal"
				if name == "closed" {
					f.before = f.after
				}
			case "case":
				tool = "get_case"
				args = map[string]any{"oid": fxCaseTwoStep}
			case "whoami":
				tool = "whoami"
				args = map[string]any{}
			case "preview":
				f.writes = false
			case "unconfirmed":
				f.after = f.before
			case "not-authorized":
				f.cancelStatus = 403
				wantError = true
			case "not-your-request":
				f.before = mutateCase(t, f.before, func(c map[string]any) { c["requestorRef"] = map[string]any{"oid": fxDlee, "targetName": "dlee"} })
				wantError = true
			case "request-closed":
				f.before = f.after
				wantError = true
			case "shared":
				f.personal = true
				f.shared = true
				wantError = true
			}
			cs := f.connect(t)
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
			if err != nil || res.IsError != wantError {
				t.Fatalf("result %v, %v", res, err)
			}
			file := map[string]any{"call": map[string]any{"name": tool, "arguments": args}, "about": "Requester scenario: " + name + ". Recorded case enriched by this server; second open approver item and bodyless cancellation modeled from the documented REST shapes (live verification pending).", "result": res}
			b, err := json.MarshalIndent(file, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), "127.0.0.1") {
				t.Fatal("address in fixture")
			}
			if err := os.WriteFile(filepath.Join(viewFixtureDir, "my-requests."+name+".json"), append(b, '\n'), 0644); err != nil {
				t.Fatal(err)
			}
		})
	}
}
