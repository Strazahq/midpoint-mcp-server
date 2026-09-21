package midpoint

import (
	"slices"
	"strings"
	"testing"
)

func TestConfigOIDC(t *testing.T) {
	base := func() {
		t.Setenv(EnvURL, "https://mp.example.com/midpoint")
		t.Setenv(EnvUsername, "svc")
		t.Setenv(EnvPassword, "secret")
	}

	t.Run("neither is personal mode", func(t *testing.T) {
		base()
		t.Setenv(EnvOIDCIssuer, "")
		t.Setenv(EnvOIDCAudience, "")
		cfg, err := ConfigFromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ResourceServerMode() {
			t.Error("ResourceServerMode true with no OIDC config")
		}
	})

	t.Run("both is resource-server mode", func(t *testing.T) {
		base()
		t.Setenv(EnvOIDCIssuer, "https://kc.example.com/realms/x")
		t.Setenv(EnvOIDCAudience, "midpoint-mcp")
		cfg, err := ConfigFromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.ResourceServerMode() {
			t.Error("ResourceServerMode false with full OIDC config")
		}
	})

	t.Run("issuer without audience is rejected", func(t *testing.T) {
		base()
		t.Setenv(EnvOIDCIssuer, "https://kc.example.com/realms/x")
		t.Setenv(EnvOIDCAudience, "")
		if _, err := ConfigFromEnv(); err == nil {
			t.Error("expected error when only the issuer is set")
		}
	})

	t.Run("audience without issuer is rejected", func(t *testing.T) {
		base()
		t.Setenv(EnvOIDCIssuer, "")
		t.Setenv(EnvOIDCAudience, "midpoint-mcp")
		if _, err := ConfigFromEnv(); err == nil {
			t.Error("expected error when only the audience is set")
		}
	})

	t.Run("custom correlation claim/attribute parsed", func(t *testing.T) {
		base()
		t.Setenv(EnvOIDCIssuer, "https://kc.example.com/realms/x")
		t.Setenv(EnvOIDCAudience, "midpoint-mcp")
		t.Setenv(EnvOIDCCorrelationClaim, "email")
		t.Setenv(EnvOIDCCorrelationAttribute, "emailAddress")
		cfg, err := ConfigFromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.OIDCCorrelationClaim != "email" || cfg.OIDCCorrelationAttribute != "emailAddress" {
			t.Errorf("correlation cfg = %q / %q", cfg.OIDCCorrelationClaim, cfg.OIDCCorrelationAttribute)
		}
	})

	t.Run("invalid correlation attribute is rejected", func(t *testing.T) {
		base()
		t.Setenv(EnvOIDCIssuer, "https://kc.example.com/realms/x")
		t.Setenv(EnvOIDCAudience, "midpoint-mcp")
		t.Setenv(EnvOIDCCorrelationAttribute, `name = "x" or 1=1`)
		if _, err := ConfigFromEnv(); err == nil {
			t.Error("expected error for an injection-shaped correlation attribute")
		}
	})
}

func TestConfigOIDCClientCorrelation(t *testing.T) {
	const (
		agentOID   = "11111111-2222-3333-4444-5555555500a2"
		serviceOID = "11111111-2222-3333-4444-5555555500a3"
	)
	tests := []struct {
		name           string
		claim          string
		archetypes     string
		wantClaim      string
		wantArchetypes []string
		// wantErr lists what the startup error must name, so the operator can
		// tell which setting to fix. Empty means the config loads.
		wantErr []string
	}{
		{name: "neither set keeps today's behavior"},
		{
			name: "claim with one archetype", claim: "client_id", archetypes: agentOID,
			wantClaim: "client_id", wantArchetypes: []string{agentOID},
		},
		{
			name: "several archetypes, spaces and empty entries trimmed", claim: " client_id ",
			archetypes: " " + agentOID + " , " + serviceOID + " ,",
			wantClaim:  "client_id", wantArchetypes: []string{agentOID, serviceOID},
		},
		{
			name: "claim without archetypes is rejected", claim: "client_id",
			wantErr: []string{EnvOIDCClientCorrelationClaim, EnvOIDCClientArchetypes, "Set " + EnvOIDCClientArchetypes},
		},
		{
			name: "claim with only separators is rejected", claim: "client_id", archetypes: " , ,",
			wantErr: []string{EnvOIDCClientCorrelationClaim, EnvOIDCClientArchetypes},
		},
		{
			name: "archetypes without the claim is rejected", archetypes: agentOID,
			wantErr: []string{EnvOIDCClientArchetypes, EnvOIDCClientCorrelationClaim, "Set " + EnvOIDCClientCorrelationClaim},
		},
		{
			name: "injection-shaped archetype oid is rejected", claim: "client_id",
			archetypes: agentOID + `") or name = "alice`,
			wantErr:    []string{EnvOIDCClientArchetypes, "not a midPoint oid"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvURL, "https://mp.example.com/midpoint")
			t.Setenv(EnvUsername, "svc")
			t.Setenv(EnvPassword, "secret")
			t.Setenv(EnvOIDCIssuer, "https://kc.example.com/realms/x")
			t.Setenv(EnvOIDCAudience, "midpoint-mcp")
			t.Setenv(EnvOIDCClientCorrelationClaim, tt.claim)
			t.Setenv(EnvOIDCClientArchetypes, tt.archetypes)
			cfg, err := ConfigFromEnv()
			if len(tt.wantErr) > 0 {
				if err == nil {
					t.Fatalf("ConfigFromEnv loaded %+v, want a startup error", cfg)
				}
				for _, want := range tt.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not name %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.OIDCClientCorrelationClaim != tt.wantClaim {
				t.Errorf("OIDCClientCorrelationClaim = %q, want %q", cfg.OIDCClientCorrelationClaim, tt.wantClaim)
			}
			if !slices.Equal(cfg.OIDCClientArchetypes, tt.wantArchetypes) {
				t.Errorf("OIDCClientArchetypes = %q, want %q", cfg.OIDCClientArchetypes, tt.wantArchetypes)
			}
		})
	}
}

func TestValidCorrelationAttribute(t *testing.T) {
	ok := []string{"name", "emailAddress", "employeeNumber", "extension/badgeId", "a"}
	bad := []string{"", "1name", "/name", "name/", "a//b", "name = x", `name"`, "na me", "name;drop"}
	for _, s := range ok {
		if !ValidCorrelationAttribute(s) {
			t.Errorf("ValidCorrelationAttribute(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if ValidCorrelationAttribute(s) {
			t.Errorf("ValidCorrelationAttribute(%q) = true, want false", s)
		}
	}
}
