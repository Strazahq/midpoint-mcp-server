package midpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Fixture OIDs (testdata, recorded on midPoint 4.10.3 and made neutral).
const (
	oidDlee       = "10000000-0000-0000-0000-0000000000a1" // approver, approver of db-admin
	oidMkovac     = "10000000-0000-0000-0000-0000000000a2" // second approver
	oidBstone     = "10000000-0000-0000-0000-0000000000b1" // requestee and requester
	oidJdoe       = "10000000-0000-0000-0000-0000000000c1" // manager of dev-ops, delegate
	oidDbAdmin    = "20000000-0000-0000-0000-0000000000f1" // the requested role
	oidBuild      = "20000000-0000-0000-0000-0000000000f3" // assigned to the requestee
	oidArtifact   = "20000000-0000-0000-0000-0000000000f4" // included in build-runner
	oidFinance    = "20000000-0000-0000-0000-0000000000e1" // requested from the managers
	oidDevOps     = "30000000-0000-0000-0000-000000000001"
	oidTwoStepCas = "40000000-0000-0000-0000-000000000001"
)

// fakeMidpoint serves /self, one case search, case GETs and object GETs, and
// counts every GET. Paths it does not know answer status (404 by default).
type fakeMidpoint struct {
	self    string
	search  string
	objects map[string]string // GET path below /ws/rest → body
	status  int
	// object, when set, answers GETs not in objects before status does.
	object func(path string) (string, bool)

	mu   sync.Mutex
	gets map[string]int
}

func (f *fakeMidpoint) client(t *testing.T, cfg Config) *Client {
	t.Helper()
	f.gets = map[string]int{}
	if f.status == 0 {
		f.status = http.StatusNotFound
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/ws/rest")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && path == "/cases/search":
			_, _ = w.Write([]byte(f.search))
			return
		case r.Method != http.MethodGet:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		f.mu.Lock()
		f.gets[path]++
		f.mu.Unlock()
		if path == "/self" {
			_, _ = w.Write([]byte(f.self))
			return
		}
		if body, ok := f.objects[path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		if f.object != nil {
			if body, ok := f.object(path); ok {
				_, _ = w.Write([]byte(body))
				return
			}
		}
		w.WriteHeader(f.status)
	}))
	t.Cleanup(srv.Close)
	cfg.BaseURL, cfg.Username, cfg.Password = srv.URL, "u", "p"
	return NewClient(cfg)
}

func (f *fakeMidpoint) readsOf(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets[path]
}

// withJustification is a server whose request form was not read: request
// fields are named, not labelled.
func withJustification() Config { return Config{} }

func userBody(oid, name, fullName string) string {
	return fmt.Sprintf(`{"user":{"oid":%q,"name":%q,"fullName":%q}}`, oid, name, fullName)
}

// approverMidpoint is the stock approver's view: list_work_items as dlee.
func approverMidpoint(t *testing.T) *fakeMidpoint {
	return &fakeMidpoint{
		self:   string(fixture(t, "self_approver.json")),
		search: string(fixture(t, "cases_search_approver.json")),
		objects: map[string]string{
			"/users/" + oidBstone:  string(fixture(t, "user_requestee.json")),
			"/users/" + oidMkovac:  userBody(oidMkovac, "mkovac", "Mia Kovac"),
			"/roles/" + oidDbAdmin: string(fixture(t, "role_requested.json")),
		},
	}
}

func ptrFalse() *bool { f := false; return &f }

