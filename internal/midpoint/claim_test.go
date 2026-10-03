package midpoint

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Fixtures (recorded on midPoint 4.10.3 through Switch-To-Principal, names
// and OIDs replaced with neutral ones):
//   - cases_search_offered.json: list_work_items' search as a member of the
//     org access-approvers, holding a read authorization with a
//     candidateAssignee clause. The approval step names the org; its one work
//     item has candidateRef = the org and no assignee.
//   - cases_search_claimed.json: the same search after the member claimed it:
//     assigneeRef = the member, candidateRef unchanged.
const (
	oidApprovers   = "30000000-0000-0000-0000-000000000002"
	oidOfferedCase = "40000000-0000-0000-0000-000000000004"
	// selfGroupMember is dlee, member of access-approvers.
	selfGroupMember = `{"user":{"oid":"` + oidDlee + `","name":"dlee","fullName":"Dana Lee","roleMembershipRef":[
		{"oid":"` + oidApprovers + `","relation":"org:default","type":"c:OrgType","targetName":"access-approvers"},
		{"oid":"00000000-0000-0000-0000-00000000000a","relation":"org:default","type":"c:RoleType","targetName":"Approver"}]}}`
	// selfOutsider is mkovac, in no group.
	selfOutsider = `{"user":{"oid":"` + oidMkovac + `","name":"mkovac","fullName":"Mia Kovac"}}`
)

// groupCase returns the recorded offered case as a map, with change applied.
func groupCase(t *testing.T, file string, change func(c, wi map[string]any)) map[string]any {
	t.Helper()
	raws, err := parseObjectList(fixture(t, file))
	if err != nil || len(raws) != 1 {
		t.Fatalf("%s: %d objects, %v", file, len(raws), err)
	}
	var c map[string]any
	if err := json.Unmarshal(raws[0], &c); err != nil {
		t.Fatal(err)
	}
	wi, _ := c["workItem"].(map[string]any)
	if change != nil {
		change(c, wi)
	}
	return c
}

// groupMidpoint serves one case to self, by search and by GET.
func groupMidpoint(t *testing.T, self string, c map[string]any) (*Client, *[]capturedRequest) {
	t.Helper()
	var reqs []capturedRequest
	body, _ := json.Marshal(c)
	mux := http.NewServeMux()
	reply := func(s string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			reqs = append(reqs, capturedRequest{r.Method, r.URL.Path, r.URL.RawQuery, string(b)})
			_, _ = io.WriteString(w, s)
		}
	}
	mux.HandleFunc("GET /ws/rest/self", reply(self))
	mux.HandleFunc("POST /ws/rest/cases/search", reply(`{"object":{"object":[`+string(body)+`]}}`))
	mux.HandleFunc("GET /ws/rest/cases/"+oidOfferedCase, reply(`{"case":`+string(body)+`}`))
	mux.HandleFunc("GET /ws/rest/orgs/"+oidApprovers, reply(`{"org":{"oid":"`+oidApprovers+`","name":"access-approvers","displayName":"Access approvers"}}`))
	mux.HandleFunc("GET /ws/rest/users/"+oidBstone, reply(`{"user":{"oid":"`+oidBstone+`","name":"bstone","fullName":"Bob Stone"}}`))
	mux.HandleFunc("GET /ws/rest/roles/"+oidFinance, reply(`{"role":{"oid":"`+oidFinance+`","name":"finance-reports","displayName":"Finance reports"}}`))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"}), &reqs
}

func TestInboxFilterOffersToGroups(t *testing.T) {
	var self userJSON
	if err := json.Unmarshal([]byte(selfGroupMember), &struct{ User *userJSON }{&self}); err != nil {
		t.Fatal(err)
	}
	got := inboxFilter(self.OID, candidatesOf(self))
	want := `state = "open" and (workItem/assigneeRef matches (oid = "` + oidDlee + `") or workItem matches (candidateRef matches ` +
		`(oid = ("` + oidDlee + `", "00000000-0000-0000-0000-00000000000a", "` + oidApprovers + `")) and assigneeRef not exists and closeTimestamp not exists))`
	if got != want {
		t.Errorf("filter =\n %s\nwant\n %s", got, want)
	}
}

