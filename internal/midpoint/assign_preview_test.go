package midpoint

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// previewMidpoint is a midPoint for one manager, Mia, and her report Bob.
// Mia's memberships: End user, Team lead ("people in orgs I manage", without
// dates), App approver (app roles as approver), No prod admin (a deny), a
// role she only owns, and an inactive superuser role. The searches answer the
// way midPoint 4.10.3 answered them on eval.
type previewMidpoint struct {
	t        *testing.T
	mu       sync.Mutex
	searches []string // "<collection> as <principal>: <filter>"
}

const (
	assignURI = "http://midpoint.evolveum.com/xml/ns/public/security/authorization-model-3#assign"
	allURI    = "http://midpoint.evolveum.com/xml/ns/public/security/authorization-3#all"
)

var previewRoles = map[string]string{
	"r-enduser": `{"oid":"r-enduser","@type":"c:RoleType","name":"end-user","displayName":"End user","authorization":` + endUserAuthorizations + `}`,
	"r-teamlead": `{"oid":"r-teamlead","@type":"c:RoleType","name":"team-lead","displayName":"Team lead","authorization":{"name":"assign-to-my-org",
		"action":"` + assignURI + `","phase":"request",
		"object":{"type":"#UserType","orgRelation":{"subjectRelation":"org:manager"}},
		"target":{"type":"#RoleType","filter":{"text":"requestable = true"}},"relation":"org:default",
		"exceptItem":"assignment/activation"}}`,
	"r-appapprover": `{"oid":"r-appapprover","@type":"c:RoleType","name":"app-approver","displayName":"App approver","authorization":{"name":"approve-app-roles",
		"action":"` + assignURI + `","phase":"request","object":{"special":"self"},
		"target":{"type":"#RoleType","archetypeRef":{"oid":"arch-app"}},"relation":"org:approver"}}`,
	"r-noprod": `{"oid":"r-noprod","@type":"c:RoleType","name":"no-prod","displayName":"No prod admin","authorization":{"name":"deny-prod-admin",
		"decision":"deny","action":"` + assignURI + `","object":{"special":"self"},
		"target":{"type":"#RoleType","filter":{"text":"name = \"prod-admin\""}}}}`,
	"r-owned": `{"oid":"r-owned","@type":"c:RoleType","name":"owned","authorization":{"name":"everything","action":"` + allURI + `"}}`,
	"r-old":   `{"oid":"r-old","@type":"c:RoleType","name":"old-super","lifecycleState":"draft","authorization":{"name":"everything","action":"` + allURI + `"}}`,
	"org-ops": `{"oid":"org-ops","@type":"c:OrgType","name":"ops","displayName":"Operations"}`,
}

var previewUsers = map[string]string{
	"u-mia": `{"oid":"u-mia","name":"mia","fullName":"Mia Manager",
		"parentOrgRef":[{"oid":"org-ops","relation":"org:manager","type":"c:OrgType"}],
		"roleMembershipRef":[
		 {"oid":"r-enduser","relation":"org:default","type":"c:RoleType"},{"oid":"r-teamlead","relation":"org:default","type":"c:RoleType"},
		 {"oid":"r-appapprover","relation":"org:default","type":"c:RoleType"},{"oid":"r-noprod","relation":"org:default","type":"c:RoleType"},
		 {"oid":"r-owned","relation":"org:owner","type":"c:RoleType"},{"oid":"r-old","relation":"org:default","type":"c:RoleType"},
		 {"oid":"org-ops","relation":"org:manager","type":"c:OrgType"}]}`,
	"u-bob": `{"oid":"u-bob","name":"bob","fullName":"Bob Stone",
		"parentOrgRef":{"oid":"org-ops","relation":"org:default","type":"c:OrgType"},
		"roleMembershipRef":[{"oid":"role-finance","relation":"org:default","type":"c:RoleType"},{"oid":"org-ops","relation":"org:default","type":"c:OrgType"}]}`,
}

func role(oid, name, display string) string {
	return `{"oid":"` + oid + `","name":"` + name + `","displayName":"` + display + `"}`
}

