package main

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

// OIDs of the recorded offered case (internal/midpoint/testdata/
// cases_search_offered.json).
const (
	fxApprovers   = "30000000-0000-0000-0000-000000000002" // the org the step names
	fxCaseOffered = "40000000-0000-0000-0000-000000000004" // finance-reports for bstone, offered to it
)

// groupSelf is dlee as a member of access-approvers.
const groupSelf = `{"user":{"oid":"` + fxDlee + `","name":"dlee","fullName":"Dana Lee","roleMembershipRef":
	{"oid":"` + fxApprovers + `","relation":"org:default","type":"c:OrgType","targetName":"access-approvers"}}}`

// claimMidpoint serves the recorded offered case and keeps who holds its work
// item: a claim makes the caller its holder, a release clears it, as midPoint
// does. claimStatus, when set, answers the claim instead, holding the item
// for raceHolder (someone who claimed it first).
type claimMidpoint struct {
	t           *testing.T
	base        map[string]any // the recorded case
	mu          sync.Mutex
	holder      string // OID of the work item's assignee; "" when nobody holds it
	claimStatus int
	raceHolder  string
	reqs        []recordedReq
}

func newClaimMidpoint(t *testing.T, holder string) *claimMidpoint {
	t.Helper()
	var c struct {
		Case map[string]any `json:"case"`
	}
	body := caseFromSearch(t, testdataFile(t, "cases_search_offered.json"), fxCaseOffered)
	if err := json.Unmarshal([]byte(body), &c); err != nil || c.Case == nil {
		t.Fatalf("cases_search_offered.json: %v", err)
	}
	return &claimMidpoint{t: t, base: c.Case, holder: holder}
}

// caseBody is the case with the current holder.
func (m *claimMidpoint) caseBody() string {
	c := maps.Clone(m.base)
	wi := maps.Clone(c["workItem"].(map[string]any))
	if m.holder != "" {
		wi["assigneeRef"] = map[string]any{"oid": m.holder, "type": "c:UserType", "targetName": map[string]string{fxDlee: "dlee", fxMkovac: "mkovac"}[m.holder]}
	}
	c["workItem"] = wi
	b, _ := json.Marshal(c)
	return string(b)
}