// Every field of the context, from the answers a stock approver gets
// (End user and Approver roles only), recorded on midPoint 4.10.3.
func TestListWorkItemsContext(t *testing.T) {
	mp := approverMidpoint(t)
	c := mp.client(t, withJustification())

	res, err := c.ListWorkItems(context.Background(), 50)
	if err != nil {
		t.Fatalf("ListWorkItems: %v", err)
	}
	if len(res.WorkItems) != 1 || res.WorkItems[0].ID != "6" {
		t.Fatalf("work items = %+v, want the approver's own item 6", res.WorkItems)
	}
	bstone := ObjectRef{OID: oidBstone, Type: "User", Name: "bstone", DisplayName: "Bob Stone"}
	want := WorkItemContext{
		Change:    ChangeAdd,
		Requester: bstone,
		Requestee: PersonRef{ObjectRef: bstone, Status: "enabled"},
		Target: TargetRef{
			ObjectRef:   ObjectRef{OID: oidDbAdmin, Type: "Role", Name: "db-admin", DisplayName: "Database admin"},
			Description: "Full access to the production databases.",
			RiskLevel:   "high",
		},
		RequestDetails: wantJustification,
		Validity:       &Validity{ValidFrom: "2026-10-02T00:00:00+02:00", ValidTo: "2026-10-31T23:59:59+01:00"},
		RequestedAt:    "2026-10-01T10:53:58.855Z",
		CreatedAt:      "2026-10-01T10:53:58.967Z",
		Deadline:       "2026-10-04T10:53:58.967Z",
		Stage:          StageInfo{Number: 1, Count: 2, Name: "Team leads", Strategy: StrategyAllMustAgree},
		// dlee manages no org; their own roleMembershipRef holds db-admin
		// with relation org:approver.
		Reason:      ReasonRoleApprover,
		CoAssignees: []ObjectRef{},
		// mkovac's work item is not in the case a stock approver reads; the
		// stage's approverRef names them.
		StageApprovers: []ObjectRef{{OID: oidMkovac, Type: "User", Name: "mkovac", DisplayName: "Mia Kovac"}},
		RequesteeAccess: RequesteeAccess{Visible: true, Roles: []RoleMembership{
			{OID: "00000000-0000-0000-0000-000000000008", Name: "End user", Type: "Role", Direct: true},
			{OID: oidDevOps, Name: "dev-ops", Type: "Org", Direct: true},
			{OID: oidBuild, Name: "build-runner", Type: "Role", Direct: true},
			{OID: oidArtifact, Name: "artifact-read", Type: "Role",
				Via: &ObjectRef{OID: oidBuild, Type: "Role", Name: "build-runner"}},
		}},
	}
	if got := res.WorkItems[0].Context; !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", " ")
		wantJSON, _ := json.MarshalIndent(want, "", " ")
		t.Errorf("context\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
	// The requester is the requestee: one read serves both.
	if n := mp.readsOf("/users/" + oidBstone); n != 1 {
		t.Errorf("requestee read %d times, want 1", n)
	}
}

// A manager who is also a delegate: a work item with two assignees, and a
// request the requestee's managers decide.
func TestListWorkItemsContextAsManager(t *testing.T) {
	mp := &fakeMidpoint{
		self: `{"user":{"oid":"` + oidJdoe + `","name":"jdoe","fullName":"Jane Doe",
			"parentOrgRef":{"oid":"` + oidDevOps + `","relation":"org:manager","type":"c:OrgType","targetName":"dev-ops"}}}`,
		search: string(fixture(t, "cases_search_delegate.json")),
		objects: map[string]string{
			"/users/" + oidBstone:  string(fixture(t, "user_requestee.json")),
			"/users/" + oidDlee:    userBody(oidDlee, "dlee", "Dana Lee"),
			"/users/" + oidMkovac:  userBody(oidMkovac, "mkovac", "Mia Kovac"),
			"/roles/" + oidDbAdmin: string(fixture(t, "role_requested.json")),
			"/roles/" + oidFinance: `{"role":{"oid":"` + oidFinance + `","name":"finance-reports","displayName":"Finance reports"}}`,
		},
	}
	res, err := mp.client(t, withJustification()).ListWorkItems(context.Background(), 50)
	if err != nil {
		t.Fatalf("ListWorkItems: %v", err)
	}
	if len(res.WorkItems) != 2 {
		t.Fatalf("work items = %d, want 2", len(res.WorkItems))
	}
	delegated, managers := res.WorkItems[0].Context, res.WorkItems[1].Context

	// jdoe manages dev-ops, of which the requestee is a member: manager comes
	// first (D29).
	for i, wc := range []WorkItemContext{delegated, managers} {
		if wc.Reason != ReasonManager {
			t.Errorf("item %d: reason = %q, want manager", i, wc.Reason)
		}
	}
	mkovac := ObjectRef{OID: oidMkovac, Type: "User", Name: "mkovac", DisplayName: "Mia Kovac"}
	dlee := ObjectRef{OID: oidDlee, Type: "User", Name: "dlee", DisplayName: "Dana Lee"}
	if !reflect.DeepEqual(delegated.CoAssignees, []ObjectRef{mkovac}) {
		t.Errorf("coAssignees = %+v, want mkovac", delegated.CoAssignees)
	}
	// From the schema: dlee; mkovac is a co-assignee and jdoe is the caller.
	if !reflect.DeepEqual(delegated.StageApprovers, []ObjectRef{dlee}) {
		t.Errorf("stageApprovers = %+v, want dlee", delegated.StageApprovers)
	}

	wantStage := StageInfo{Number: 1, Count: 1, Name: "Requestee's manager", Strategy: StrategyFirstDecides}
	if managers.Stage != wantStage {
		t.Errorf("stage = %+v, want %+v", managers.Stage, wantStage)
	}
	// The other manager's item is hidden and an expression chose the
	// approvers, so nobody else is known.
	if len(managers.StageApprovers) != 0 || len(managers.CoAssignees) != 0 {
		t.Errorf("stageApprovers = %+v, coAssignees = %+v, want none", managers.StageApprovers, managers.CoAssignees)
	}
	if managers.Deadline != "" || managers.Validity != nil || managers.RequestDetails != nil {
		t.Errorf("deadline=%q validity=%+v details=%+v, want none", managers.Deadline, managers.Validity, managers.RequestDetails)
	}
	if managers.Target.DisplayName != "Finance reports" {
		t.Errorf("target = %+v", managers.Target)
	}
	// Reads are shared across the cases of one answer.
	if n := mp.readsOf("/users/" + oidBstone); n != 1 {
		t.Errorf("requestee read %d times across two cases, want 1", n)
	}
}

// When midPoint refuses every read, the list still answers: the people and
// the role are marked unreadable and keep the names the case gave them.
func TestListWorkItemsFailedReads(t *testing.T) {
	mp := approverMidpoint(t)
	mp.objects = nil
	mp.status = http.StatusForbidden
	res, err := mp.client(t, withJustification()).ListWorkItems(context.Background(), 50)
	if err != nil {
		t.Fatalf("ListWorkItems with every read refused: %v", err)
	}
	wc := res.WorkItems[0].Context
	hidden := ObjectRef{OID: oidBstone, Type: "User", Name: "bstone", Readable: ptrFalse()}
	if !reflect.DeepEqual(wc.Requester, hidden) || !reflect.DeepEqual(wc.Requestee, PersonRef{ObjectRef: hidden}) {
		t.Errorf("requester = %+v, requestee = %+v, want unreadable bstone", wc.Requester, wc.Requestee)
	}
	wantTarget := TargetRef{ObjectRef: ObjectRef{OID: oidDbAdmin, Type: "Role", Name: "db-admin", Readable: ptrFalse()}}
	if !reflect.DeepEqual(wc.Target, wantTarget) {
		t.Errorf("target = %+v, want %+v", wc.Target, wantTarget)
	}
	if wc.RequesteeAccess.Visible || len(wc.RequesteeAccess.Roles) != 0 || wc.RequesteeAccess.Roles == nil {
		t.Errorf("requesteeAccess = %+v, want not visible with an empty list", wc.RequesteeAccess)
	}
	// The approver's own memberships still say why.
	if wc.Reason != ReasonRoleApprover {
		t.Errorf("reason = %q, want roleApprover", wc.Reason)
	}
	if len(wc.StageApprovers) != 1 || wc.StageApprovers[0].Readable == nil || *wc.StageApprovers[0].Readable {
		t.Errorf("stageApprovers = %+v, want mkovac unreadable", wc.StageApprovers)
	}
	// What the case itself says needs no read.
	if len(wc.RequestDetails) == 0 || wc.Validity == nil || wc.RequestedAt == "" || wc.Stage.Count != 2 {
		t.Errorf("case-derived fields missing: %+v", wc)
	}
}

// A user read that succeeds but carries neither memberships nor assignments
// (midPoint dropped the items the reader may not see) is not visible access.
func TestRequesteeAccessHiddenItems(t *testing.T) {
	r := newRefReader(nil)
	if acc := r.requesteeAccess(context.Background(), userJSON{OID: "u"}, readOK); acc.Visible || acc.Roles == nil {
		t.Errorf("access = %+v, want not visible", acc)
	}
	u := userJSON{OID: "u", Assignment: flexSlice{json.RawMessage(`{"targetRef":{"oid":"r","type":"c:RoleType"}}`)}}
	if acc := r.requesteeAccess(context.Background(), u, readOK); !acc.Visible || len(acc.Roles) != 0 {
		t.Errorf("access = %+v, want visible with no roles in effect", acc)
	}
}

// casesSearch wraps case objects in a search answer.
func casesSearch(cases ...string) string {
	return `{"object":{"object":[` + strings.Join(cases, ",") + `]}}`
}

// openCase is a minimal open case with one work item for assignee.
func openCase(oid, requestee, assignee string) string {
	return fmt.Sprintf(`{"oid":%q,"state":"open",
		"objectRef":{"oid":%q,"type":"c:UserType"},"requestorRef":{"oid":%q,"type":"c:UserType"},
		"targetRef":{"oid":"role-1","type":"c:RoleType"},
		"workItem":{"@id":1,"stageNumber":1,"assigneeRef":{"oid":%q,"type":"c:UserType"}}}`,
		oid, requestee, requestee, assignee)
}

// Several items naming the same people and role read each once.
func TestListWorkItemsDeduplicatesReads(t *testing.T) {
	mp := &fakeMidpoint{
		self:   userBody("u-self", "self", "Self"),
		search: casesSearch(openCase("c1", "u-a", "u-self"), openCase("c2", "u-a", "u-self"), openCase("c3", "u-b", "u-self")),
		object: func(path string) (string, bool) {
			if oid, ok := strings.CutPrefix(path, "/users/"); ok {
				return userBody(oid, oid, "Full "+oid), true
			}
			return `{"role":{"oid":"role-1","name":"role-1"}}`, path == "/roles/role-1"
		},
	}
	res, err := mp.client(t, Config{}).ListWorkItems(context.Background(), 50)
	if err != nil || len(res.WorkItems) != 3 {
		t.Fatalf("ListWorkItems = %d items, %v", len(res.WorkItems), err)
	}
	for path, want := range map[string]int{"/users/u-a": 1, "/users/u-b": 1, "/roles/role-1": 1} {
		if n := mp.readsOf(path); n != want {
			t.Errorf("%s read %d times, want %d", path, n, want)
		}
	}
}

// Only the first inboxEnrichLimit items read from midPoint; a later item
// still uses what an earlier one read.
func TestListWorkItemsEnrichmentCap(t *testing.T) {
	var cases []string
	for i := 0; i <= inboxEnrichLimit; i++ {
		cases = append(cases, openCase(fmt.Sprintf("c%d", i), fmt.Sprintf("u-%d", i), "u-self"))
	}
	cases = append(cases, openCase("c-again", "u-0", "u-self")) // read for the first item
	mp := &fakeMidpoint{
		self:   userBody("u-self", "self", "Self"),
		search: casesSearch(cases...),
		object: func(path string) (string, bool) {
			if oid, ok := strings.CutPrefix(path, "/users/"); ok {
				return userBody(oid, oid, "Full "+oid), true
			}
			return "", false
		},
	}
	res, err := mp.client(t, Config{}).ListWorkItems(context.Background(), 100)
	if err != nil {
		t.Fatalf("ListWorkItems: %v", err)
	}
	if len(res.WorkItems) != inboxEnrichLimit+2 {
		t.Fatalf("work items = %d, want %d", len(res.WorkItems), inboxEnrichLimit+2)
	}
	userReads := 0
	for path, n := range mp.gets {
		if strings.HasPrefix(path, "/users/") {
			userReads += n
		}
	}
	if userReads != inboxEnrichLimit {
		t.Errorf("%d user reads, want %d", userReads, inboxEnrichLimit)
	}
	last := res.WorkItems[inboxEnrichLimit].Context
	if last.Requestee.DisplayName != "" || last.Requestee.Readable != nil || last.RequesteeAccess.Visible {
		t.Errorf("item past the cap = %+v, want names from the case only and nothing marked unreadable", last.Requestee)
	}
	if again := res.WorkItems[inboxEnrichLimit+1].Context; again.Requestee.DisplayName != "Full u-0" {
		t.Errorf("item past the cap with a requestee already read = %+v", again.Requestee)
	}
}

// refsJSON is a flexSlice of references.
func refsJSON(refs ...string) flexSlice {
	out := flexSlice{}
	for _, r := range refs {
		out = append(out, json.RawMessage(r))
	}
	return out
}

func TestReasonOrder(t *testing.T) {
	member := `{"oid":"org-1","relation":"org:default","type":"c:OrgType","targetName":"team"}`
	manager := `{"oid":"org-1","relation":"org:manager","type":"c:OrgType","targetName":"team"}`
	approverOf := `{"oid":"role-1","relation":"org:approver","type":"c:RoleType"}`
	ownerOf := `{"oid":"role-1","relation":"org:owner","type":"c:RoleType"}`
	plainMember := `{"oid":"role-1","relation":"org:default","type":"c:RoleType"}`

	requestee := userJSON{OID: "u-r", ParentOrgRef: refsJSON(member)}
	for _, tc := range []struct {
		name          string
		self          userJSON
		team          TeamConfig
		requestee     userJSON
		requesteeRead bool
		want          string
	}{
		{"manager first", userJSON{ParentOrgRef: refsJSON(manager), RoleMembershipRef: refsJSON(approverOf)}, TeamConfig{}, requestee, true, ReasonManager},
		{"requestee unreadable", userJSON{ParentOrgRef: refsJSON(manager), RoleMembershipRef: refsJSON(approverOf)}, TeamConfig{}, requestee, false, ReasonRoleApprover},
		{"manager link not selected", userJSON{ParentOrgRef: refsJSON(manager)}, TeamConfig{OrgOIDs: []string{"org-2"}}, requestee, true, ReasonAssigned},
		{"requestee manages the org too", userJSON{ParentOrgRef: refsJSON(manager)}, TeamConfig{}, userJSON{ParentOrgRef: refsJSON(manager)}, true, ReasonAssigned},
		{"caller only a member", userJSON{ParentOrgRef: refsJSON(member)}, TeamConfig{}, requestee, true, ReasonAssigned},
		{"approver before owner", userJSON{RoleMembershipRef: refsJSON(ownerOf, approverOf)}, TeamConfig{}, requestee, true, ReasonRoleApprover},
		{"owner", userJSON{RoleMembershipRef: refsJSON(ownerOf, plainMember)}, TeamConfig{}, requestee, true, ReasonRoleOwner},
		{"holding the role is no reason", userJSON{RoleMembershipRef: refsJSON(plainMember)}, TeamConfig{}, requestee, true, ReasonAssigned},
		{"nothing", userJSON{}, TeamConfig{}, requestee, true, ReasonAssigned},
	} {
		if got := reason(tc.self, tc.team, tc.requestee, tc.requesteeRead, "role-1"); got != tc.want {
			t.Errorf("%s: reason = %q, want %q", tc.name, got, tc.want)
		}
	}
	// A requestee whose membership midPoint wrote without a relation is a
	// member.
	bare := userJSON{ParentOrgRef: refsJSON(`{"oid":"org-1","type":"c:OrgType"}`)}
	if got := reason(userJSON{ParentOrgRef: refsJSON(manager)}, TeamConfig{}, bare, true, "role-1"); got != ReasonManager {
		t.Errorf("bare membership: reason = %q, want manager", got)
	}
}

func workItem(id string, stage int, open bool, assignees ...string) workItemJSON {
	wi := workItemJSON{ID: flexID{id}, StageNumber: stage}
	for _, a := range assignees {
		wi.AssigneeRef = append(wi.AssigneeRef, json.RawMessage(`{"oid":"`+a+`","type":"c:UserType","targetName":"`+a+`"}`))
	}
	if !open {
		wi.CloseTimestamp = "2026-10-01T00:00:00Z"
	}
	return wi
}

func refOIDs(refs []ObjectRef) []string {
	out := []string{}
	for _, r := range refs {
		out = append(out, r.OID)
	}
	return out
}

// With every work item visible, co-assignees and stage approvers come from
// the items; the caller is never among them, and the schema is not used.
func TestOtherApproversFromVisibleItems(t *testing.T) {
	items := []workItemJSON{
		workItem("1", 1, true, "me", "x"),
		workItem("2", 1, true, "y", "x"),
		workItem("3", 1, false, "z"), // decided
		workItem("4", 2, true, "w"),  // a later step
		workItem("5", 1, true, "me"), // another item of the caller's
	}
	a := approval{stages: []stageDef{{StageInfo: StageInfo{Number: 1}, approvers: decodeRefs(refsJSON(`{"oid":"s"}`))}}}
	e := &enricher{reader: newRefReader(nil), self: userJSON{OID: "me"}}
	e.reader.closed = true // no reads: names come from the work items
	wc := e.workItemContext(context.Background(), caseJSON{State: "open"}, a, items, items[0], false)
	if got := refOIDs(wc.CoAssignees); !reflect.DeepEqual(got, []string{"x"}) {
		t.Errorf("coAssignees = %v, want [x]", got)
	}
	if got := refOIDs(wc.StageApprovers); !reflect.DeepEqual(got, []string{"y"}) {
		t.Errorf("stageApprovers = %v, want [y]", got)
	}

	// The same item when the caller sees only its own items: the schema's
	// approvers, still without the caller and the co-assignees.
	own := []workItemJSON{workItem("1", 1, true, "me", "x")}
	a.stages[0].approvers = decodeRefs(refsJSON(`{"oid":"me"}`, `{"oid":"x"}`, `{"oid":"s"}`))
	wc = e.workItemContext(context.Background(), caseJSON{State: "open"}, a, own, own[0], false)
	if got := refOIDs(wc.StageApprovers); !reflect.DeepEqual(got, []string{"s"}) {
		t.Errorf("stageApprovers from the schema = %v, want [s]", got)
	}
}

func TestNextApprovers(t *testing.T) {
	a := approval{stages: []stageDef{
		{StageInfo: StageInfo{Number: 1}, approvers: decodeRefs(refsJSON(`{"oid":"me"}`, `{"oid":"other"}`))},
		{StageInfo: StageInfo{Number: 2}, approvers: decodeRefs(refsJSON(`{"oid":"later"}`))},
	}}
	oids := func(refs []refJSON) []string {
		out := []string{}
		for _, r := range refs {
			out = append(out, r.OID)
		}
		return out
	}
	for _, tc := range []struct {
		name  string
		cj    caseJSON
		items []workItemJSON
		want  []string
	}{
		{"visible open items", caseJSON{State: "open", StageNumber: 1},
			[]workItemJSON{workItem("1", 1, false, "me"), workItem("2", 1, true, "other", "deputy")}, []string{"other", "deputy"}},
		{"others hidden, same step", caseJSON{State: "open", StageNumber: 1},
			[]workItemJSON{workItem("1", 1, false, "me")}, []string{"other"}},
		{"others hidden, next step", caseJSON{State: "open", StageNumber: 2},
			[]workItemJSON{workItem("1", 1, false, "me")}, []string{"later"}},
		{"closed case", caseJSON{State: "closed", StageNumber: 2},
			[]workItemJSON{workItem("1", 1, false, "me")}, []string{}},
		{"closing case", caseJSON{State: "closing", StageNumber: 1},
			[]workItemJSON{workItem("1", 1, true, "other")}, []string{}},
	} {
		if got := oids(nextApprovers(tc.cj, a, tc.items, "me")); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: nextApprovers = %v, want %v", tc.name, got, tc.want)
		}
	}
	// Without the caller's identity nothing is guessed from the schema.
	if got := nextApprovers(caseJSON{State: "open", StageNumber: 1}, a, []workItemJSON{workItem("1", 1, false, "me")}, ""); len(got) != 0 {
		t.Errorf("nextApprovers without self = %v, want none", oids(got))
	}
}

