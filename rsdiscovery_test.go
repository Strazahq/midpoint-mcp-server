package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
	"github.com/strazahq/midpoint-mcp-server/internal/oidcauth"
)

func TestDiscoveryOnly(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"initialize", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`, true},
		{"initialized notification", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, true},
		{"protocol ping", `{"jsonrpc":"2.0","id":2,"method":"ping"}`, true},
		{"tools list", `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`, true},

		{"tools call", `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_user"}}`, false},
		// The ping *tool* shares a name with the protocol ping but reaches
		// midPoint's /ws/rest/self; it must not ride in on the allowlisted method.
		{"ping tool via tools/call", `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"ping"}}`, false},
		{"resources list is not allowlisted", `{"jsonrpc":"2.0","id":6,"method":"resources/list"}`, false},

		{"batch all discovery", `[{"jsonrpc":"2.0","id":1,"method":"initialize"},{"jsonrpc":"2.0","id":2,"method":"tools/list"}]`, true},
		// One privileged member makes the whole request privileged.
		{"batch smuggling a tools/call", `[{"jsonrpc":"2.0","id":1,"method":"tools/list"},{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_user"}}]`, false},
		{"empty batch", `[]`, false},

		{"empty body", ``, false},
		{"whitespace only", "  \n\t ", false},
		{"malformed json", `{"method":`, false},
		{"no method", `{"jsonrpc":"2.0","id":1}`, false},
		{"method is not a string", `{"jsonrpc":"2.0","id":1,"method":{"a":1}}`, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := discoveryOnly([]byte(tc.body)); got != tc.want {
				t.Errorf("discoveryOnly(%s) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

// stubRequireAuth stands in for RequireBearerToken: it 401s a request with no
// bearer and otherwise passes through, which is all the gate's routing depends
// on. Real token verification is covered by the end-to-end tests in rsauth_test.go.
func stubRequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			http.Error(w, "no bearer token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func TestDiscoveryGateRouting(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		auth       string
		body       string
		wantStatus int
		wantReach  bool // did the request reach the MCP handler?
	}{
		{
			name: "anonymous initialize is served", method: http.MethodPost,
			body:       `{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
			wantStatus: http.StatusOK, wantReach: true,
		},
		{
			name: "anonymous tools/list is served", method: http.MethodPost,
			body:       `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
			wantStatus: http.StatusOK, wantReach: true,
		},
		{
			name: "anonymous tools/call is refused", method: http.MethodPost,
			body:       `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_user"}}`,
			wantStatus: http.StatusUnauthorized, wantReach: false,
		},
		{
			name: "anonymous batch smuggling a tools/call is refused", method: http.MethodPost,
			body:       `[{"jsonrpc":"2.0","id":1,"method":"tools/list"},{"jsonrpc":"2.0","id":2,"method":"tools/call"}]`,
			wantStatus: http.StatusUnauthorized, wantReach: false,
		},
		{
			// A token is always verified, even for a method the gate would have
			// served anonymously — that is what binds the session to a user.
			name: "initialize with a token still goes through auth", method: http.MethodPost,
			auth: "Bearer good", body: `{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
			wantStatus: http.StatusOK, wantReach: true,
		},
		{
			// Malformed Authorization is not "no Authorization": it must not be
			// treated as an anonymous discovery caller.
			name: "initialize with a malformed token is refused", method: http.MethodPost,
			auth: "Basic abc", body: `{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
			wantStatus: http.StatusUnauthorized, wantReach: false,
		},
		{
			name: "anonymous malformed body is refused", method: http.MethodPost,
			body: `{"method":`, wantStatus: http.StatusUnauthorized, wantReach: false,
		},
		{
			// Bigger than maxDiscoveryBody: refused without being buffered whole.
			name: "anonymous oversized body is refused", method: http.MethodPost,
			body:       `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"pad":"` + strings.Repeat("A", maxDiscoveryBody) + `"}}`,
			wantStatus: http.StatusUnauthorized, wantReach: false,
		},
		{
			// GET opens the stream; it carries no method, and the SDK guards the
			// session itself.
			name: "anonymous GET passes through", method: http.MethodGet,
			wantStatus: http.StatusOK, wantReach: true,
		},
		{
			name: "anonymous DELETE passes through", method: http.MethodDelete,
			wantStatus: http.StatusOK, wantReach: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var reached bool
			var seenBody string
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				b, _ := io.ReadAll(r.Body)
				seenBody = string(b)
				w.WriteHeader(http.StatusOK)
			})

			req := httptest.NewRequest(tc.method, "/mcp", strings.NewReader(tc.body))
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			rec := httptest.NewRecorder()
			discoveryGate(stubRequireAuth, next).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if reached != tc.wantReach {
				t.Errorf("reached MCP handler = %v, want %v", reached, tc.wantReach)
			}
			// The gate reads the body to classify it, so the handler behind it
			// must still see the bytes intact.
			if tc.wantReach && tc.method == http.MethodPost && seenBody != tc.body {
				t.Errorf("body not restored downstream:\n got %q\nwant %q", seenBody, tc.body)
			}
		})
	}
}

// Without the opt-in the gate is not installed at all, so nothing is anonymous.
// This pins the default rather than trusting the wiring in mcpHTTPHandler.
func TestDiscoveryGateNotInstalledByDefault(t *testing.T) {
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	rec := httptest.NewRecorder()
	stubRequireAuth(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if reached {
		t.Error("anonymous initialize reached the MCP handler with the gate absent")
	}
}

// pathsSeen reports how many distinct midPoint paths were hit. switchTo cannot
// answer "was midPoint touched at all?" — it returns "" both for a path never
// called and for one called without the impersonation header.
func (rm *recordingMidpoint) pathsSeen() int {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return len(rm.switch_)
}

// mutableBearer lets one MCP session start anonymous and acquire a token later:
// the flow a gateway uses when it inventories the tool surface before any user
// has authenticated.
type mutableBearer struct {
	mu    sync.Mutex
	token string
	base  http.RoundTripper
}

func (m *mutableBearer) set(tok string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token = tok
}

func (m *mutableBearer) RoundTrip(r *http.Request) (*http.Response, error) {
	m.mu.Lock()
	tok := m.token
	m.mu.Unlock()
	if tok != "" {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	return m.base.RoundTrip(r)
}

// connectAnonymousDiscovery builds a resource-server handler with anonymous
// discovery enabled and connects a client through rt.
func connectAnonymousDiscovery(t *testing.T, oidc *mockOIDCProvider, mp *recordingMidpoint, rt http.RoundTripper) (*mcp.ClientSession, error) {
	t.Helper()
	ctx := context.Background()

	cfg := midpoint.Config{
		BaseURL:            mp.server.URL,
		Username:           "svc",
		Password:           "p",
		OIDCIssuer:         oidc.issuer(),
		OIDCAudience:       e2eAudience,
		AnonymousDiscovery: true,
	}
	client := midpoint.NewClient(cfg)
	authn, err := oidcauth.New(ctx, cfg.OIDCIssuer, cfg.OIDCAudience, cfg.OIDCCorrelationClaim)
	if err != nil {
		t.Fatalf("oidcauth.New: %v", err)
	}

	httpSrv := httptest.NewServer(mcpHTTPHandler(client, cfg, authn))
	t.Cleanup(httpSrv.Close)

	transport := &mcp.StreamableClientTransport{
		Endpoint:             httpSrv.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: rt},
		DisableStandaloneSSE: true,
	}
	mc := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "t"}, nil)
	return mc.Connect(ctx, transport, nil)
}

