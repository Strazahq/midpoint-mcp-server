package midpoint

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newSelfClient serves GET /self and records the query it was called with.
func newSelfClient(t *testing.T, body string, gotQuery *string) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/rest/self", func(w http.ResponseWriter, r *http.Request) {
		*gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return NewClient(Config{BaseURL: srv.URL, Username: "svc", Password: "p"})
}

const selfWithNamedOrgs = `{"user":{"oid":"me","name":"svc-account","fullName":"Service Account",
	"parentOrgRef":[
		{"oid":"o-mgd","relation":"org:manager","type":"OrgType","targetName":"dev-ops"},
		{"oid":"o-mem","relation":"org:default","type":"OrgType","targetName":"all-staff"}
	]}}`

func TestWhoamiPersonalMode(t *testing.T) {
	var query string
	c := newSelfClient(t, selfWithNamedOrgs, &query)

	p, err := c.Whoami(context.Background())
	if err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if p.Name != "svc-account" || p.OID != "me" || p.FullName != "Service Account" {
		t.Errorf("principal = %+v", p)
	}
	// No per-request principal: this is the configured credentials' identity.
	if p.Mode != ModePersonal || p.Impersonated {
		t.Errorf("mode = %q impersonated = %v, want personal/false", p.Mode, p.Impersonated)
	}
	if len(p.Orgs) != 2 {
		t.Fatalf("orgs = %+v, want 2", p.Orgs)
	}
	// Org names are what make an empty team answer diagnosable, so /self must ask
	// midPoint to resolve them.
	if query != "options=resolveNames" {
		t.Errorf("/self query = %q, want options=resolveNames", query)
	}
	if p.Orgs[0].Name != "dev-ops" || p.Orgs[0].Relation != "manager" || !p.Orgs[0].Manager {
		t.Errorf("managed org link = %+v", p.Orgs[0])
	}
	if p.Orgs[1].Name != "all-staff" || p.Orgs[1].Manager {
		t.Errorf("member org link = %+v", p.Orgs[1])
	}
}

func TestWhoamiResourceServerMode(t *testing.T) {
	var query string
	c := newSelfClient(t, selfWithNamedOrgs, &query)

	p, err := c.Whoami(WithPrincipal(context.Background(), "u-alice"))
	if err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if p.Mode != ModeResourceServer || !p.Impersonated {
		t.Errorf("mode = %q impersonated = %v, want resource-server/true", p.Mode, p.Impersonated)
	}
}

// A deployment can be configured for resource-server mode and still serve a
// request that carries no mapped principal; that request runs as the service
// account and must not be reported as if it were the end user.
func TestModeIsPerRequestNotPerConfig(t *testing.T) {
	c := NewClient(Config{
		BaseURL:      "http://example.invalid",
		Username:     "svc",
		Password:     "p",
		OIDCIssuer:   "https://issuer.example",
		OIDCAudience: "midpoint-mcp",
	})
	if got := c.Mode(context.Background()); got != ModePersonal {
		t.Errorf("mode without a principal = %q, want %q", got, ModePersonal)
	}
	if got := c.Mode(WithPrincipal(context.Background(), "u-alice")); got != ModeResourceServer {
		t.Errorf("mode with a principal = %q, want %q", got, ModeResourceServer)
	}
}

func TestOrgNamesFallsBackToOID(t *testing.T) {
	got := OrgNames([]OrgLink{{OID: "o-1", Name: "dev-ops"}, {OID: "o-2"}})
	if got != "dev-ops, o-2" {
		t.Errorf("OrgNames = %q, want %q", got, "dev-ops, o-2")
	}
}