func TestInEffectRoles(t *testing.T) {
	u := userJSON{
		Assignment: refsJSON(`{"targetRef":{"oid":"r-direct","type":"c:RoleType"}}`),
		RoleMembershipRef: refsJSON(
			`{"oid":"r-direct","type":"c:RoleType","targetName":"direct"}`,
			`{"oid":"r-other","type":"c:RoleType","targetName":"other"}`,
			`{"oid":"arch","type":"c:ArchetypeType","targetName":"employee"}`,
			`{"oid":"r-gov","type":"c:RoleType","relation":"org:approver"}`,
			`{"oid":"r-gov2","type":"c:RoleType","relation":"org:owner"}`,
			`{"oid":"r-direct","type":"c:RoleType","relation":"org:manager","targetName":"direct"}`,
			`{"oid":"r-both","type":"c:RoleType","@metadata":[
				{"provenance":{"assignmentPath":{"segment":[{"targetRef":{"oid":"r-direct"}},{"targetRef":{"oid":"r-both"}}]}}},
				{"provenance":{"assignmentPath":{"segment":{"targetRef":{"oid":"r-both"}}}}}]}`,
			`{"oid":"r-induced","type":"c:RoleType","@metadata":
				{"provenance":{"assignmentPath":{"segment":[{"targetRef":{"oid":"r-direct","type":"c:RoleType"}},{"targetRef":{"oid":"r-induced"}}]}}}}`,
		),
	}
	want := []RoleMembership{
		{OID: "r-direct", Name: "direct", Type: "Role", Direct: true}, // no metadata: assigned
		{OID: "r-other", Name: "other", Type: "Role"},                 // no metadata, not assigned: no via
		{OID: "r-both", Type: "Role", Direct: true},                   // one path direct
		{OID: "r-induced", Type: "Role", Via: &ObjectRef{OID: "r-direct", Type: "Role", Name: "direct"}},
	}
	if got := inEffectRoles(u); !reflect.DeepEqual(got, want) {
		t.Errorf("roles\n got: %+v\nwant: %+v", got, want)
	}
}

