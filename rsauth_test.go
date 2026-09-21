package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
	"github.com/strazahq/midpoint-mcp-server/internal/oidcauth"
)

const (
	e2eAudience  = "midpoint-mcp"
	e2eSubject   = "sub-abc"
	e2eUsername  = "jdoe"
	e2eMappedOID = "mp-oid-999"
)

// mockOIDCProvider serves OIDC discovery + JWKS for a generated RSA key and
// mints signed tokens. The issuer is derived from the request host so it matches
// the httptest URL passed to oidc discovery.
type mockOIDCProvider struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	signer jose.Signer
}

func newMockOIDC(t *testing.T) *mockOIDCProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	sgn, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test-kid"),
	)
	if err != nil {
		t.Fatal(err)
	}
	m := &mockOIDCProvider{key: key, signer: sgn}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		issuer := "http://" + r.Host
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 issuer,
			"authorization_endpoint": issuer + "/auth",
			"token_endpoint":         issuer + "/token",
			"jwks_uri":               issuer + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key: key.Public(), KeyID: "test-kid", Algorithm: "RS256", Use: "sig",
		}}}
		_ = json.NewEncoder(w).Encode(set)
	})
	m.server = httptest.NewServer(mux)
	t.Cleanup(m.server.Close)
	return m
}

func (m *mockOIDCProvider) issuer() string { return m.server.URL }

