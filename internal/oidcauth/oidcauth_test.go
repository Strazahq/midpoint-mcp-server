package oidcauth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
)

const (
	testIssuer   = "https://issuer.example.com"
	testAudience = "midpoint-mcp"
)

// signer mints signed JWTs for tests.
type signer struct {
	key *rsa.PrivateKey
	jws jose.Signer
}

func newSigner(t *testing.T) *signer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jws, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		t.Fatal(err)
	}
	return &signer{key: key, jws: jws}
}

func (s *signer) mint(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := s.jws.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := obj.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// authenticatorFor builds an Authenticator that trusts s's public key (bypassing
// network discovery via a static key set).
func authenticatorFor(s *signer) *Authenticator {
	keySet := &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{s.key.Public()}}
	v := oidc.NewVerifier(testIssuer, keySet, &oidc.Config{ClientID: testAudience})
	return &Authenticator{verifier: v}
}

// authenticatorForClaim is authenticatorFor with a custom correlation claim.
func authenticatorForClaim(s *signer, claim string) *Authenticator {
	a := authenticatorFor(s)
	a.correlationClaim = claim
	return a
}

func baseClaims() map[string]any {
	return map[string]any{
		"iss":                testIssuer,
		"aud":                testAudience,
		"sub":                "user-sub-123",
		"preferred_username": "jdoe",
		"exp":                time.Now().Add(time.Hour).Unix(),
		"iat":                time.Now().Add(-time.Minute).Unix(),
	}
}

func TestVerifyValidToken(t *testing.T) {
	s := newSigner(t)
	a := authenticatorFor(s)

	claims, err := a.Verify(context.Background(), s.mint(t, baseClaims()))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "user-sub-123" || claims.CorrelationValue != "jdoe" {
		t.Errorf("claims = %+v", claims)
	}
	if claims.Expiry.Before(time.Now()) {
		t.Errorf("expiry = %v, want future", claims.Expiry)
	}
}

func TestVerifyCustomCorrelationClaim(t *testing.T) {
	s := newSigner(t)

	t.Run("string claim", func(t *testing.T) {
		a := authenticatorForClaim(s, "email")
		c := baseClaims()
		c["email"] = "jane@example.com"
		claims, err := a.Verify(context.Background(), s.mint(t, c))
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		// preferred_username must be ignored in favor of the configured claim.
		if claims.CorrelationValue != "jane@example.com" {
			t.Errorf("CorrelationValue = %q, want the email claim", claims.CorrelationValue)
		}
	})

	t.Run("numeric claim is stringified", func(t *testing.T) {
		a := authenticatorForClaim(s, "employeeNumber")
		c := baseClaims()
		c["employeeNumber"] = 40277
		claims, err := a.Verify(context.Background(), s.mint(t, c))
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if claims.CorrelationValue != "40277" {
			t.Errorf("CorrelationValue = %q, want \"40277\"", claims.CorrelationValue)
		}
	})

	t.Run("absent claim yields empty", func(t *testing.T) {
		a := authenticatorForClaim(s, "does_not_exist")
		claims, err := a.Verify(context.Background(), s.mint(t, baseClaims()))
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if claims.CorrelationValue != "" {
			t.Errorf("CorrelationValue = %q, want empty", claims.CorrelationValue)
		}
	})
}

func TestVerifyClientCorrelationClaim(t *testing.T) {
	s := newSigner(t)
	clientToken := func(clientID any) map[string]any {
		c := baseClaims()
		c["preferred_username"] = "service-account-build-agent"
		c["client_id"] = clientID
		return c
	}

	tests := []struct {
		name        string
		personClaim string // "" = default (preferred_username)
		clientClaim string // "" = the setting is unset
		claims      map[string]any
		wantValue   string
		wantClient  bool
		wantErr     bool
	}{
		{
			name: "client claim present names the client", clientClaim: "client_id",
			claims: clientToken("build-agent"), wantValue: "build-agent", wantClient: true,
		},
		{
			name: "person token has no client claim", clientClaim: "client_id",
			claims: baseClaims(), wantValue: "jdoe",
		},
		{
			name: "person token keeps a custom person claim", personClaim: "email", clientClaim: "client_id",
			claims: func() map[string]any {
				c := baseClaims()
				c["email"] = "jane@example.com"
				return c
			}(),
			wantValue: "jane@example.com",
		},
		{
			name:   "unset setting ignores the client claim",
			claims: clientToken("build-agent"), wantValue: "service-account-build-agent",
		},
		{
			name: "empty client claim is refused", clientClaim: "client_id",
			claims: clientToken(""), wantErr: true,
		},
		{
			name: "non-scalar client claim is refused", clientClaim: "client_id",
			claims: clientToken([]string{"build-agent"}), wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := authenticatorForClaim(s, tt.personClaim)
			a.clientCorrelationClaim = tt.clientClaim
			claims, err := a.Verify(context.Background(), s.mint(t, tt.claims))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Verify = %+v, want error", claims)
				}
				if !strings.Contains(err.Error(), `"client_id"`) {
					t.Errorf("error %q does not name the claim", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if claims.CorrelationValue != tt.wantValue || claims.Client != tt.wantClient {
				t.Errorf("CorrelationValue, Client = %q, %v, want %q, %v",
					claims.CorrelationValue, claims.Client, tt.wantValue, tt.wantClient)
			}
		})
	}
}