// get_case as the requester, once the case closed: every reference named,
// every work item with its people, times and comment.
func TestGetCaseEnriched(t *testing.T) {
	mp := &fakeMidpoint{
		self: userBody(oidBstone, "bstone", "Bob Stone"),
		objects: map[string]string{
			"/cases/" + oidTwoStepCas: string(fixture(t, "case_get_closed.json")),
			"/users/" + oidBstone:     string(fixture(t, "user_requestee.json")),
			"/users/" + oidDlee:       userBody(oidDlee, "dlee", "Dana Lee"),
			"/users/" + oidMkovac:     userBody(oidMkovac, "mkovac", "Mia Kovac"),
			"/users/" + oidJdoe:       userBody(oidJdoe, "jdoe", "Jane Doe"),
			"/roles/" + oidDbAdmin:    string(fixture(t, "role_requested.json")),
		},
	}
	d, err := mp.client(t, withJustification()).GetCase(WithSelfMemo(context.Background()), oidTwoStepCas)
	if err != nil {
		t.Fatalf("GetCase: %v", err)
	}
	bstone := &ObjectRef{OID: oidBstone, Type: "User", Name: "bstone", DisplayName: "Bob Stone"}
	if !reflect.DeepEqual(d.ObjectRef, bstone) || !reflect.DeepEqual(d.RequestorRef, bstone) {
		t.Errorf("objectRef = %+v, requestorRef = %+v", d.ObjectRef, d.RequestorRef)
	}
	if want := (&ObjectRef{OID: oidDbAdmin, Type: "Role", Name: "db-admin", DisplayName: "Database admin"}); !reflect.DeepEqual(d.TargetRef, want) {
		t.Errorf("targetRef = %+v", d.TargetRef)
	}
	if d.Change != ChangeAdd || d.RequestedAt != "2026-10-01T10:53:58.855Z" || d.ClosedAt != "2026-10-01T10:58:03.417Z" ||
		!reflect.DeepEqual(d.RequestDetails, wantJustification) || d.Validity == nil || d.Validity.ValidTo != "2026-10-31T23:59:59+01:00" {
		t.Errorf("case fields = change %q requested %q closed %q details %+v validity %+v",
			d.Change, d.RequestedAt, d.ClosedAt, d.RequestDetails, d.Validity)
	}
	if d.Stage != nil {
		t.Errorf("stage = %+v, want none for a closed case", d.Stage)
	}
	if !reflect.DeepEqual(d.Stages, twoSteps) {
		t.Errorf("stages = %+v", d.Stages)
	}
	if d.Outcome != "approve" || d.State != "closed" {
		t.Errorf("state %q outcome %q", d.State, d.Outcome)
	}
	if len(d.NextApprovers) != 0 || d.NextApprovers == nil {
		t.Errorf("nextApprovers = %+v, want an empty list for a closed case", d.NextApprovers)
	}

	dlee := ObjectRef{OID: oidDlee, Type: "User", Name: "dlee", DisplayName: "Dana Lee"}
	mkovac := ObjectRef{OID: oidMkovac, Type: "User", Name: "mkovac", DisplayName: "Mia Kovac"}
	jdoe := ObjectRef{OID: oidJdoe, Type: "User", Name: "jdoe", DisplayName: "Jane Doe"}
	want := []CaseWorkItem{
		{WorkItem: WorkItem{CaseOID: oidTwoStepCas, ID: "6", Assignee: "dlee", Stage: 1, Outcome: "approve"},
			Assignees: []ObjectRef{dlee}, CreatedAt: "2026-10-01T10:53:58.967Z", ClosedAt: "2026-10-01T10:57:35.330Z",
			Deadline: "2026-10-04T10:53:58.967Z", Performer: &dlee, Comment: "Fine by me."},
		// Delegated: two assignees, decided by the delegate.
		{WorkItem: WorkItem{CaseOID: oidTwoStepCas, ID: "7", Assignee: "mkovac, jdoe", Stage: 1, Outcome: "approve"},
			Assignees: []ObjectRef{mkovac, jdoe}, CreatedAt: "2026-10-01T10:53:58.967Z", ClosedAt: "2026-10-01T10:57:47.983Z",
			Deadline: "2026-10-04T10:53:58.967Z", Performer: &jdoe, Comment: "Approved on behalf of the team."},
		{WorkItem: WorkItem{CaseOID: oidTwoStepCas, ID: "10", Assignee: "dlee", Stage: 2, Outcome: "approve"},
			Assignees: []ObjectRef{dlee}, CreatedAt: d.WorkItems[2].CreatedAt, ClosedAt: "2026-10-01T10:58:03.361Z",
			Performer: &dlee, Comment: "Role approver agrees."},
	}
	if !reflect.DeepEqual(d.WorkItems, want) {
		gotJSON, _ := json.MarshalIndent(d.WorkItems, "", " ")
		t.Errorf("work items:\n%s", gotJSON)
	}
	if d.WorkItems[2].CreatedAt == "" {
		t.Error("stage 2 work item has no createdAt")
	}
	// Each person is read once though named several times.
	if n := mp.readsOf("/users/" + oidDlee); n != 1 {
		t.Errorf("dlee read %d times, want 1", n)
	}
}

