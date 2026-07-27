package midpoint

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTeamClient serves GET /self (with the given body) and POST /users/search
// (dispatching by the request's query-language filter).
func newTeamClient(t *testing.T, selfBody string, search func(filter string) string) *Client {
	return newTeamClientCfg(t, selfBody, search, FileConfig{})
}

func newTeamClientCfg(t *testing.T, selfBody string, search func(filter string) string, fc FileConfig) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/rest/self", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, selfBody)
	})
	mux.HandleFunc("POST /ws/rest/users/search", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var sr searchRequest
		_ = json.Unmarshal(body, &sr)
		filter := ""
		if sr.Query.Filter != nil {
			filter = sr.Query.Filter.Text
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, search(filter))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return NewClient(Config{BaseURL: srv.URL, Username: "svc", Password: "p", File: fc})
}

// selfWithOrgs renders a /self body whose parentOrgRef carries the given refs.
func selfWithOrgs(refs ...orgRef) string {
	items := make([]map[string]string, 0, len(refs))
	for _, r := range refs {
		items = append(items, map[string]string{"oid": r.OID, "relation": r.Relation, "type": "OrgType"})
	}
	b, _ := json.Marshal(map[string]any{
		"user": map[string]any{"oid": "me", "name": "mgr", "parentOrgRef": items},
	})
	return string(b)
}

func names(us []UserSummary) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = u.Name
	}
	return out
}

func TestListMyTeam(t *testing.T) {
	self := selfWithOrgs(
		orgRef{OID: "org-mgd", Relation: "org:manager"},
		orgRef{OID: "org-mem", Relation: "org:default"},
	)
	c := newTeamClient(t, self, func(filter string) string {
		// Reports = members (default relation) of the managed org; the caller
		// itself may come back in the member list and must be dropped.
		if strings.Contains(filter, "org-mgd") && strings.Contains(filter, "relation = default") {
			return `{"object":[{"oid":"me","name":"mgr"},{"oid":"r1","name":"rep-one"},{"oid":"r2","name":"rep-two"}]}`
		}
		return `{"object":[]}`
	})

	team, err := c.ListMyTeam(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListMyTeam: %v", err)
	}
	got := names(team.Users)
	if len(got) != 2 || got[0] != "rep-one" || got[1] != "rep-two" {
		t.Fatalf("team = %v, want [rep-one rep-two] (caller excluded)", got)
	}
	// The answer carries who it was for and which org links produced it.
	if team.Subject.Name != "mgr" || team.Subject.OID != "me" {
		t.Errorf("subject = %+v, want mgr/me", team.Subject)
	}
	if len(team.Orgs) != 1 || team.Orgs[0].OID != "org-mgd" || !team.Orgs[0].Manager {
		t.Errorf("orgs = %+v, want the one managed org", team.Orgs)
	}
}

func TestListMyTeamNonManager(t *testing.T) {
	self := selfWithOrgs(orgRef{OID: "org-mem", Relation: "org:default"})
	searched := false
	c := newTeamClient(t, self, func(string) string {
		searched = true
		return `{"object":[]}`
	})

	team, err := c.ListMyTeam(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListMyTeam: %v", err)
	}
	if len(team.Users) != 0 {
		t.Errorf("non-manager team = %v, want empty", names(team.Users))
	}
	if searched {
		t.Error("must not search for members when the caller manages no org")
	}
	// An empty answer must still say who it was for and that no managed org was
	// the reason — that is what tells "you manage nobody" from "not you at all".
	if team.Subject.Name != "mgr" {
		t.Errorf("subject = %+v, want mgr even when empty", team.Subject)
	}
	if len(team.Orgs) != 0 {
		t.Errorf("orgs = %+v, want none (caller manages no org)", team.Orgs)
	}
}

func TestListMyManagers(t *testing.T) {
	self := selfWithOrgs(
		orgRef{OID: "org-mgd", Relation: "org:manager"},
		orgRef{OID: "org-mem", Relation: "org:default"},
	)
	c := newTeamClient(t, self, func(filter string) string {
		// Managers = manager-relation links on the org the caller is a member of.
		if strings.Contains(filter, "org-mem") && strings.Contains(filter, "relation = manager") {
			return `{"object":[{"oid":"boss","name":"the-boss"}]}`
		}
		return `{"object":[]}`
	})

	mgrs, err := c.ListMyManagers(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListMyManagers: %v", err)
	}
	if got := names(mgrs.Users); len(got) != 1 || got[0] != "the-boss" {
		t.Fatalf("managers = %v, want [the-boss]", got)
	}
	if len(mgrs.Orgs) != 1 || mgrs.Orgs[0].OID != "org-mem" || mgrs.Orgs[0].Manager {
		t.Errorf("orgs = %+v, want the one member org", mgrs.Orgs)
	}
}