// A tokenless caller can complete the handshake and read the tool surface, and
// doing so must not reach midPoint even once.
func TestAnonymousDiscoveryListsToolsWithoutToken(t *testing.T) {
	oidc := newMockOIDC(t)
	mp := newRecordingMidpoint(t)

	cs, err := connectAnonymousDiscovery(t, oidc, mp, &mutableBearer{base: http.DefaultTransport})
	if err != nil {
		t.Fatalf("anonymous connect: %v", err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("anonymous ListTools: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("anonymous ListTools returned no tools")
	}
	if n := mp.pathsSeen(); n != 0 {
		t.Errorf("discovery reached midPoint on %d path(s), want 0", n)
	}
}

// The whole point of the gate: discovery opens, tools/call does not.
func TestAnonymousDiscoveryStillRefusesToolCalls(t *testing.T) {
	oidc := newMockOIDC(t)
	mp := newRecordingMidpoint(t)

	cs, err := connectAnonymousDiscovery(t, oidc, mp, &mutableBearer{base: http.DefaultTransport})
	if err != nil {
		t.Fatalf("anonymous connect: %v", err)
	}
	defer cs.Close()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "ping", Arguments: map[string]any{},
	})
	if err == nil {
		t.Fatalf("anonymous tools/call succeeded, want refused: %+v", res)
	}
	if n := mp.pathsSeen(); n != 0 {
		t.Errorf("refused tools/call still reached midPoint on %d path(s), want 0", n)
	}
}

// The gateway's real sequence: inventory tools with no token, then call one on
// the SAME session once a user token is in hand. This pins the behaviour the
// design leans on — the SDK binds a session to a user ID at initialize and
// skips its hijack check when that ID is empty, so an anonymously-created
// session must still accept a later authenticated call and impersonate the
// token's user rather than the service account.
func TestAnonymousDiscoveryThenAuthenticatedCall(t *testing.T) {
	oidc := newMockOIDC(t)
	mp := newRecordingMidpoint(t)
	rt := &mutableBearer{base: http.DefaultTransport}

	cs, err := connectAnonymousDiscovery(t, oidc, mp, rt)
	if err != nil {
		t.Fatalf("anonymous connect: %v", err)
	}
	defer cs.Close()

	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("anonymous ListTools: %v", err)
	}

	rt.set(oidc.mint(t, nil))

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "ping", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("authenticated tools/call on an anonymously-initialized session: %v", err)
	}
	if res.IsError {
		t.Fatalf("ping tool error: %v", res.Content)
	}
	if got := mp.switchTo("/ws/rest/self"); got != e2eMappedOID {
		t.Errorf("Switch-To-Principal on /self = %q, want %q", got, e2eMappedOID)
	}
}
