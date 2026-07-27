package midpoint

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newSelfClient serves GET /self plus the by-OID re-read, recording the paths
// (with query) that were called.
func newSelfClient(t *testing.T, selfBody, userBody string, calls *[]string) *Client {
	t.Helper()
	mux := http.NewServeMux()
	record := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if r.URL.RawQuery != "" {
				path += "?" + r.URL.RawQuery
			}
			*calls = append(*calls, path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		}
	}
	mux.HandleFunc("GET /ws/rest/self", record(selfBody))
	mux.HandleFunc("GET /ws/rest/users/{oid}", record(userBody))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return NewClient(Config{BaseURL: srv.URL, Username: "svc", Password: "p"})
}

// midPoint ignores options=resolveNames on /self, so the org refs it returns
// carry no targetName…
const userUnnamedOrgs = `{"oid":"me","name":"svc-account","fullName":"Service Account",
	"parentOrgRef":[
		{"oid":"o-mgd","relation":"org:manager","type":"OrgType"},
		{"oid":"o-mem","relation":"org:default","type":"OrgType"}
	]}`

// …while the by-OID read does resolve them.
const userNamedOrgs = `{"oid":"me","name":"svc-account","fullName":"Service Account",
	"parentOrgRef":[
		{"oid":"o-mgd","relation":"org:manager","type":"OrgType","targetName":"dev-ops"},
		{"oid":"o-mem","relation":"org:default","type":"OrgType","targetName":"all-staff"}
	]}`

const (
	selfUnnamedOrgs = `{"user":` + userUnnamedOrgs + `}`
	selfNamedOrgs   = `{"user":` + userNamedOrgs + `}`
)

func TestWhoamiPersonalMode(t *testing.T) {
	var calls []string
	c := newSelfClient(t, selfUnnamedOrgs, selfNamedOrgs, &calls)

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
	// Org names are what make an empty team answer diagnosable, and midPoint does
	// not resolve them on /self — so an unnamed org link must trigger the by-OID
	// re-read that does.
	want := []string{"/ws/rest/self", "/ws/rest/users/me?options=resolveNames"}
	if len(calls) != 2 || calls[0] != want[0] || calls[1] != want[1] {
		t.Errorf("calls = %v, want %v", calls, want)
	}
	if p.Orgs[0].Name != "dev-ops" || p.Orgs[0].Relation != "manager" || !p.Orgs[0].Manager {
		t.Errorf("managed org link = %+v", p.Orgs[0])
	}
	if p.Orgs[1].Name != "all-staff" || p.Orgs[1].Manager {
		t.Errorf("member org link = %+v", p.Orgs[1])
	}
}

func TestWhoamiResourceServerMode(t *testing.T) {
	var calls []string
	c := newSelfClient(t, selfNamedOrgs, selfNamedOrgs, &calls)

	p, err := c.Whoami(WithPrincipal(context.Background(), "u-alice"))
	if err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if p.Mode != ModeResourceServer || !p.Impersonated {
		t.Errorf("mode = %q impersonated = %v, want resource-server/true", p.Mode, p.Impersonated)
	}
}

// When /self already resolved the org names, the extra read must not happen.
func TestSelfUserSkipsRereadWhenOrgsAreNamed(t *testing.T) {
	var calls []string
	c := newSelfClient(t, selfNamedOrgs, `{"user":{"oid":"unexpected"}}`, &calls)

	p, err := c.Whoami(context.Background())
	if err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if len(calls) != 1 || calls[0] != "/ws/rest/self" {
		t.Errorf("calls = %v, want only /self", calls)
	}
	if len(p.Orgs) != 2 || p.Orgs[0].Name != "dev-ops" {
		t.Errorf("orgs = %+v", p.Orgs)
	}
}

// A user with no org links needs no re-read either.
func TestSelfUserSkipsRereadWhenNoOrgs(t *testing.T) {
	var calls []string
	c := newSelfClient(t, `{"user":{"oid":"me","name":"svc-account"}}`, `{"user":{"oid":"unexpected"}}`, &calls)

	if _, err := c.Whoami(context.Background()); err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if len(calls) != 1 {
		t.Errorf("calls = %v, want only /self", calls)
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