func TestListWorkItemsOffered(t *testing.T) {
	c, reqs := groupMidpoint(t, selfGroupMember, groupCase(t, "cases_search_offered.json", nil))
	res, err := c.ListWorkItems(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListWorkItems: %v", err)
	}
	if len(res.WorkItems) != 1 {
		t.Fatalf("got %d work items, want the offered one", len(res.WorkItems))
	}
	wi := res.WorkItems[0]
	if !wi.Offered || wi.Claimed || wi.Assignee != "" || wi.ID != "5" || wi.CaseOID != oidOfferedCase {
		t.Errorf("work item = %+v, want offered, unclaimed, no assignee", wi)
	}
	if g := wi.OfferedTo; g == nil || g.OID != oidApprovers || g.Type != "Org" || g.DisplayName != "Access approvers" {
		t.Errorf("offeredTo = %+v", g)
	}
	if wi.Context.Reason != ReasonGroup || wi.Context.Change != ChangeAdd || wi.Context.Requestee.DisplayName != "Bob Stone" ||
		wi.Context.Target.DisplayName != "Finance reports" || wi.Context.Stage.Name != "Group step" {
		t.Errorf("context = %+v", wi.Context)
	}
	if len(wi.Context.CoAssignees) != 0 {
		t.Errorf("coAssignees = %+v, want none", wi.Context.CoAssignees)
	}
	var search string
	for _, r := range *reqs {
		if r.path == "/ws/rest/cases/search" {
			search = r.body
		}
	}
	if !strings.Contains(search, `candidateRef matches (oid = (`) || !strings.Contains(search, oidApprovers) {
		t.Errorf("search %s, want the caller's groups as candidates", search)
	}
}

func TestListWorkItemsClaimed(t *testing.T) {
	c, _ := groupMidpoint(t, selfGroupMember, groupCase(t, "cases_search_claimed.json", nil))
	res, err := c.ListWorkItems(context.Background(), 0)
	if err != nil || len(res.WorkItems) != 1 {
		t.Fatalf("ListWorkItems = %+v, %v", res.WorkItems, err)
	}
	wi := res.WorkItems[0]
	if wi.Offered || !wi.Claimed || wi.Assignee != "dlee" || wi.OfferedTo == nil || wi.OfferedTo.OID != oidApprovers {
		t.Errorf("work item = %+v, want claimed from access-approvers", wi)
	}
	if wi.Context.Reason != ReasonGroup {
		t.Errorf("reason = %q, want group", wi.Context.Reason)
	}
}

// Who isn't in the group, or can't read the item, gets nothing listed.
func TestListWorkItemsOfferedToOthers(t *testing.T) {
	c, _ := groupMidpoint(t, selfOutsider, groupCase(t, "cases_search_offered.json", nil))
	if res, err := c.ListWorkItems(context.Background(), 0); err != nil || len(res.WorkItems) != 0 {
		t.Errorf("outsider: %+v, %v; want nothing", res.WorkItems, err)
	}
	// Live on 4.10.3: the stock Approver role finds the case (it reads every
	// case's top-level items) but midPoint leaves the offered item out of it.
	hidden := groupCase(t, "cases_search_offered.json", func(c, _ map[string]any) { delete(c, "workItem") })
	c, _ = groupMidpoint(t, selfGroupMember, hidden)
	if res, err := c.ListWorkItems(context.Background(), 0); err != nil || len(res.WorkItems) != 0 {
		t.Errorf("hidden item: %+v, %v; want nothing", res.WorkItems, err)
	}
}