func (m *claimMidpoint) start() *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		defer m.mu.Unlock()
		m.reqs = append(m.reqs, recordedReq{r.Method, r.URL.Path, string(b)})
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/ws/rest")
		item := "/cases/" + fxCaseOffered + "/workItems/5/"
		switch {
		case r.Method == http.MethodGet && path == "/self":
			_, _ = io.WriteString(w, groupSelf)
		case r.Method == http.MethodPost && path == "/cases/search":
			_, _ = io.WriteString(w, `{"object":{"object":[`+m.caseBody()+`]}}`)
		case r.Method == http.MethodGet && path == "/cases/"+fxCaseOffered:
			_, _ = io.WriteString(w, `{"case":`+m.caseBody()+`}`)
		case r.Method == http.MethodPost && path == item+"claim":
			if m.claimStatus != 0 {
				m.holder = m.raceHolder
				w.WriteHeader(m.claimStatus)
				return
			}
			m.holder = fxDlee
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && path == item+"complete":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && path == item+"release":
			m.holder = ""
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && path == "/orgs/"+fxApprovers:
			_, _ = io.WriteString(w, `{"org":{"oid":"`+fxApprovers+`","name":"access-approvers","displayName":"Access approvers"}}`)
		case r.Method == http.MethodGet && path == "/users/"+fxBstone:
			_, _ = io.WriteString(w, fixtureUser(fxBstone, "bstone", "Bob Stone"))
		case r.Method == http.MethodGet && path == "/roles/"+fxFinance:
			_, _ = io.WriteString(w, `{"role":{"oid":"`+fxFinance+`","name":"finance-reports","displayName":"Finance reports"}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	m.t.Cleanup(srv.Close)
	return srv
}

// writes are the requests that change something.
func (m *claimMidpoint) writes() []recordedReq {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []recordedReq
	for _, r := range m.reqs {
		if r.method != http.MethodGet && !strings.HasSuffix(r.path, "/search") {
			out = append(out, r)
		}
	}
	return out
}

// connectClaims connects a client to the claim and request tools, with stable
// error codes, as the server main assembles does.
func connectClaims(t *testing.T, m *claimMidpoint, allowWrites bool) *mcp.ClientSession {
	t.Helper()
	cfg := midpoint.Config{BaseURL: m.start().URL, Username: "u", Password: "p", AllowWrites: allowWrites}
	client := midpoint.NewClient(cfg)
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "t"}, nil)
	registerRequestTools(server, client, allowWrites, newServerInfo(cfg))
	registerClaimTools(server, client, allowWrites, newServerInfo(cfg))
	server.AddReceivingMiddleware(errorCodes)
	return connectSession(t, server, false)
}

func groupArgs(over map[string]any) map[string]any {
	args := map[string]any{"caseOid": fxCaseOffered, "workItemId": "5", "userName": "bstone", "roleName": "finance-reports"}
	maps.Copy(args, over)
	return args
}

func TestClaimWorkItem(t *testing.T) {
	m := newClaimMidpoint(t, "")
	cs := connectClaims(t, m, true)

	out, text := callToolText(t, cs, "claim_work_item", groupArgs(nil))
	w := m.writes()
	if len(w) != 1 || w[0].method != http.MethodPost || w[0].path != "/ws/rest/cases/"+fxCaseOffered+"/workItems/5/claim" || w[0].body != "" {
		t.Fatalf("writes = %+v, want one bodyless POST .../claim", w)
	}
	if out["tool"] != "claim_work_item" || out["applied"] != true || out["confirmed"] != true || out["workItemId"] != "5" {
		t.Errorf("structured = %v", out)
	}
	if g, _ := out["offeredTo"].(map[string]any); g["displayName"] != "Access approvers" || g["type"] != "Org" {
		t.Errorf("offeredTo = %v", out["offeredTo"])
	}
	for _, s := range []string{"Claimed Finance reports for Bob Stone (bstone) as dlee", "taken from Access approvers", "decide_work_item", "work item 5"} {
		if !strings.Contains(text, s) {
			t.Errorf("text %q lacks %q", text, s)
		}
	}

	// Now it's dlee's: list_work_items shows it claimed, and decide_work_item takes it.
	list, listText := callToolText(t, cs, "list_work_items", map[string]any{})
	items, _ := list["workItems"].([]any)
	if len(items) != 1 {
		t.Fatalf("workItems = %v", list["workItems"])
	}
	if wi := items[0].(map[string]any); wi["claimed"] != true || wi["offered"] != false {
		t.Errorf("listed after claim = %v", wi)
	}
	if !strings.Contains(listText, " claimed=true offeredTo=access-approvers") {
		t.Errorf("list text %q, want claimed=true offeredTo=access-approvers", listText)
	}
	if _, text := callToolText(t, cs, "decide_work_item", groupArgs(map[string]any{"decision": "approve"})); !strings.Contains(text, "approve") {
		t.Errorf("decide after claim: %q", text)
	}
}

func TestReleaseWorkItem(t *testing.T) {
	m := newClaimMidpoint(t, fxDlee)
	cs := connectClaims(t, m, true)

	out, text := callToolText(t, cs, "release_work_item", groupArgs(nil))
	if w := m.writes(); len(w) != 1 || w[0].path != "/ws/rest/cases/"+fxCaseOffered+"/workItems/5/release" || w[0].body != "" {
		t.Fatalf("writes = %+v, want one bodyless POST .../release", w)
	}
	if out["tool"] != "release_work_item" || out["applied"] != true || out["confirmed"] != true {
		t.Errorf("structured = %v", out)
	}
	if !strings.HasPrefix(text, "Released Finance reports for Bob Stone (bstone) as dlee") || !strings.Contains(text, "back with Access approvers") || !strings.Contains(text, "work item 5") {
		t.Errorf("text = %q", text)
	}
	// Offered again.
	list, listText := callToolText(t, cs, "list_work_items", map[string]any{})
	if wi := list["workItems"].([]any)[0].(map[string]any); wi["offered"] != true || wi["claimed"] != false {
		t.Errorf("listed after release = %v", wi)
	}
	if !strings.Contains(listText, " offered=true offeredTo=access-approvers") {
		t.Errorf("list text %q", listText)
	}
}

// With writes off, both preview; the checks still run first.
func TestClaimAndReleaseGateOff(t *testing.T) {
	for _, tc := range []struct {
		tool, holder, verb string
	}{{"claim_work_item", "", "claim"}, {"release_work_item", fxDlee, "release"}} {
		m := newClaimMidpoint(t, tc.holder)
		cs := connectClaims(t, m, false)
		out, text := callToolText(t, cs, tc.tool, groupArgs(nil))
		if len(m.writes()) != 0 {
			t.Errorf("%s: wrote with the gate off: %+v", tc.tool, m.writes())
		}
		if out["dryRun"] != true || out["applied"] != false || !strings.HasPrefix(text, "DRY RUN") || !strings.Contains(text, "Would "+tc.verb) {
			t.Errorf("%s: out %v text %q", tc.tool, out, text)
		}
	}
	// A preview of a claim nobody may make is refused like the claim.
	cs := connectClaims(t, newClaimMidpoint(t, fxMkovac), false)
	if text := callToolErr(t, cs, "claim_work_item", groupArgs(nil)); !strings.Contains(text, "is assigned to mkovac") {
		t.Errorf("dry run of a held item: %q", text)
	}
}

func TestClaimRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, tool, holder string
		args               map[string]any
		code, text         string
	}{
		{"held by another", "claim_work_item", fxMkovac, nil, midpoint.CodeNotInInbox, "is assigned to mkovac"},
		{"already mine", "claim_work_item", fxDlee, nil, midpoint.CodeInvalidInput, "already yours"},
		{"wrong role name", "claim_work_item", "", map[string]any{"roleName": "db-admin"}, midpoint.CodeInvalidInput, `roleName "db-admin" doesn't match`},
		{"wrong user name", "claim_work_item", "", map[string]any{"userName": "jdoe"}, midpoint.CodeInvalidInput, `userName "jdoe" doesn't match`},
		{"release unclaimed", "release_work_item", "", nil, midpoint.CodeNotClaimed, "nobody has claimed it"},
		{"release another's", "release_work_item", fxMkovac, nil, midpoint.CodeNotInInbox, "is assigned to mkovac"},
		{"decide unclaimed", "decide_work_item", "", map[string]any{"decision": "approve"}, midpoint.CodeNotClaimed, "claim it first (claim_work_item)"},
		{"no such item", "claim_work_item", "", map[string]any{"workItemId": "9"}, midpoint.CodeNotInInbox, "has no work item 9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newClaimMidpoint(t, tc.holder)
			cs := connectClaims(t, m, true)
			text, payload := callToolCode(t, cs, tc.tool, groupArgs(tc.args))
			if payload["code"] != tc.code || !strings.Contains(text, tc.text) {
				t.Errorf("code %v text %q, want %s mentioning %q", payload["code"], text, tc.code, tc.text)
			}
			if w := m.writes(); len(w) != 0 {
				t.Errorf("refused call wrote: %+v", w)
			}
		})
	}
}