func TestVerifyRejectsBadTokens(t *testing.T) {
	s := newSigner(t)
	a := authenticatorFor(s)

	t.Run("expired", func(t *testing.T) {
		c := baseClaims()
		c["exp"] = time.Now().Add(-time.Hour).Unix()
		if _, err := a.Verify(context.Background(), s.mint(t, c)); err == nil {
			t.Fatal("expected error for expired token")
		}
	})

	t.Run("wrong audience", func(t *testing.T) {
		c := baseClaims()
		c["aud"] = "some-other-service"
		if _, err := a.Verify(context.Background(), s.mint(t, c)); err == nil {
			t.Fatal("expected error for wrong audience")
		}
	})

	t.Run("wrong issuer", func(t *testing.T) {
		c := baseClaims()
		c["iss"] = "https://evil.example.com"
		if _, err := a.Verify(context.Background(), s.mint(t, c)); err == nil {
			t.Fatal("expected error for wrong issuer")
		}
	})

	t.Run("signature from an untrusted key", func(t *testing.T) {
		// Sign with a different key than the authenticator trusts.
		other := newSigner(t)
		if _, err := a.Verify(context.Background(), other.mint(t, baseClaims())); err == nil {
			t.Fatal("expected error for untrusted signature")
		}
	})

	t.Run("not a jwt", func(t *testing.T) {
		if _, err := a.Verify(context.Background(), "not-a-token"); err == nil {
			t.Fatal("expected error for malformed token")
		}
	})
}

// fakeIdP serves discovery documents and the JWKS for a test key. Every
// document names issuer, which is a URL no test dials, and puts jwks_uri on the
// fake itself, the way a provider reached on an internal address does.
type fakeIdP struct {
	*httptest.Server
	issuer string
	// algs is the document's id_token_signing_alg_values_supported. Nil means
	// RS256 only.
	algs []string
}

// Paths the fake serves. discoveryPath is deliberately not a well-known path,
// so a discovery URL only works when it is used exactly as given.
const (
	discoveryPath = "/internal/realms/x/discovery"
	wellKnownPath = "/.well-known/openid-configuration"
	notJSONPath   = "/not-json"
	largePath     = "/large"
	// selfIssuer stands for the fake's own URL in TestNewDiscovery rows.
	selfIssuer = "{self}"
)

func newFakeIdP(t *testing.T, s *signer, issuer string) *fakeIdP {
	t.Helper()
	idp := &fakeIdP{issuer: issuer}
	doc := func(w http.ResponseWriter, _ *http.Request) {
		algs := idp.algs
		if algs == nil {
			algs = []string{"RS256"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                idp.issuer,
			"jwks_uri":                              idp.URL + "/certs",
			"id_token_signing_alg_values_supported": algs,
		})
	}
	mux := http.NewServeMux()
	mux.HandleFunc(discoveryPath, doc)
	mux.HandleFunc(wellKnownPath, doc)
	mux.HandleFunc("/certs", func(w http.ResponseWriter, _ *http.Request) {
		set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key: s.key.Public(), Algorithm: "RS256", Use: "sig",
		}}}
		_ = json.NewEncoder(w).Encode(set)
	})
	mux.HandleFunc(notJSONPath, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "<html>sign in</html>")
	})
	mux.HandleFunc(largePath, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte(" "), 1<<20+1))
	})
	idp.Server = httptest.NewServer(mux)
	t.Cleanup(idp.Close)
	return idp
}

// claimsFrom is baseClaims with iss set to issuer.
func claimsFrom(issuer string) map[string]any {
	c := baseClaims()
	c["iss"] = issuer
	return c
}