func (m *mockOIDCProvider) mint(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	claims := map[string]any{
		"iss":                m.server.URL,
		"aud":                e2eAudience,
		"sub":                e2eSubject,
		"preferred_username": e2eUsername,
		"exp":                time.Now().Add(time.Hour).Unix(),
		"iat":                time.Now().Add(-time.Minute).Unix(),
	}
	if mutate != nil {
		mutate(claims)
	}
	payload, _ := json.Marshal(claims)
	obj, err := m.signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := obj.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// recordingMidpoint records the Switch-To-Principal header seen on each request
// path and the filter of each user search, correlates any user to e2eMappedOID,
// and answers /self.
type recordingMidpoint struct {
	server  *httptest.Server
	mu      sync.Mutex
	switch_ map[string]string // path -> Switch-To-Principal header value
	filters []string          // users/search filter texts, in order
	// holdsArchetype answers a search that carries an archetype condition. Nil
	// means every user holds it.
	holdsArchetype func(filter string) bool
}

func newRecordingMidpoint(t *testing.T) *recordingMidpoint {
	t.Helper()
	rm := &recordingMidpoint{switch_: map[string]string{}}
	mux := http.NewServeMux()
	record := func(w http.ResponseWriter, r *http.Request, body string) {
		rm.mu.Lock()
		rm.switch_[r.URL.Path] = r.Header.Get(midpoint.SwitchToPrincipalHeader)
		rm.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
	mux.HandleFunc("POST /ws/rest/users/search", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query struct {
				Filter struct {
					Text string `json:"text"`
				} `json:"filter"`
			} `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		filter := req.Query.Filter.Text
		rm.mu.Lock()
		rm.filters = append(rm.filters, filter)
		rm.mu.Unlock()
		if strings.Contains(filter, "archetypeRef") && rm.holdsArchetype != nil && !rm.holdsArchetype(filter) {
			record(w, r, `{"object":[]}`)
			return
		}
		record(w, r, `{"object":[{"oid":"`+e2eMappedOID+`","name":"`+e2eUsername+`"}]}`)
	})
	mux.HandleFunc("GET /ws/rest/self", func(w http.ResponseWriter, r *http.Request) {
		record(w, r, `{"user":{"oid":"`+e2eMappedOID+`","name":"`+e2eUsername+`"}}`)
	})
	rm.server = httptest.NewServer(mux)
	t.Cleanup(rm.server.Close)
	return rm
}

func (rm *recordingMidpoint) switchTo(path string) string {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.switch_[path]
}

func (rm *recordingMidpoint) searchFilters() []string {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return slices.Clone(rm.filters)
}

type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (b bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if b.token != "" {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return b.base.RoundTrip(r)
}

// connectResourceServer builds the resource-server handler and connects an MCP
// client with the given bearer token.
func connectResourceServer(t *testing.T, oidc *mockOIDCProvider, mp *recordingMidpoint, token string) (*mcp.ClientSession, error) {
	t.Helper()
	return connectResourceServerConfig(t, midpoint.Config{
		BaseURL:      mp.server.URL,
		Username:     "svc",
		Password:     "p",
		OIDCIssuer:   oidc.issuer(),
		OIDCAudience: e2eAudience,
	}, token)
}

// connectResourceServerConfig is connectResourceServer for a caller that needs
// its own settings.
func connectResourceServerConfig(t *testing.T, cfg midpoint.Config, token string) (*mcp.ClientSession, error) {
	t.Helper()
	ctx := context.Background()

	client := midpoint.NewClient(cfg)
	authn, err := oidcauth.New(ctx, cfg.OIDCIssuer, cfg.OIDCAudience, cfg.OIDCCorrelationClaim, cfg.OIDCClientCorrelationClaim)
	if err != nil {
		t.Fatalf("oidcauth.New: %v", err)
	}

	httpSrv := httptest.NewServer(mcpHTTPHandler(client, cfg, authn))
	t.Cleanup(httpSrv.Close)

	transport := &mcp.StreamableClientTransport{
		Endpoint:             httpSrv.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerRoundTripper{token: token, base: http.DefaultTransport}},
		DisableStandaloneSSE: true,
	}
	mc := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "t"}, nil)
	return mc.Connect(ctx, transport, nil)
}

func TestResourceServerImpersonatesCaller(t *testing.T) {
	oidc := newMockOIDC(t)
	mp := newRecordingMidpoint(t)

	cs, err := connectResourceServer(t, oidc, mp, oidc.mint(t, nil))
	if err != nil {
		t.Fatalf("connect with valid token: %v", err)
	}
	defer cs.Close()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "ping", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool(ping): %v", err)
	}
	if res.IsError {
		t.Fatalf("ping tool error: %v", res.Content)
	}

	// The tool call must have executed as the mapped user.
	if got := mp.switchTo("/ws/rest/self"); got != e2eMappedOID {
		t.Errorf("Switch-To-Principal on /self = %q, want %q", got, e2eMappedOID)
	}
	// Correlation itself runs as the service account (no impersonation).
	if got := mp.switchTo("/ws/rest/users/search"); got != "" {
		t.Errorf("correlation search carried Switch-To-Principal %q, want none", got)
	}
}

func TestResourceServerRejectsBadAuth(t *testing.T) {
	oidc := newMockOIDC(t)
	mp := newRecordingMidpoint(t)

	t.Run("no token", func(t *testing.T) {
		if cs, err := connectResourceServer(t, oidc, mp, ""); err == nil {
			cs.Close()
			t.Fatal("connect without a token should fail")
		}
	})

	t.Run("expired token", func(t *testing.T) {
		token := oidc.mint(t, func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() })
		if cs, err := connectResourceServer(t, oidc, mp, token); err == nil {
			cs.Close()
			t.Fatal("connect with an expired token should fail")
		}
	})

	t.Run("wrong audience", func(t *testing.T) {
		token := oidc.mint(t, func(c map[string]any) { c["aud"] = "not-us" })
		if cs, err := connectResourceServer(t, oidc, mp, token); err == nil {
			cs.Close()
			t.Fatal("connect with a wrong-audience token should fail")
		}
	})
}

// A client credentials token names an OAuth client, not a person. With the client
// settings on, its client claim is the name to correlate and every correlation
// query is limited to the listed archetypes. A person's token is untouched.
func TestResourceServerClientToken(t *testing.T) {
	const agentArchetype = "11111111-2222-3333-4444-5555555500a2"
	const guard = `(archetypeRef matches (oid = "` + agentArchetype + `"))`
	clientToken := func(oidc *mockOIDCProvider, clientID string) string {
		return oidc.mint(t, func(c map[string]any) {
			c["client_id"] = clientID
			c["preferred_username"] = "service-account-" + clientID
		})
	}
	config := func(oidc *mockOIDCProvider, mp *recordingMidpoint) midpoint.Config {
		return midpoint.Config{
			BaseURL:                    mp.server.URL,
			Username:                   "svc",
			Password:                   "p",
			OIDCIssuer:                 oidc.issuer(),
			OIDCAudience:               e2eAudience,
			OIDCClientCorrelationClaim: "client_id",
			OIDCClientArchetypes:       []string{agentArchetype},
		}
	}

	t.Run("agent client runs as the agent's user", func(t *testing.T) {
		oidc, mp := newMockOIDC(t), newRecordingMidpoint(t)
		cs, err := connectResourceServerConfig(t, config(oidc, mp), clientToken(oidc, "build-agent"))
		if err != nil {
			t.Fatalf("connect with a client token: %v", err)
		}
		defer cs.Close()
		filters := mp.searchFilters()
		if len(filters) == 0 {
			t.Fatal("no correlation query reached midPoint")
		}
		for _, f := range filters {
			if !strings.HasSuffix(f, " and "+guard) {
				t.Errorf("correlation query %q lacks the archetype condition", f)
			}
		}
		if !slices.Contains(filters, `externalId = "`+e2eSubject+`" and `+guard) {
			t.Errorf("queries %q: the externalId attempt is missing or unguarded", filters)
		}
	})

	// People may correlate on another attribute. A client id is the agent
	// user's name, which midPoint keeps unique.
	t.Run("client id is matched on name whatever people correlate on", func(t *testing.T) {
		oidc, mp := newMockOIDC(t), newRecordingMidpoint(t)
		cfg := config(oidc, mp)
		cfg.OIDCCorrelationAttribute = "emailAddress"
		cs, err := connectResourceServerConfig(t, cfg, clientToken(oidc, "build-agent"))
		if err != nil {
			t.Fatalf("connect with a client token: %v", err)
		}
		defer cs.Close()
		if !slices.Contains(mp.searchFilters(), `name = "build-agent" and `+guard) {
			t.Errorf("queries %q: the client id was not matched on name", mp.searchFilters())
		}
	})

	t.Run("client named like a person is refused", func(t *testing.T) {
		oidc, mp := newMockOIDC(t), newRecordingMidpoint(t)
		mp.holdsArchetype = func(string) bool { return false }
		cs, err := connectResourceServerConfig(t, config(oidc, mp), clientToken(oidc, e2eUsername))
		if err == nil {
			cs.Close()
			t.Fatal("a client whose name matches a person must not connect as that person")
		}
		if !slices.Contains(mp.searchFilters(), `name = "`+e2eUsername+`" and `+guard) {
			t.Errorf("queries %q: the client id was not correlated under the archetype condition", mp.searchFilters())
		}
	})

	// ConfigFromEnv refuses this pair at startup. The verifier must not depend
	// on that: a client's token without an archetype list has no safe match.
	t.Run("client token without an archetype list is refused", func(t *testing.T) {
		oidc, mp := newMockOIDC(t), newRecordingMidpoint(t)
		cfg := config(oidc, mp)
		cfg.OIDCClientArchetypes = nil
		cs, err := connectResourceServerConfig(t, cfg, clientToken(oidc, "build-agent"))
		if err == nil {
			cs.Close()
			t.Fatal("a client's token must not correlate without the archetype condition")
		}
		if got := mp.searchFilters(); len(got) != 0 {
			t.Errorf("unguarded correlation queries reached midPoint: %q", got)
		}
	})

	t.Run("person token is not guarded", func(t *testing.T) {
		oidc, mp := newMockOIDC(t), newRecordingMidpoint(t)
		mp.holdsArchetype = func(string) bool { return false }
		cs, err := connectResourceServerConfig(t, config(oidc, mp), oidc.mint(t, nil))
		if err != nil {
			t.Fatalf("connect with a person's token: %v", err)
		}
		defer cs.Close()
		for _, f := range mp.searchFilters() {
			if strings.Contains(f, "archetypeRef") {
				t.Errorf("person correlation query %q carries the archetype condition", f)
			}
		}
	})
}