// Someone claims the item between the check and the claim: midPoint answers
// 500 (live on 4.10.3), and the read-back turns that into not-in-inbox.
func TestClaimRace(t *testing.T) {
	m := newClaimMidpoint(t, "")
	m.claimStatus, m.raceHolder = http.StatusInternalServerError, fxMkovac
	cs := connectClaims(t, m, true)
	text, payload := callToolCode(t, cs, "claim_work_item", groupArgs(nil))
	if payload["code"] != midpoint.CodeNotInInbox || !strings.Contains(text, "is assigned to mkovac now; someone else claimed it first") {
		t.Errorf("code %v text %q", payload["code"], text)
	}
	if got := fallbackCode("claim_work_item", text); got != midpoint.CodeNotInInbox {
		t.Errorf("text falls back to %q", got)
	}
}

// The claim tools carry the view fields, so the inbox can call them.
func TestClaimToolsViewFields(t *testing.T) {
	cs := connectClaims(t, newClaimMidpoint(t, ""), true)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name != "claim_work_item" && tool.Name != "release_work_item" {
			continue
		}
		b, _ := json.Marshal(tool.OutputSchema)
		for _, f := range []string{`"tool"`, `"acting"`, `"server"`, `"confirmed"`, `"offeredTo"`} {
			if !strings.Contains(string(b), f) {
				t.Errorf("%s: output schema lacks %s", tool.Name, f)
			}
		}
		b, _ = json.Marshal(tool.InputSchema)
		for _, f := range []string{"caseOid", "workItemId", "userName", "roleName"} {
			if !strings.Contains(string(b), `"`+f+`"`) {
				t.Errorf("%s: input schema lacks %s", tool.Name, f)
			}
		}
	}
}