func TestCheckClaimable(t *testing.T) {
	ctx := context.Background()
	c, reqs := groupMidpoint(t, selfGroupMember, groupCase(t, "cases_search_offered.json", nil))
	g, err := c.CheckClaimable(ctx, oidOfferedCase, "5")
	if err != nil {
		t.Fatalf("CheckClaimable: %v", err)
	}
	if g.Subject.Name != "dlee" || g.WorkItem.ID != "5" || g.OfferedTo.DisplayName != "Access approvers" ||
		g.Object.Name != "bstone" || g.Target.Name != "finance-reports" {
		t.Errorf("claimable = %+v", g)
	}
	for _, r := range *reqs {
		if r.method != http.MethodGet {
			t.Errorf("CheckClaimable made a %s %s request; it must only read", r.method, r.path)
		}
	}

	other := func(_, wi map[string]any) {
		wi["assigneeRef"] = map[string]any{"oid": oidMkovac, "type": "c:UserType", "targetName": "mkovac"}
	}
	closed := func(_, wi map[string]any) { wi["closeTimestamp"] = "2026-10-03T18:20:00Z" }
	for _, r := range []struct {
		name, self, file string
		change           func(c, wi map[string]any)
		id, code         string
		want             []string
	}{
		{"claimed by me", selfGroupMember, "cases_search_claimed.json", nil, "5", CodeInvalidInput, []string{"already yours"}},
		{"claimed by another", selfGroupMember, "cases_search_offered.json", other, "5", CodeNotInInbox, []string{"is assigned to mkovac"}},
		{"not my group", selfOutsider, "cases_search_offered.json", nil, "5", CodeNotInInbox, []string{"is assigned to no one", "access-approvers"}},
		{"closed item", selfGroupMember, "cases_search_offered.json", closed, "5", CodeAlreadyDecided, []string{"is already closed"}},
		{"closed case", selfGroupMember, "cases_search_offered.json", func(c, _ map[string]any) { c["state"] = "closed" }, "5", CodeRequestClosed, []string{", not open,"}},
		{"item not shown", selfGroupMember, "cases_search_offered.json", func(c, _ map[string]any) { delete(c, "workItem") }, "5", CodeNotInInbox, []string{"has no work item 5"}},
	} {
		c, _ := groupMidpoint(t, r.self, groupCase(t, r.file, r.change))
		_, err := c.CheckClaimable(ctx, oidOfferedCase, r.id)
		if code, _ := ErrorCode(err); code != r.code {
			t.Errorf("%s: code %q (%v), want %q", r.name, code, err, r.code)
			continue
		}
		for _, w := range r.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: %q does not mention %q", r.name, err, w)
			}
		}
	}
}

func TestCheckReleasable(t *testing.T) {
	ctx := context.Background()
	c, _ := groupMidpoint(t, selfGroupMember, groupCase(t, "cases_search_claimed.json", nil))
	g, err := c.CheckReleasable(ctx, oidOfferedCase, "5")
	if err != nil {
		t.Fatalf("CheckReleasable: %v", err)
	}
	if g.WorkItem.Assignee != "dlee" || g.OfferedTo.OID != oidApprovers {
		t.Errorf("releasable = %+v", g)
	}

	direct := func(_, wi map[string]any) { delete(wi, "candidateRef") }
	two := func(_, wi map[string]any) {
		wi["assigneeRef"] = []any{wi["assigneeRef"], map[string]any{"oid": oidMkovac, "type": "c:UserType", "targetName": "mkovac"}}
	}
	for _, r := range []struct {
		name, self, file string
		change           func(c, wi map[string]any)
		code             string
		want             string
	}{
		{"not claimed", selfGroupMember, "cases_search_offered.json", nil, CodeNotClaimed, "nobody has claimed it"},
		{"someone else's", selfOutsider, "cases_search_claimed.json", nil, CodeNotInInbox, "is assigned to dlee"},
		{"two holders", selfGroupMember, "cases_search_claimed.json", two, CodeNotInInbox, "is assigned to dlee, mkovac"},
		// Live on 4.10.3 midPoint answers this release with 204 and changes nothing.
		{"assigned directly", selfGroupMember, "cases_search_claimed.json", direct, CodeInvalidInput, "no group to release it to"},
	} {
		c, _ := groupMidpoint(t, r.self, groupCase(t, r.file, r.change))
		_, err := c.CheckReleasable(ctx, oidOfferedCase, "5")
		if code, _ := ErrorCode(err); code != r.code || !strings.Contains(err.Error(), r.want) {
			t.Errorf("%s: %v (code %q), want %q mentioning %q", r.name, err, code, r.code, r.want)
		}
	}
}