// The first approver reads the case back after deciding: their own item is
// closed, the other approver's is hidden, so the step's approverRef says who
// is next.
func TestGetCaseNextApproversAfterDecision(t *testing.T) {
	mp := &fakeMidpoint{
		self: string(fixture(t, "self_approver.json")),
		objects: map[string]string{
			"/cases/" + oidTwoStepCas: string(fixture(t, "case_get_after_decision.json")),
			"/users/" + oidMkovac:     userBody(oidMkovac, "mkovac", "Mia Kovac"),
		},
	}
	d, err := mp.client(t, Config{}).GetCase(WithSelfMemo(context.Background()), oidTwoStepCas)
	if err != nil {
		t.Fatalf("GetCase: %v", err)
	}
	want := []ObjectRef{{OID: oidMkovac, Type: "User", Name: "mkovac", DisplayName: "Mia Kovac"}}
	if !reflect.DeepEqual(d.NextApprovers, want) {
		t.Errorf("nextApprovers = %+v, want %+v", d.NextApprovers, want)
	}
	if d.Stage == nil || *d.Stage != twoSteps[0] {
		t.Errorf("stage = %+v, want step 1 of 2", d.Stage)
	}
	if len(d.WorkItems) != 1 || d.WorkItems[0].Comment != "Fine by me." {
		t.Errorf("work items = %+v, want the approver's own decided item", d.WorkItems)
	}
	// get_case does not return nextApprovers.
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), "extApprovers") {
		t.Errorf("CaseDetail JSON carries nextApprovers: %s", b)
	}
}