func TestNewDiscovery(t *testing.T) {
	const configured = "http://idp.example/realms/x"
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()

	tests := []struct {
		name string
		// issuer is the configured issuer and docIssuer the one the documents
		// name. selfIssuer means the fake's own URL.
		issuer, docIssuer string
		// discovery is the discovery URL, with selfIssuer standing for the
		// fake's own URL. Empty means none is set.
		discovery string
		algs      []string
		// wantErr lists what New's error must contain, with selfIssuer
		// standing for the fake's URL. Empty means New succeeds and a token
		// carrying the configured issuer verifies.
		wantErr []string
	}{
		{
			name:   "no discovery URL, the issuer serves its own document",
			issuer: selfIssuer, docIssuer: selfIssuer,
		},
		{
			name:   "no discovery URL, the document names another issuer",
			issuer: selfIssuer, docIssuer: configured,
			wantErr: []string{"did not match"},
		},
		{
			name:   "discovery URL, the document names the configured issuer",
			issuer: configured, docIssuer: configured, discovery: selfIssuer + discoveryPath,
		},
		{
			name:   "discovery URL, the document names another issuer",
			issuer: configured, docIssuer: "http://idp.example/realms/other", discovery: selfIssuer + discoveryPath,
			wantErr: []string{
				`the discovery document at ` + selfIssuer + discoveryPath + ` names issuer "http://idp.example/realms/other", but MIDPOINT_MCP_OIDC_ISSUER is "http://idp.example/realms/x".`,
				"Set MIDPOINT_MCP_OIDC_ISSUER to the issuer your identity provider puts in its tokens, or point MIDPOINT_MCP_OIDC_DISCOVERY_URL at that provider's own document",
			},
		},
		{
			name:   "discovery URL, the issuers differ only by a trailing slash",
			issuer: configured + "/", docIssuer: configured, discovery: selfIssuer + discoveryPath,
			wantErr: []string{`names issuer "http://idp.example/realms/x", but MIDPOINT_MCP_OIDC_ISSUER is "http://idp.example/realms/x/"`},
		},
		{
			name:   "discovery URL, the document answers 404",
			issuer: configured, docIssuer: configured, discovery: selfIssuer + "/missing",
			wantErr: []string{selfIssuer + "/missing", "404"},
		},
		{
			name:   "discovery URL, nothing listens there",
			issuer: configured, docIssuer: configured, discovery: closed.URL + discoveryPath,
			wantErr: []string{closed.URL + discoveryPath, "MIDPOINT_MCP_OIDC_DISCOVERY_URL"},
		},
		{
			name:   "discovery URL, the answer is not JSON",
			issuer: configured, docIssuer: configured, discovery: selfIssuer + notJSONPath,
			wantErr: []string{selfIssuer + notJSONPath, "MIDPOINT_MCP_OIDC_DISCOVERY_URL"},
		},
		{
			name:   "discovery URL, the answer is larger than the cap",
			issuer: configured, docIssuer: configured, discovery: selfIssuer + largePath,
			wantErr: []string{selfIssuer + largePath, "1 MiB"},
		},
		{
			// The issuer path drops algorithms go-oidc cannot verify, and so
			// must this one, or an HS256-only list would refuse RS256 tokens.
			name:   "discovery URL, algorithms go-oidc does not support are dropped",
			issuer: configured, docIssuer: configured, discovery: selfIssuer + discoveryPath,
			algs: []string{"HS256", "none"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSigner(t)
			idp := newFakeIdP(t, s, "")
			self := func(v string) string { return strings.ReplaceAll(v, selfIssuer, idp.URL) }
			idp.issuer = self(tt.docIssuer)
			idp.algs = tt.algs
			issuer := self(tt.issuer)

			a, err := New(context.Background(), issuer, self(tt.discovery), testAudience, "", "")
			if len(tt.wantErr) > 0 {
				if err == nil {
					t.Fatal("New succeeded, want an error")
				}
				for _, want := range tt.wantErr {
					if !strings.Contains(err.Error(), self(want)) {
						t.Errorf("error %q does not contain %q", err, self(want))
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := a.Verify(context.Background(), s.mint(t, claimsFrom(issuer))); err != nil {
				t.Errorf("Verify of a token from %q: %v", issuer, err)
			}
		})
	}
}

func TestVerifyAfterDiscoveryURL(t *testing.T) {
	const configured = "http://idp.example/realms/x"
	s := newSigner(t)
	idp := newFakeIdP(t, s, configured)
	a, err := New(context.Background(), configured, idp.URL+discoveryPath, testAudience, "", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(map[string]any)
		wantErr bool
	}{
		{name: "configured issuer and audience verify"},
		{
			// The address the document was fetched from is not an issuer.
			name:    "issuer is the dial address",
			mutate:  func(c map[string]any) { c["iss"] = idp.URL + "/internal/realms/x" },
			wantErr: true,
		},
		{
			name:    "issuer is the discovery URL",
			mutate:  func(c map[string]any) { c["iss"] = idp.URL + discoveryPath },
			wantErr: true,
		},
		{
			name:    "wrong audience",
			mutate:  func(c map[string]any) { c["aud"] = "some-other-service" },
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := claimsFrom(configured)
			if tt.mutate != nil {
				tt.mutate(c)
			}
			claims, err := a.Verify(context.Background(), s.mint(t, c))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Verify = %+v, want error", claims)
				}
				return
			}
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if claims.Subject != "user-sub-123" {
				t.Errorf("Subject = %q, want user-sub-123", claims.Subject)
			}
		})
	}
}
