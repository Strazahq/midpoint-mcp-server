package oidcauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
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
