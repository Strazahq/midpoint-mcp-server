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

const (
	selfJSON = `{"user":{"oid":"u-self","name":"selfuser"}}`

	// One open case: work item @id 1 is the caller's and open; @id 2 belongs to
	// someone else; @id 3 is the caller's but already completed; @id 4 is the
	// caller's but was closed without a decision (cancelled when another
	// approver decided the stage); @id 5 is open with two assignees, one of them
	// the caller (assigneeRef is multi-valued).
	caseBody = `{
		"@type":"http://midpoint.evolveum.com/xml/ns/public/common/common-3#CaseType",
		"oid":"case-1",
		"name":"Approving Superuser for Jane",
		"state":"open",
		"objectRef":{"oid":"u-jane","type":"c:UserType","targetName":"Jane Doe"},
		"targetRef":{"oid":"role-su","type":"c:RoleType","targetName":"Superuser"},
		"requestorRef":{"oid":"u-self","type":"c:UserType","targetName":"selfuser"},
		"workItem":[
			{"@id":1,"assigneeRef":{"oid":"u-self","type":"c:UserType","targetName":"selfuser"},"stageNumber":1},
			{"@id":2,"assigneeRef":{"oid":"u-other","type":"c:UserType","targetName":"Someone Else"},"stageNumber":1},
			{"@id":3,"assigneeRef":{"oid":"u-self","type":"c:UserType","targetName":"selfuser"},"stageNumber":1,
			 "output":{"outcome":"http://midpoint.evolveum.com/xml/ns/public/model/approval/outcome#approve","comment":"ok"},
			 "closeTimestamp":"2026-07-01T10:00:00.000Z"},
			{"@id":4,"assigneeRef":{"oid":"u-self","type":"c:UserType","targetName":"selfuser"},"stageNumber":1,
			 "closeTimestamp":"2026-07-01T10:00:00.000Z"},
			{"@id":5,"assigneeRef":[
				{"oid":"u-other","type":"c:UserType","targetName":"Someone Else"},
				{"oid":"u-self","type":"c:UserType","targetName":"selfuser"}],"stageNumber":2}
		]
	}`

	// The same request after midPoint closed it.
	closedCaseBody = `{
		"oid":"case-closed","name":"Approved already","state":"closed",
		"outcome":"http://midpoint.evolveum.com/xml/ns/public/model/approval/outcome#approve",
		"workItem":[{"@id":1,"assigneeRef":{"oid":"u-self","type":"c:UserType","targetName":"selfuser"},
			"output":{"outcome":"http://midpoint.evolveum.com/xml/ns/public/model/approval/outcome#approve"},
			"closeTimestamp":"2026-07-01T10:00:00.000Z"}]
	}`
)