func TestRelationLocalAndIsManager(t *testing.T) {
	cases := map[string]bool{
		"org:manager": true,
		"manager":     true,
		"http://midpoint.evolveum.com/xml/ns/public/common/org-3#manager": true,
		"org:default": false,
		"":            false,
		"member":      false,
	}
	for rel, wantMgr := range cases {
		if got := (orgRef{Relation: rel}).isManagerOf(relationManager); got != wantMgr {
			t.Errorf("isManager(%q) = %v, want %v", rel, got, wantMgr)
		}
	}
}

// selfWithAssignedOrgs renders a /self body whose org links exist only as
// assignments — the shape a deployment has when parentOrgRef was never computed.
func selfWithAssignedOrgs(refs ...orgRef) string {
	items := make([]map[string]any, 0, len(refs))
	for _, r := range refs {
		items = append(items, map[string]any{
			"targetRef": map[string]string{"oid": r.OID, "relation": r.Relation, "type": "c:OrgType"},
		})
	}
	b, _ := json.Marshal(map[string]any{
		"user": map[string]any{"oid": "me", "name": "mgr", "assignment": items},
	})
	return string(b)
}

func TestListMyTeammates(t *testing.T) {
	self := selfWithOrgs(orgRef{OID: "org-mem", Relation: "org:default"})
	c := newTeamClient(t, self, func(filter string) string {
		// Peers = the OTHER members of the org the caller belongs to.
		if strings.Contains(filter, "org-mem") && strings.Contains(filter, "relation = default") {
			return `{"object":[{"oid":"me","name":"mgr"},{"oid":"p1","name":"peer-one"},{"oid":"p2","name":"peer-two"}]}`
		}
		return `{"object":[]}`
	})

	res, err := c.ListMyTeammates(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListMyTeammates: %v", err)
	}
	if got := names(res.Users); len(got) != 2 || got[0] != "peer-one" || got[1] != "peer-two" {
		t.Fatalf("teammates = %v, want [peer-one peer-two] (caller excluded)", got)
	}
}

// orgSource decides where org links are read from, so a deployment whose
// parentOrgRef is empty can still resolve teams from the assignments.
func TestOrgSourceSelectsWhereLinksComeFrom(t *testing.T) {
	both := `{"user":{"oid":"me","name":"mgr",
		"parentOrgRef":[{"oid":"org-computed","relation":"org:manager","type":"OrgType"}],
		"assignment":[{"targetRef":{"oid":"org-assigned","relation":"org:manager","type":"c:OrgType"}}]}}`

	tests := []struct {
		source string
		self   string
		want   []string
	}{
		{OrgSourceParentOrgRef, both, []string{"org-computed"}},
		{OrgSourceAssignment, both, []string{"org-assigned"}},
		{OrgSourceBoth, both, []string{"org-computed", "org-assigned"}},
		// fallback prefers the computed ref and only reaches for assignments
		// when there is none.
		{OrgSourceFallback, both, []string{"org-computed"}},
		{OrgSourceFallback, selfWithAssignedOrgs(orgRef{OID: "org-assigned", Relation: "org:manager"}), []string{"org-assigned"}},
		{OrgSourceParentOrgRef, selfWithAssignedOrgs(orgRef{OID: "org-assigned", Relation: "org:manager"}), nil},
	}
	for _, tt := range tests {
		t.Run(tt.source+"/"+strings.Join(tt.want, "+"), func(t *testing.T) {
			c := newTeamClientCfg(t, tt.self, func(string) string { return `{"object":[]}` },
				FileConfig{Team: TeamConfig{OrgSource: tt.source}})
			res, err := c.ListMyTeam(context.Background(), 0)
			if err != nil {
				t.Fatalf("ListMyTeam: %v", err)
			}
			got := make([]string, 0, len(res.Orgs))
			for _, o := range res.Orgs {
				got = append(got, o.OID)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("orgs = %v, want %v", got, tt.want)
			}
		})
	}
}