// An offered item is claimed before it is decided; a claimed one is decided
// like any other.
func TestCheckDecidableOffered(t *testing.T) {
	ctx := context.Background()
	c, _ := groupMidpoint(t, selfGroupMember, groupCase(t, "cases_search_offered.json", nil))
	_, err := c.CheckDecidable(ctx, oidOfferedCase, "5")
	if code, _ := ErrorCode(err); code != CodeNotClaimed || !strings.Contains(err.Error(), "claim it first (claim_work_item)") {
		t.Errorf("offered: %v (code %q), want not-claimed", err, code)
	}
	c, _ = groupMidpoint(t, selfGroupMember, groupCase(t, "cases_search_claimed.json", nil))
	if d, err := c.CheckDecidable(ctx, oidOfferedCase, "5"); err != nil || d.WorkItem.Assignee != "dlee" {
		t.Errorf("claimed: %+v, %v", d, err)
	}
	// Offered to a group the caller isn't in: not theirs, as before.
	c, _ = groupMidpoint(t, selfOutsider, groupCase(t, "cases_search_offered.json", nil))
	if _, err := c.CheckDecidable(ctx, oidOfferedCase, "5"); func() string { s, _ := ErrorCode(err); return s }() != CodeNotInInbox {
		t.Errorf("outsider: %v, want not-in-inbox", err)
	}
}

func TestPlanClaimAndRelease(t *testing.T) {
	c := NewClient(Config{})
	for _, tc := range []struct {
		plan func(string, string) (Plan, error)
		path string
	}{
		{c.PlanClaimWorkItem, "/cases/case-1/workItems/5/claim"},
		{c.PlanReleaseWorkItem, "/cases/case-1/workItems/5/release"},
	} {
		p, err := tc.plan("case-1", "5")
		if err != nil || p.Method != http.MethodPost || p.Path != tc.path || p.Body != nil {
			t.Errorf("plan = %+v, %v; want POST %s without a body", p, err, tc.path)
		}
		if _, err := tc.plan("", "5"); err == nil {
			t.Error("empty case oid accepted")
		}
		if _, err := tc.plan("case-1", " "); err == nil {
			t.Error("empty work item id accepted")
		}
	}
}

// get_case names an item's groups, and an item nobody claimed waits for them.
func TestGetCaseOffered(t *testing.T) {
	c, _ := groupMidpoint(t, selfGroupMember, groupCase(t, "cases_search_offered.json", nil))
	d, err := c.GetCase(context.Background(), oidOfferedCase)
	if err != nil || len(d.WorkItems) != 1 {
		t.Fatalf("GetCase = %+v, %v", d, err)
	}
	wi := d.WorkItems[0]
	if len(wi.Assignees) != 0 || len(wi.OfferedTo) != 1 || wi.OfferedTo[0].DisplayName != "Access approvers" {
		t.Errorf("work item = %+v, want no assignees, offered to Access approvers", wi)
	}
	if len(d.NextApprovers) != 1 || d.NextApprovers[0].OID != oidApprovers {
		t.Errorf("nextApprovers = %+v, want the group", d.NextApprovers)
	}
}

func TestReadWorkItemHolders(t *testing.T) {
	ctx := context.Background()
	c, _ := groupMidpoint(t, selfGroupMember, groupCase(t, "cases_search_claimed.json", nil))
	h, err := c.ReadWorkItemHolders(ctx, oidOfferedCase, "5")
	if err != nil || !h.Visible || !h.Open || !h.Mine || len(h.Assignees) != 1 || h.Assignees[0].Name != "dlee" {
		t.Errorf("claimed: %+v, %v", h, err)
	}
	if h, err := c.ReadWorkItemHolders(ctx, oidOfferedCase, "9"); err != nil || h.Visible {
		t.Errorf("unknown item: %+v, %v; want not visible", h, err)
	}
}