func newCasesClient(t *testing.T) (*Client, *[]capturedRequest) {
	t.Helper()
	var reqs []capturedRequest
	serve := func(status int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			reqs = append(reqs, capturedRequest{r.Method, r.URL.Path, r.URL.RawQuery, string(b)})
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/rest/self", serve(200, selfJSON))
	mux.HandleFunc("POST /ws/rest/cases/search", serve(200, `{"object":[`+caseBody+`]}`))
	mux.HandleFunc("GET /ws/rest/cases/{oid}", serve(200, `{"case":`+caseBody+`}`))
	mux.HandleFunc("GET /ws/rest/cases/case-closed", serve(200, `{"case":`+closedCaseBody+`}`))
	mux.HandleFunc("POST /ws/rest/cases/{caseOid}/workItems/{wid}/complete", serve(204, ""))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"}), &reqs
}

func TestGetCase(t *testing.T) {
	c, reqs := newCasesClient(t)

	detail, err := c.GetCase(context.Background(), "case-1")
	if err != nil {
		t.Fatalf("GetCase: %v", err)
	}
	if detail.State != "open" || detail.Target != "Superuser" || detail.Requestor != "selfuser" || detail.Object != "Jane Doe" {
		t.Errorf("case summary = %+v", detail.CaseSummary)
	}
	if len(detail.WorkItems) != 5 {
		t.Fatalf("got %d work items, want 5 (GetCase lists all)", len(detail.WorkItems))
	}
	// The completed item's outcome URI should render short.
	if detail.WorkItems[2].Outcome != "approve" {
		t.Errorf("work item 3 outcome = %q, want approve", detail.WorkItems[2].Outcome)
	}
	// A multi-assignee work item names every assignee.
	if got := detail.WorkItems[4].Assignee; got != "Someone Else, selfuser" {
		t.Errorf("work item 5 assignee = %q, want both assignees", got)
	}
	if q := lastRequest(t, reqs).rawQuery; q != "options=resolveNames" {
		t.Errorf("query = %q, want options=resolveNames", q)
	}
}

func TestListMyRequests(t *testing.T) {
	c, reqs := newCasesClient(t)

	res, err := c.ListMyRequests(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListMyRequests: %v", err)
	}
	cases := res.Requests
	if len(cases) != 1 || cases[0].OID != "case-1" || cases[0].Target != "Superuser" {
		t.Fatalf("cases = %+v", cases)
	}
	// The answer must name whose requests these are.
	if res.Subject.Name != "selfuser" || res.Subject.OID != "u-self" || res.Subject.Mode != ModePersonal {
		t.Errorf("subject = %+v, want selfuser/u-self in personal mode", res.Subject)
	}
	// The search filter must scope to the authenticated requestor.
	var sr searchRequest
	if err := json.Unmarshal([]byte(lastRequest(t, reqs).body), &sr); err != nil {
		t.Fatalf("decoding search body: %v", err)
	}
	if sr.Query.Filter == nil || !strings.Contains(sr.Query.Filter.Text, `requestorRef matches (oid = "u-self")`) {
		t.Errorf("filter = %+v, want requestorRef scoped to self", sr.Query.Filter)
	}
}

func TestListWorkItems(t *testing.T) {
	c, reqs := newCasesClient(t)

	res, err := c.ListWorkItems(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListWorkItems: %v", err)
	}
	items := res.WorkItems
	// Work items @id 1 and @id 5 qualify: assigned to self (5 among others) and
	// still open. @id 4 is the caller's but closed without an output.
	if len(items) != 2 {
		t.Fatalf("got %d work items, want 2 (self + open only): %+v", len(items), items)
	}
	if res.Subject.Name != "selfuser" || res.Subject.Mode != ModePersonal {
		t.Errorf("subject = %+v, want selfuser in personal mode", res.Subject)
	}
	got := items[0]
	if got.ID != "1" || got.CaseOID != "case-1" || got.Target != "Superuser" || got.Requestor != "selfuser" {
		t.Errorf("work item = %+v", got)
	}
	if items[1].ID != "5" || items[1].Assignee != "selfuser" {
		t.Errorf("multi-assignee work item = %+v, want @id 5 listed under the caller", items[1])
	}

	var sr searchRequest
	if err := json.Unmarshal([]byte(lastRequest(t, reqs).body), &sr); err != nil {
		t.Fatalf("decoding search body: %v", err)
	}
	text := ""
	if sr.Query.Filter != nil {
		text = sr.Query.Filter.Text
	}
	if !strings.Contains(text, `state = "open"`) || !strings.Contains(text, `workItem/assigneeRef matches (oid = "u-self")`) {
		t.Errorf("filter = %q, want open + assignee scoped to self", text)
	}
}

func TestPlanRequestRole(t *testing.T) {
	c := NewClient(Config{})
	p, err := c.PlanRequestRole("u-jane", "role-su")
	if err != nil {
		t.Fatalf("PlanRequestRole: %v", err)
	}
	if p.Method != http.MethodPatch || p.Path != "/users/u-jane" {
		t.Errorf("method/path = %s %s", p.Method, p.Path)
	}
	assertJSONBody(t, p.Body, `{"objectModification":{"itemDelta":[{"modificationType":"add","path":"assignment","value":{"targetRef":{"oid":"role-su","type":"RoleType"}}}]}}`)
}

func TestPlanCompleteWorkItem(t *testing.T) {
	c := NewClient(Config{})

	approve, err := c.PlanCompleteWorkItem("case-1", "1", true, "looks good")
	if err != nil {
		t.Fatalf("PlanCompleteWorkItem approve: %v", err)
	}
	if approve.Method != http.MethodPost || approve.Path != "/cases/case-1/workItems/1/complete" {
		t.Errorf("method/path = %s %s", approve.Method, approve.Path)
	}
	assertJSONBody(t, approve.Body, `{"output":{"@type":"c:AbstractWorkItemOutputType","outcome":"`+outcomeApprove+`","comment":"looks good"}}`)

	reject, err := c.PlanCompleteWorkItem("case-1", "1", false, "")
	if err != nil {
		t.Fatalf("PlanCompleteWorkItem reject: %v", err)
	}
	assertJSONBody(t, reject.Body, `{"output":{"@type":"c:AbstractWorkItemOutputType","outcome":"`+outcomeReject+`"}}`)
}

func TestPlanCompleteWorkItemValidates(t *testing.T) {
	c := NewClient(Config{})
	if _, err := c.PlanCompleteWorkItem("", "1", true, ""); err == nil {
		t.Error("expected error for empty case oid")
	}
	if _, err := c.PlanCompleteWorkItem("case-1", "", true, ""); err == nil {
		t.Error("expected error for empty work item id")
	}
}

func TestFindRequestCase(t *testing.T) {
	c, reqs := newCasesClient(t)
	oid := c.FindRequestCase(context.Background(), "u-jane", "role-su")
	if oid != "case-1" {
		t.Errorf("FindRequestCase = %q, want case-1", oid)
	}
	var sr searchRequest
	_ = json.Unmarshal([]byte(lastRequest(t, reqs).body), &sr)
	if sr.Query.Filter == nil ||
		!strings.Contains(sr.Query.Filter.Text, `objectRef matches (oid = "u-jane")`) ||
		!strings.Contains(sr.Query.Filter.Text, `targetRef matches (oid = "role-su")`) {
		t.Errorf("filter = %+v", sr.Query.Filter)
	}
}

// CheckDecidable applies the inbox rule to one work item: only an open work
// item assigned to the caller, in an open case, may be decided.
func TestCheckDecidable(t *testing.T) {
	c, reqs := newCasesClient(t)
	ctx := context.Background()

	for _, id := range []string{"1", "5"} {
		d, err := c.CheckDecidable(ctx, "case-1", id)
		if err != nil {
			t.Fatalf("CheckDecidable(%s): %v", id, err)
		}
		if d.Subject.Name != "selfuser" || d.Subject.OID != "u-self" || d.Subject.Mode != ModePersonal {
			t.Errorf("%s: subject = %+v", id, d.Subject)
		}
		if d.WorkItem.ID != id || d.WorkItem.CaseOID != "case-1" || d.WorkItem.Assignee != "selfuser" {
			t.Errorf("%s: work item = %+v", id, d.WorkItem)
		}
		if d.Case.Name != "Approving Superuser for Jane" || d.Case.Target != "Superuser" || d.Case.Object != "Jane Doe" {
			t.Errorf("%s: case = %+v", id, d.Case)
		}
	}

	refusals := []struct {
		name, caseOID, id string
		want              []string
	}{
		{"someone else's", "case-1", "2", []string{"refused", "assigned to Someone Else", "not to selfuser", "list_work_items"}},
		{"completed", "case-1", "3", []string{"refused", "already closed with outcome approve"}},
		{"cancelled", "case-1", "4", []string{"refused", "already closed"}},
		{"unknown id", "case-1", "99", []string{"refused", "has no work item 99"}},
		{"closed case", "case-closed", "1", []string{"refused", "is closed, not open"}},
	}
	for _, r := range refusals {
		_, err := c.CheckDecidable(ctx, r.caseOID, r.id)
		if err == nil {
			t.Errorf("%s: CheckDecidable succeeded, want a refusal", r.name)
			continue
		}
		for _, w := range r.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: refusal %q does not mention %q", r.name, err, w)
			}
		}
	}

	// The check only reads: /self, then the case with names resolved.
	for _, r := range *reqs {
		if r.method != http.MethodGet {
			t.Errorf("CheckDecidable made a %s %s request; it must only read", r.method, r.path)
		}
	}
}