var (
	finance = role("role-finance", "finance-reports", "Finance reports")
	wiki    = role("role-wiki", "wiki-editor", "Wiki editor")
	prod    = role("prod-admin", "prod-admin", "Production admin")
	release = role("role-release", "release-manager", "Release manager")
)

func (m *previewMidpoint) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	as := r.Header.Get(SwitchToPrincipalHeader)
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/ws/rest/users/"):
		if r.URL.Query().Get("exclude") != "assignment" {
			m.t.Error("a person is read with their assignments")
		}
		u, ok := previewUsers[strings.TrimPrefix(r.URL.Path, "/ws/rest/users/")]
		if !ok {
			w.WriteHeader(404)
			return
		}
		_, _ = io.WriteString(w, `{"user":`+u+`}`)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/search"):
		var q struct {
			Query struct {
				Filter struct{ Text string } `json:"filter"`
			} `json:"query"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &q)
		f := q.Query.Filter.Text
		coll := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/ws/rest/"), "/search")
		m.mu.Lock()
		m.searches = append(m.searches, coll+" as "+as+": "+f)
		m.mu.Unlock()
		var objs []string
		switch coll {
		case "abstractRoles":
			if as != "" {
				m.t.Error("rules read as a person, not as the server's own account")
			}
			for oid, body := range previewRoles {
				if strings.Contains(f, `"`+oid+`"`) {
					objs = append(objs, body)
				}
			}
		case "roles":
			switch {
			case strings.Contains(f, `name = "prod-admin"`):
				objs = []string{prod}
			case strings.Contains(f, "requestable = true"):
				objs = []string{finance, wiki, prod}
			case strings.Contains(f, `archetypeRef matches (oid = "arch-app")`):
				objs = []string{release}
			}
		case "users":
			switch {
			case strings.Contains(f, `. inOrg "org-ops"`) && strings.Contains(f, `name = "bob"`):
				objs = []string{previewUsers["u-bob"]}
			case f == `name = "bob"`:
				objs = []string{previewUsers["u-bob"]}
			case strings.HasPrefix(f, `. inOrg "org-ops"`):
				objs = []string{previewUsers["u-bob"], previewUsers["u-mia"]}
			case strings.Contains(f, `. inOid ("u-mia")`):
				objs = []string{previewUsers["u-mia"]}
			}
		}
		_, _ = io.WriteString(w, `{"object":{"object":[`+strings.Join(objs, ",")+`]}}`)
	default:
		w.WriteHeader(404)
	}
}

func previewFor(t *testing.T) (*Preview, *previewMidpoint, context.Context) {
	t.Helper()
	m := &previewMidpoint{t: t}
	srv := httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(srv.Close)
	ctx := WithPrincipal(context.Background(), "u-mia")
	p, err := NewClient(Config{BaseURL: srv.URL}).RequestPreview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return p, m, ctx
}

// Rules come only from active roles held at login relations.
func TestPreviewRules(t *testing.T) {
	p, _, _ := previewFor(t)
	var labels []string
	for _, r := range p.Rules {
		labels = append(labels, r.Label())
	}
	want := []string{"End user › assign-requestable-roles", "Team lead › assign-to-my-org", "App approver › approve-app-roles", "No prod admin › deny-prod-admin"}
	if !reflect.DeepEqual(labels, want) || len(p.Unsure) != 0 {
		t.Errorf("rules %v unsure %v", labels, p.Unsure)
	}
}

// For herself, Mia may request the requestable roles as member, with dates,
// except the denied one, and app roles as approver.
func TestPreviewRolesForSelf(t *testing.T) {
	p, _, ctx := previewFor(t)
	offer, err := p.RolesFor(ctx, p.Me, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]OfferedRole{}
	for _, r := range offer.Roles {
		got[r.Name] = r
	}
	if len(offer.Roles) != 3 || got["prod-admin"].Name != "" {
		t.Fatalf("roles %+v", offer.Roles)
	}
	if f := got["finance-reports"]; !reflect.DeepEqual(f.Relations, []string{"default"}) || !f.Validity || !f.AllFields ||
		!reflect.DeepEqual(f.Because, []string{"End user › assign-requestable-roles", "Team lead › assign-to-my-org"}) {
		t.Errorf("finance %+v", f)
	}
	if r := got["release-manager"]; !reflect.DeepEqual(r.Relations, []string{"approver"}) || !reflect.DeepEqual(r.Because, []string{"App approver › approve-app-roles"}) {
		t.Errorf("release %+v", r)
	}
}

// For Bob, only the team-lead rule applies: requestable roles he doesn't hold
// yet, as member, without dates. Mia's own deny is about herself.
func TestPreviewRolesForReport(t *testing.T) {
	p, m, ctx := previewFor(t)
	bob, err := p.c.readPerson(ctx, "u-bob")
	if err != nil {
		t.Fatal(err)
	}
	offer, err := p.RolesFor(ctx, bob, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range offer.Roles {
		names = append(names, r.Name)
		if r.Validity || !reflect.DeepEqual(r.Relations, []string{"default"}) || !reflect.DeepEqual(r.Because, []string{"Team lead › assign-to-my-org"}) {
			t.Errorf("%s: %+v", r.Name, r)
		}
	}
	if !reflect.DeepEqual(names, []string{"prod-admin", "wiki-editor"}) {
		t.Errorf("roles %v (finance is held, wiki and prod are not)", names)
	}
	for _, s := range m.searches {
		if strings.HasPrefix(s, "abstractRoles as u-mia") {
			t.Errorf("rules read as the person: %s", s)
		}
	}
}

// Mia may request for herself and for the people in her org.
func TestPreviewPeople(t *testing.T) {
	p, _, ctx := previewFor(t)
	offer, err := p.People(ctx, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, person := range offer.People {
		got = append(got, person.Name+": "+strings.Join(person.Because, ", "))
	}
	want := []string{
		"mia: End user › assign-requestable-roles, Team lead › assign-to-my-org, App approver › approve-app-roles",
		"bob: Team lead › assign-to-my-org",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("people\n got %q\nwant %q", got, want)
	}
}

// A rule filter with $subject is filled with the requester's own value, and
// one the preview can't fill makes the rule unsure.
func TestPreviewSubstituteSubject(t *testing.T) {
	p := &Preview{Me: person{raw: map[string]json.RawMessage{
		"costCenter": json.RawMessage(`"CC-1"`),
		"name":       json.RawMessage(`{"orig":"mia","norm":"mia"}`),
		"extension":  json.RawMessage(`{"@ns":"urn:x","otherAccount":{"oid":"u-mia-admin","type":"c:UserType"}}`),
	}}}
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"costCenter = $subject/costCenter", `costCenter = "CC-1"`, true},
		{"name = $subject/c:name", `name = "mia"`, true},
		{"oid = $subject/extension/otherAccount", `oid = "u-mia-admin"`, true},
		{"costCenter = $subject/locality", "", false},
		{"name = $actor/name", "", false},
	} {
		got, ok := p.substituteSubject(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("%q: %q %v", tc.in, got, ok)
		}
	}
}

// Without the grant, midPoint answers the rules search with an empty list; the
// preview is then unavailable instead of offering nothing.
func TestPreviewUnreadableRoles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"user":`+previewUsers["u-mia"]+`}`)
			return
		}
		_, _ = io.WriteString(w, `{"object":{"object":[]}}`)
	}))
	t.Cleanup(srv.Close)
	_, err := NewClient(Config{BaseURL: srv.URL}).RequestPreview(WithPrincipal(context.Background(), "u-mia"))
	if !errors.Is(err, ErrPreviewUnavailable) || !strings.Contains(err.Error(), "could not read 6 of your 6 roles") {
		t.Errorf("err %v", err)
	}
	if _, err := NewClient(Config{BaseURL: srv.URL}).RequestPreview(context.Background()); !errors.Is(err, ErrPreviewUnavailable) {
		t.Errorf("personal mode: %v", err)
	}
}