// The org selector is what stops "my teammates" from meaning "everyone in every
// org I happen to belong to".
func TestOrgSelectorNarrowsTeamQueries(t *testing.T) {
	self := selfWithOrgs(
		orgRef{OID: "org-team", Relation: "org:default"},
		orgRef{OID: "org-all-staff", Relation: "org:default"},
	)
	var asked []string
	c := newTeamClientCfg(t, self, func(filter string) string {
		asked = append(asked, filter)
		return `{"object":[{"oid":"p1","name":"peer-one"}]}`
	}, FileConfig{Team: TeamConfig{OrgOIDs: []string{"org-team"}}})

	res, err := c.ListMyTeammates(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListMyTeammates: %v", err)
	}
	if len(res.Orgs) != 1 || res.Orgs[0].OID != "org-team" {
		t.Fatalf("orgs = %+v, want only the selected org", res.Orgs)
	}
	if len(asked) != 1 || strings.Contains(asked[0], "org-all-staff") {
		t.Errorf("query %v must not reach the unselected org", asked)
	}
}

// whoami still reports the excluded orgs, flagged, so a selector that matches
// nothing is visible instead of just returning nobody.
func TestWhoamiReportsUnselectedOrgs(t *testing.T) {
	self := selfWithOrgs(
		orgRef{OID: "org-team", Relation: "org:default"},
		orgRef{OID: "org-all-staff", Relation: "org:default"},
	)
	c := newTeamClientCfg(t, self, func(string) string { return `{"object":[]}` },
		FileConfig{Team: TeamConfig{OrgOIDs: []string{"org-team"}}})

	p, err := c.Whoami(context.Background())
	if err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if len(p.Orgs) != 2 {
		t.Fatalf("whoami orgs = %+v, want both links", p.Orgs)
	}
	if !p.Orgs[0].Selected || p.Orgs[1].Selected {
		t.Errorf("selected flags = %v/%v, want true/false", p.Orgs[0].Selected, p.Orgs[1].Selected)
	}
}

// A deployment that declares its credentials shared must not have self-scoped
// tools answer for the service account.
func TestSharedCredentialRefusesSelfScopedTools(t *testing.T) {
	self := selfWithOrgs(orgRef{OID: "org-mgd", Relation: "org:manager"})
	fc := FileConfig{Identity: IdentityConfig{CredentialIsShared: true}}
	searches := 0
	c := newTeamClientCfg(t, self, func(string) string {
		searches++
		return `{"object":[]}`
	}, fc)

	if _, err := c.ListMyTeam(context.Background(), 0); !errors.Is(err, ErrNoCallerIdentity) {
		t.Errorf("ListMyTeam error = %v, want ErrNoCallerIdentity", err)
	}
	if _, err := c.ListMyTeammates(context.Background(), 0); !errors.Is(err, ErrNoCallerIdentity) {
		t.Errorf("ListMyTeammates error = %v, want ErrNoCallerIdentity", err)
	}
	if searches != 0 {
		t.Errorf("refused calls issued %d search(es); they must not reach midPoint", searches)
	}
	// whoami is the exception: it is how the caller learns why.
	if _, err := c.Whoami(context.Background()); err != nil {
		t.Errorf("Whoami must still answer: %v", err)
	}
	// A mapped end user carries a real identity, so the refusal lifts.
	if _, err := c.ListMyTeam(WithPrincipal(context.Background(), "u-alice"), 0); err != nil {
		t.Errorf("resource-server mode must not be refused: %v", err)
	}
}

// relations are deployment-specific; the configured local part must reach both
// the classification of the caller's own links and the membership query.
func TestConfiguredRelationsReachTheQuery(t *testing.T) {
	self := selfWithOrgs(orgRef{OID: "org-1", Relation: "org:org-owner"})
	var asked string
	c := newTeamClientCfg(t, self, func(filter string) string {
		asked = filter
		return `{"object":[{"oid":"r1","name":"rep-one"}]}`
	}, FileConfig{Team: TeamConfig{ManagerRelation: "org-owner", MemberRelation: "staff"}})

	res, err := c.ListMyTeam(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListMyTeam: %v", err)
	}
	if len(res.Orgs) != 1 || !res.Orgs[0].Manager {
		t.Fatalf("orgs = %+v, want org-1 classified as managed via org-owner", res.Orgs)
	}
	if !strings.Contains(asked, "relation = staff") {
		t.Errorf("filter %q must use the configured member relation", asked)
	}
}

func TestOrgMembersFilter(t *testing.T) {
	f := orgMembersFilter([]string{"o1", "o2"}, relationManager)
	for _, want := range []string{
		`parentOrgRef matches (oid = "o1" and relation = manager)`,
		`parentOrgRef matches (oid = "o2" and relation = manager)`,
		" or ",
	} {
		if !strings.Contains(f, want) {
			t.Errorf("filter %q missing %q", f, want)
		}
	}
}
