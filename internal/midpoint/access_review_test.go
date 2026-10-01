package midpoint

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAccessReviewEnrichment(t *testing.T) {
	mp := &fakeMidpoint{self: `{"user":{"oid":"manager","parentOrgRef":{"oid":"` + oidDevOps + `","targetName":"dev-ops","relation":"org:manager"}}}`, objects: map[string]string{
		"/users/" + oidBstone:   string(fixture(t, "user_requestee.json")),
		"/roles/" + oidBuild:    `{"role":{"oid":"` + oidBuild + `","name":"build-runner","displayName":"Build runner","description":"Runs build jobs."}}`,
		"/roles/" + oidArtifact: `{"role":{"oid":"` + oidArtifact + `","name":"artifact-read","displayName":"Artifact reader"}}`,
	}}
	c := mp.client(t, Config{})
	res, err := c.GetUserAssignments(context.Background(), oidBstone)
	if err != nil {
		t.Fatal(err)
	}
	if res.SubjectRelation != "direct-report" {
		t.Fatalf("subjectRelation = %q", res.SubjectRelation)
	}
	if a := res.Assignments[2]; a.Target == nil || a.Target.DisplayName != "Build runner" || a.Target.Description != "Runs build jobs." {
		t.Errorf("target = %+v", a.Target)
	}
	if res.Assignments[4].ValidFrom != "2027-01-01T00:00:00Z" {
		t.Error("validFrom missing")
	}
	m := res.Effective[len(res.Effective)-1]
	if m.Direct || m.DisplayName != "Artifact reader" || m.Via == nil || m.Via.DisplayName != "Build runner" {
		t.Errorf("included = %+v", m)
	}
	if n := mp.readsOf("/roles/" + oidBuild); n != 1 {
		t.Errorf("build read %d times", n)
	}
	if n := mp.readsOf("/archetypes/30000000-0000-0000-0000-000000000020"); n != 0 {
		t.Error("read archetype")
	}
	if a := res.Assignments[0].Target; a.Readable == nil || *a.Readable {
		t.Error("failed target read not marked unreadable")
	}
}

func TestAccessReviewSubjectRelations(t *testing.T) {
	for _, tc := range []struct {
		name, subject, member, want string
		team                        TeamConfig
	}{
		{name: "self wins", subject: "manager", member: "", want: "self"},
		{name: "default omitted", subject: "report", member: "", want: "direct-report"},
		{name: "default explicit", subject: "report", member: "org:default", want: "direct-report"},
		{name: "another manager", subject: "report", member: "org:manager", want: "other"},
		{name: "unselected org", subject: "report", member: "org:default", want: "other", team: TeamConfig{OrgOIDs: []string{"another"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mp := &fakeMidpoint{self: `{"user":{"oid":"manager","parentOrgRef":{"oid":"team","targetName":"Team","relation":"org:manager"}}}`}
			cfg := Config{}
			cfg.File.Team = tc.team
			c := mp.client(t, cfg)
			var u userJSON
			if err := json.Unmarshal([]byte(`{"oid":"`+tc.subject+`","parentOrgRef":{"oid":"team","relation":"`+tc.member+`"}}`), &u); err != nil {
				t.Fatal(err)
			}
			if got := c.assignmentSubjectRelation(context.Background(), u); got != tc.want {
				t.Errorf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestAccessReviewRevocation(t *testing.T) {
	for _, tc := range []struct {
		name, user, cases, want string
		applied                 bool
	}{
		{"removed", `{"user":{"oid":"` + oidBstone + `","fullName":"Bob Stone"}}`, `{"object":{"object":[]}}`, "removed", true},
		{"pending", strings.ReplaceAll(string(fixture(t, "user_requestee.json")), oidBuild, oidDbAdmin), string(fixture(t, "cases_search_removal.json")), "pending-approval", true},
		{"still", strings.ReplaceAll(string(fixture(t, "user_requestee.json")), oidBuild, oidDbAdmin), `{"object":{"object":[]}}`, "still-assigned", true},
		{"add is not removal", strings.ReplaceAll(string(fixture(t, "user_requestee.json")), oidBuild, oidDbAdmin), string(fixture(t, "cases_search_approver.json")), "still-assigned", true},
		{"preview", string(fixture(t, "user_requestee.json")), `{"object":{"object":[]}}`, "preview", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mp := &fakeMidpoint{search: tc.cases, objects: map[string]string{"/users/" + oidBstone: tc.user, "/roles/" + oidDbAdmin: string(fixture(t, "role_requested.json"))}}
			res, err := mp.client(t, Config{}).ReadRevocation(context.Background(), oidBstone, oidDbAdmin, tc.applied)
			if err != nil {
				t.Fatal(err)
			}
			if res.Outcome != tc.want || res.Role.DisplayName != "Database admin" || res.User.DisplayName != "Bob Stone" {
				t.Errorf("revocation %+v", res)
			}
			if (res.CaseOID != "") != (tc.want == "pending-approval") {
				t.Errorf("caseOid = %q", res.CaseOID)
			}
		})
	}
	mp := &fakeMidpoint{objects: map[string]string{}}
	if _, err := mp.client(t, Config{}).ReadRevocation(context.Background(), oidBstone, oidDbAdmin, true); err == nil {
		t.Error("failed read-back claimed an outcome")
	}
}

func TestCurrentRolesReadDisplayNames(t *testing.T) {
	mp := approverMidpoint(t)
	mp.objects["/roles/"+oidBuild] = `{"role":{"oid":"` + oidBuild + `","name":"build-runner","displayName":"Build runner"}}`
	got, err := mp.client(t, withJustification()).ListWorkItems(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range got.WorkItems[0].Context.RequesteeAccess.Roles {
		if m.OID == oidBuild {
			found = true
			if m.DisplayName != "Build runner" {
				t.Errorf("displayName = %q", m.DisplayName)
			}
		}
	}
	if !found {
		t.Fatal("build membership missing")
	}
	if n := mp.readsOf("/roles/" + oidBuild); n != 1 {
		t.Errorf("read role %d times", n)
	}
}