func TestCheckDecidableValidates(t *testing.T) {
	c, reqs := newCasesClient(t)
	if _, err := c.CheckDecidable(context.Background(), "", "1"); err == nil {
		t.Error("expected error for empty case oid")
	}
	if _, err := c.CheckDecidable(context.Background(), "case-1", " "); err == nil {
		t.Error("expected error for empty work item id")
	}
	if len(*reqs) != 0 {
		t.Errorf("invalid input reached midPoint: %+v", *reqs)
	}
}

// A shared technical account has no inbox of its own to decide from: the check
// refuses exactly as list_work_items does, before reading the case.
func TestCheckDecidableRefusesSharedCredential(t *testing.T) {
	var reads int
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/rest/", func(w http.ResponseWriter, _ *http.Request) {
		reads++
		_, _ = io.WriteString(w, selfJSON)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := Config{BaseURL: srv.URL, Username: "svc", Password: "p"}
	cfg.File.Identity.CredentialIsShared = true

	if _, err := NewClient(cfg).CheckDecidable(context.Background(), "case-1", "1"); !errors.Is(err, ErrNoCallerIdentity) {
		t.Errorf("error = %v, want ErrNoCallerIdentity", err)
	}
	if reads != 0 {
		t.Errorf("refused check made %d request(s) to midPoint", reads)
	}
}

// searchCaseBody is a POST /cases/search answer in the shape midPoint 4.10.3
// really returns: the nested ObjectListType envelope, and every reference
// with namespace-prefixed keys ("t:oid", "t:type", "t:relation"), unlike the
// plain keys of a single-object GET. Work item @id 1 is the caller's alone;
// @id 2 has two assignees, the caller among them; @id 3 is someone else's.
const searchCaseBody = `{"@ns":"http://prism.evolveum.com/xml/ns/public/types-3","object":{
	"@type":"http://midpoint.evolveum.com/xml/ns/public/common/api-types-3#ObjectListType",
	"object":[{
		"@type":"c:CaseType","oid":"case-9","name":"Approving Superuser for Jane","state":"open",
		"objectRef":{"t:oid":"u-jane","t:relation":"org:default","t:type":"c:UserType","targetName":"Jane Doe"},
		"targetRef":{"t:oid":"role-su","t:relation":"org:default","t:type":"c:RoleType","targetName":"Superuser"},
		"requestorRef":{"t:oid":"u-jane","t:relation":"org:default","t:type":"c:UserType","targetName":"Jane Doe"},
		"workItem":[
			{"@id":1,"stageNumber":1,
			 "assigneeRef":{"t:oid":"u-self","t:relation":"org:default","t:type":"c:UserType","targetName":"selfuser"}},
			{"@id":2,"stageNumber":1,"assigneeRef":[
				{"t:oid":"u-other","t:relation":"org:default","t:type":"c:UserType","targetName":"Someone Else"},
				{"t:oid":"u-self","t:relation":"org:default","t:type":"c:UserType","targetName":"selfuser"}]},
			{"@id":3,"stageNumber":1,
			 "assigneeRef":{"t:oid":"u-other","t:relation":"org:default","t:type":"c:UserType","targetName":"Someone Else"}}
		]
	}]
}}`

// The inbox rule itself, fed the real search answer: prefixed assignee keys
// must still identify the caller.
func TestInboxRuleReadsPrefixedAssigneeRefs(t *testing.T) {
	raws, err := parseObjectList([]byte(searchCaseBody))
	if err != nil || len(raws) != 1 {
		t.Fatalf("parseObjectList = %d objects, %v", len(raws), err)
	}
	var cj caseJSON
	if err := json.Unmarshal(raws[0], &cj); err != nil {
		t.Fatalf("decoding case: %v", err)
	}
	items := cj.items()
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	want := []struct {
		assignees int
		inInbox   bool
	}{{1, true}, {2, true}, {1, false}}
	for i, wi := range items {
		if got := len(wi.assignees()); got != want[i].assignees {
			t.Errorf("work item %s: %d assignee(s), want %d", wi.ID.s, got, want[i].assignees)
		}
		if got := wi.inInbox("u-self"); got != want[i].inInbox {
			t.Errorf("work item %s: inInbox = %v, want %v", wi.ID.s, got, want[i].inInbox)
		}
	}
	if s := cj.summary(); s.Object != "Jane Doe" || s.Target != "Superuser" || s.Requestor != "Jane Doe" {
		t.Errorf("case summary = %+v", s)
	}
}

// list_work_items end to end over the real search shape.
func TestListWorkItemsPrefixedSearchRefs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/rest/self", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, selfJSON)
	})
	mux.HandleFunc("POST /ws/rest/cases/search", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, searchCaseBody)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	res, err := c.ListWorkItems(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListWorkItems: %v", err)
	}
	if len(res.WorkItems) != 2 {
		t.Fatalf("got %d work items, want 2 (@id 1 and the two-assignee @id 2): %+v", len(res.WorkItems), res.WorkItems)
	}
	for i, id := range []string{"1", "2"} {
		got := res.WorkItems[i]
		if got.ID != id || got.CaseOID != "case-9" || got.Assignee != "selfuser" ||
			got.Target != "Superuser" || got.Object != "Jane Doe" || got.Requestor != "Jane Doe" {
			t.Errorf("work item %d = %+v", i, got)
		}
	}
}
