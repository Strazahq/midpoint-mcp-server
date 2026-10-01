package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

// Fixtures for the view harness in test/views: real tool results, produced by
// this server's own code in a UI session against a fake midPoint that serves
// the answers recorded on midPoint 4.10.3 (internal/midpoint/testdata). The
// harness feeds them to a view as a host would.
//
// Skipped unless MIDPOINT_MCP_WRITE_VIEW_FIXTURES=1, so `go test ./...` never
// rewrites them. Regenerate after a change to what these tools return:
//
//	MIDPOINT_MCP_WRITE_VIEW_FIXTURES=1 go test -run TestWriteViewFixtures .
//
// Each file is {"call": {name, arguments}, "about": ..., "result": <the full
// CallToolResult>}. The generator owns the inbox fixtures: a fixture it no longer
// produces is removed; other suites retain their own fixtures.

const (
	envWriteViewFixtures = "MIDPOINT_MCP_WRITE_VIEW_FIXTURES"
	viewFixtureDir       = "test/views/fixtures"
	// fixtureJustificationItem is the justification item the recordings carry.
	fixtureJustificationItem = "{http://example.com/xml/ns/access-request}justification"
)

// OIDs of the recorded answers (neutral; see internal/midpoint/enrich_test.go).
const (
	fxDlee         = "10000000-0000-0000-0000-0000000000a1" // approver of db-admin
	fxMkovac       = "10000000-0000-0000-0000-0000000000a2" // second approver
	fxBstone       = "10000000-0000-0000-0000-0000000000b1" // requestee and requester
	fxJdoe         = "10000000-0000-0000-0000-0000000000c1" // manager of dev-ops, delegate
	fxShared       = "10000000-0000-0000-0000-0000000000d1" // a shared service account
	fxDbAdmin      = "20000000-0000-0000-0000-0000000000f1"
	fxFinance      = "20000000-0000-0000-0000-0000000000e1"
	fxDevOps       = "30000000-0000-0000-0000-000000000001"
	fxCaseTwoStep  = "40000000-0000-0000-0000-000000000001" // db-admin for bstone, two steps
	fxCaseManagers = "40000000-0000-0000-0000-000000000002" // finance-reports, the requestee's managers
	fxCaseRemoval  = "40000000-0000-0000-0000-000000000003" // removing db-admin from bstone
)

// fixtureMidpoint is a fake midPoint for one persona. A case answers its
// "before" body until a work item of it is completed, then its "after" body,
// as midPoint does when a decision is read back.
type fixtureMidpoint struct {
	self   string            // GET /self, also GET /users/{self oid}
	search string            // POST /cases/search
	cases  map[string]string // GET /cases/{oid} before a completion
	after  map[string]string // GET /cases/{oid} after one
	users  map[string]string // GET /users/{oid}
	roles  map[string]string // GET /roles/{oid}
	// readStatus answers user and role reads not in the maps (default 404).
	readStatus int
	// fail answers "METHOD /ws/rest/path" with a status before anything else.
	fail map[string]int

	mu        sync.Mutex
	completed map[string]bool
}

func (f *fixtureMidpoint) start(t *testing.T) string {
	t.Helper()
	f.completed = map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (f *fixtureMidpoint) serve(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	w.Header().Set("Content-Type", "application/json")
	if st, ok := f.fail[r.Method+" "+r.URL.Path]; ok {
		w.WriteHeader(st)
		return
	}
	reply := func(body string, ok bool, otherwise int) {
		if !ok {
			w.WriteHeader(otherwise)
			return
		}
		_, _ = io.WriteString(w, body)
	}
	readStatus := f.readStatus
	if readStatus == 0 {
		readStatus = http.StatusNotFound
	}
	path := strings.TrimPrefix(r.URL.Path, "/ws/rest")
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && path == "/self":
		reply(f.self, true, 0)
	case r.Method == http.MethodPost && path == "/cases/search":
		reply(f.search, true, 0)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/complete"):
		oid := strings.Split(strings.TrimPrefix(path, "/cases/"), "/")[0]
		f.completed[oid] = true
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/cases/"):
		oid := strings.TrimPrefix(path, "/cases/")
		if body, ok := f.after[oid]; ok && f.completed[oid] {
			reply(body, true, 0)
			return
		}
		body, ok := f.cases[oid]
		reply(body, ok, http.StatusNotFound)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/users/"):
		oid := strings.TrimPrefix(path, "/users/")
		if body, ok := f.users[oid]; ok {
			reply(body, true, 0)
			return
		}
		reply(f.self, strings.Contains(f.self, `"`+oid+`"`) && oid != "", readStatus)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/roles/"):
		body, ok := f.roles[strings.TrimPrefix(path, "/roles/")]
		reply(body, ok, readStatus)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// fixtureUser is a minimal user answer, as the client package's tests use for
// people the recordings name but did not record.
func fixtureUser(oid, name, fullName string) string {
	b, _ := json.Marshal(map[string]any{"user": map[string]any{"oid": oid, "name": name, "fullName": fullName}})
	return string(b)
}

// caseFromSearch returns one case of a recorded search answer as the answer
// to GET /cases/{oid}: the same case object, wrapped as midPoint wraps a
// single object.
func caseFromSearch(t *testing.T, search, oid string) string {
	t.Helper()
	var s struct {
		Object struct {
			Object json.RawMessage `json:"object"`
		} `json:"object"`
	}
	if err := json.Unmarshal([]byte(search), &s); err != nil {
		t.Fatalf("search answer: %v", err)
	}
	var list []map[string]any
	if err := json.Unmarshal(s.Object.Object, &list); err != nil {
		var one map[string]any
		if err := json.Unmarshal(s.Object.Object, &one); err != nil {
			t.Fatalf("search objects: %v", err)
		}
		list = []map[string]any{one}
	}
	for _, c := range list {
		if c["oid"] == oid {
			b, _ := json.Marshal(map[string]any{"case": c})
			return string(b)
		}
	}
	t.Fatalf("case %s not in the search answer", oid)
	return ""
}

// mutateCase changes a recorded case answer ({"case": ...}). Every use names
// what it changes and why the recordings don't have it.
func mutateCase(t *testing.T, body string, change func(c map[string]any)) string {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("case answer: %v", err)
	}
	c, _ := v["case"].(map[string]any)
	if c == nil {
		t.Fatal("case answer without a case")
	}
	change(c)
	b, _ := json.Marshal(v)
	return string(b)
}

// caseWorkItems returns a case's work items, a single one being a bare object.
func caseWorkItems(c map[string]any) []map[string]any {
	switch wi := c["workItem"].(type) {
	case []any:
		var out []map[string]any
		for _, w := range wi {
			if m, ok := w.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	case map[string]any:
		return []map[string]any{wi}
	}
	return nil
}

const fixtureOutcomeNS = "http://midpoint.evolveum.com/xml/ns/public/model/approval/outcome#"

// --- personas ---

// approverPersona is dlee, a stock approver (End user and Approver roles)
// who approves requests for db-admin: one work item, step 1 of 2.
func approverPersona(t *testing.T) *fixtureMidpoint {
	search := testdataFile(t, "cases_search_approver.json")
	return &fixtureMidpoint{
		self:   testdataFile(t, "self_approver.json"),
		search: search,
		cases:  map[string]string{fxCaseTwoStep: caseFromSearch(t, search, fxCaseTwoStep)},
		users: map[string]string{
			fxBstone: testdataFile(t, "user_requestee.json"),
			fxMkovac: fixtureUser(fxMkovac, "mkovac", "Mia Kovac"),
		},
		roles: map[string]string{fxDbAdmin: testdataFile(t, "role_requested.json")},
	}
}

// managerPersona is jdoe, manager of dev-ops and delegate of mkovac: two work
// items, one delegated (two assignees), one for the requestee's managers.
func managerPersona(t *testing.T) *fixtureMidpoint {
	search := testdataFile(t, "cases_search_delegate.json")
	return &fixtureMidpoint{
		self: `{"user":{"oid":"` + fxJdoe + `","name":"jdoe","fullName":"Jane Doe",
			"parentOrgRef":{"oid":"` + fxDevOps + `","relation":"org:manager","type":"c:OrgType","targetName":"dev-ops"}}}`,
		search: search,
		cases: map[string]string{
			fxCaseTwoStep:  caseFromSearch(t, search, fxCaseTwoStep),
			fxCaseManagers: caseFromSearch(t, search, fxCaseManagers),
		},
		users: map[string]string{
			fxBstone: testdataFile(t, "user_requestee.json"),
			fxDlee:   fixtureUser(fxDlee, "dlee", "Dana Lee"),
			fxMkovac: fixtureUser(fxMkovac, "mkovac", "Mia Kovac"),
		},
		roles: map[string]string{
			fxDbAdmin: testdataFile(t, "role_requested.json"),
			fxFinance: `{"role":{"oid":"` + fxFinance + `","name":"finance-reports","displayName":"Finance reports"}}`,
		},
	}
}

// removalPersona is mkovac, asked to approve removing db-admin from bstone.
// The requester, midPoint's administrator, is deliberately not served: it
// shows a requester the approver can't read.
func removalPersona(t *testing.T) *fixtureMidpoint {
	search := testdataFile(t, "cases_search_removal.json")
	return &fixtureMidpoint{
		self:   fixtureUser(fxMkovac, "mkovac", "Mia Kovac"),
		search: search,
		cases:  map[string]string{fxCaseRemoval: caseFromSearch(t, search, fxCaseRemoval)},
		users:  map[string]string{fxBstone: testdataFile(t, "user_requestee.json")},
		roles:  map[string]string{fxDbAdmin: testdataFile(t, "role_requested.json")},
	}
}

// sharedPersona is a service account the deployment declared shared
// (identity.credentialIsShared), seen in personal mode.
func sharedPersona(t *testing.T) *fixtureMidpoint {
	m := approverPersona(t)
	m.self = fixtureUser(fxShared, "svc-access", "Access service")
	return m
}

// --- sessions ---

// fixtureSession is how the server is configured and who calls it.
type fixtureSession struct {
	// principal runs every call as this user (resource-server mode, as the
	// OIDC middleware does); empty is personal mode.
	principal   string
	writes      bool // MIDPOINT_MCP_ALLOW_WRITES
	reasonField bool // requests.justificationItem set
	shared      bool // identity.credentialIsShared
}

// connect starts mp and connects a UI session (the client advertises the MCP
// Apps extension) to the server main assembles.
func (s fixtureSession) connect(t *testing.T, mp *fixtureMidpoint) *mcp.ClientSession {
	t.Helper()
	cfg := midpoint.Config{BaseURL: mp.start(t), Username: "u", Password: "p", AllowWrites: s.writes}
	if s.reasonField {
		cfg.File.Requests.JustificationItem = fixtureJustificationItem
	}
	cfg.File.Identity.CredentialIsShared = s.shared
	server := newMCPServerWithViews(midpoint.NewClient(cfg), cfg, testViews())
	if s.principal != "" {
		oid := s.principal
		server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
			return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
				return next(midpoint.WithPrincipal(ctx, oid), method, req)
			}
		})
	}
	return connectSession(t, server, true)
}

// viewFixture is one recorded call.
type viewFixture struct {
	name    string // file name without .json
	about   string
	mp      func(t *testing.T) *fixtureMidpoint
	session fixtureSession
	tool    string
	args    map[string]any
	isError bool
}

func viewFixtures() []viewFixture {
	rs := func(oid string) fixtureSession {
		return fixtureSession{principal: oid, writes: true, reasonField: true}
	}
	decide := func(caseOID, id, decision string, comment ...string) map[string]any {
		args := map[string]any{"caseOid": caseOID, "workItemId": id, "decision": decision}
		if len(comment) > 0 {
			args["comment"] = comment[0]
		}
		return args
	}
	withAfter := func(persona func(*testing.T) *fixtureMidpoint, caseOID string, after func(t *testing.T, m *fixtureMidpoint) string) func(*testing.T) *fixtureMidpoint {
		return func(t *testing.T) *fixtureMidpoint {
			m := persona(t)
			m.after = map[string]string{caseOID: after(t, m)}
			return m
		}
	}
	withCase := func(persona func(*testing.T) *fixtureMidpoint, caseOID, file string) func(*testing.T) *fixtureMidpoint {
		return func(t *testing.T) *fixtureMidpoint {
			m := persona(t)
			m.cases[caseOID] = testdataFile(t, file)
			return m
		}
	}
	withFail := func(persona func(*testing.T) *fixtureMidpoint, call string, status int) func(*testing.T) *fixtureMidpoint {
		return func(t *testing.T) *fixtureMidpoint {
			m := persona(t)
			m.fail = map[string]int{call: status}
			return m
		}
	}
	afterFile := func(file string) func(*testing.T, *fixtureMidpoint) string {
		return func(t *testing.T, _ *fixtureMidpoint) string { return testdataFile(t, file) }
	}
	empty := func(persona func(*testing.T) *fixtureMidpoint) func(*testing.T) *fixtureMidpoint {
		return func(t *testing.T) *fixtureMidpoint {
			m := persona(t)
			m.search = `{"object":{"object":[]}}`
			return m
		}
	}
	unreadable := func(t *testing.T) *fixtureMidpoint {
		m := approverPersona(t)
		m.users, m.roles, m.readStatus = nil, nil, http.StatusForbidden
		return m
	}
	personal := fixtureSession{writes: true, reasonField: true}
	dryRun := fixtureSession{principal: fxDlee, reasonField: true}
	noReasonField := fixtureSession{principal: fxDlee, writes: true}
	shared := fixtureSession{writes: true, reasonField: true, shared: true}
	completePath := "POST /ws/rest/cases/" + fxCaseTwoStep + "/workItems/6/complete"

	return []viewFixture{
		// --- list_work_items ---
		{name: "inbox.approver", tool: "list_work_items", args: map[string]any{}, mp: approverPersona, session: rs(fxDlee),
			about: "dlee, a stock approver of db-admin, resource-server mode: one item, step 1 of 2 (all must agree, Mia Kovac also asked), risk high, deadline, future start, a reason; requester = requestee"},
		{name: "inbox.approver-personal", tool: "list_work_items", args: map[string]any{}, mp: approverPersona, session: personal,
			about: "the same inbox in personal mode: midPoint sees the server's own account"},
		{name: "inbox.approver-no-reason-field", tool: "list_work_items", args: map[string]any{}, mp: approverPersona, session: noReasonField,
			about: "the same inbox without requests.justificationItem: server.requestReason false, no justification"},
		{name: "inbox.approver-dry-run", tool: "list_work_items", args: map[string]any{}, mp: approverPersona, session: dryRun,
			about: "the same inbox with writes disabled: server.writesEnabled false"},
		{name: "inbox.approver-unreadable", tool: "list_work_items", args: map[string]any{}, mp: unreadable, session: rs(fxDlee),
			about: "every user and role read refused (403): requestee, requester and role readable false, requestee's access not visible"},
		{name: "inbox.manager", tool: "list_work_items", args: map[string]any{}, mp: managerPersona, session: rs(fxJdoe),
			about: "jdoe, manager of dev-ops and delegate: a delegated item (co-assignee Mia Kovac) and a managers' step (1 of 1, first decides, no deadline, no reason given)"},
		{name: "inbox.removal", tool: "list_work_items", args: map[string]any{}, mp: removalPersona, session: rs(fxMkovac),
			about: "mkovac: a removal (change delete), requested by an administrator the approver can't read"},
		{name: "inbox.empty", tool: "list_work_items", args: map[string]any{}, mp: empty(approverPersona), session: rs(fxDlee),
			about: "nothing waiting, resource-server mode"},
		{name: "inbox.empty-personal", tool: "list_work_items", args: map[string]any{}, mp: empty(approverPersona), session: personal,
			about: "nothing waiting, personal mode"},

		// --- decide_work_item, writes on ---
		{name: "decide.approve-next", tool: "decide_work_item", args: decide(fxCaseTwoStep, "6", "approve"),
			mp: withAfter(approverPersona, fxCaseTwoStep, afterFile("case_get_after_decision.json")), session: rs(fxDlee),
			about: "approved; read back: case open, Mia Kovac next (recorded after dlee's decision)"},
		{name: "decide.approve-closed", tool: "decide_work_item", args: decide(fxCaseTwoStep, "6", "approve"),
			mp: withAfter(approverPersona, fxCaseTwoStep, afterFile("case_get_closed.json")), session: rs(fxDlee),
			about: "approved; read back: the case closed approved (the recording of the finished case)"},
		{name: "decide.reject-by-other", tool: "decide_work_item", args: decide(fxCaseTwoStep, "6", "reject"),
			mp: withAfter(approverPersona, fxCaseTwoStep, afterFile("case_get_after_decision.json")), session: rs(fxDlee),
			about: "rejected, but the read-back shows the item approved: someone else decided first"},
		{name: "decide.approve-unconfirmed", tool: "decide_work_item", args: decide(fxCaseTwoStep, "6", "approve"),
			mp: approverPersona, session: rs(fxDlee),
			about: "approved; the read-back still shows the item open with no outcome: not confirmed yet"},
		{name: "decide.reject-closed", tool: "decide_work_item", args: decide(fxCaseTwoStep, "6", "reject", "Not needed for this project."),
			mp: withAfter(approverPersona, fxCaseTwoStep, func(t *testing.T, _ *fixtureMidpoint) string {
				// No rejected case was recorded: the after-decision answer with
				// the item's outcome set to reject and the case closed, as
				// midPoint closes a case rejected in its first step.
				return mutateCase(t, testdataFile(t, "case_get_after_decision.json"), func(c map[string]any) {
					c["state"] = "closed"
					c["outcome"] = fixtureOutcomeNS + "reject"
					for _, wi := range caseWorkItems(c) {
						if out, ok := wi["output"].(map[string]any); ok {
							out["outcome"] = fixtureOutcomeNS + "reject"
							out["comment"] = "Not needed for this project."
						}
					}
				})
			}), session: rs(fxDlee),
			about: "rejected; read back: closed rejected (recording modified: outcome reject, state closed)"},
		{name: "decide.reject-open", tool: "decide_work_item", args: decide(fxCaseTwoStep, "6", "reject", "Not needed for this project."),
			mp: withAfter(approverPersona, fxCaseTwoStep, func(t *testing.T, _ *fixtureMidpoint) string {
				// The after-decision answer with the item's outcome set to
				// reject while the case is still open (midPoint has not closed it
				// yet).
				return mutateCase(t, testdataFile(t, "case_get_after_decision.json"), func(c map[string]any) {
					for _, wi := range caseWorkItems(c) {
						if out, ok := wi["output"].(map[string]any); ok {
							out["outcome"] = fixtureOutcomeNS + "reject"
						}
					}
				})
			}), session: rs(fxDlee),
			about: "rejected; read back: item rejected, case still open (recording modified: outcome reject)"},
		{name: "decide.approve-open", tool: "decide_work_item", args: decide(fxCaseManagers, "5", "approve"),
			mp: withAfter(managerPersona, fxCaseManagers, func(t *testing.T, m *fixtureMidpoint) string {
				// The managers' case as recorded before the decision, with jdoe's
				// item closed approved: the case stays open and the step names no
				// other approver, so nobody is known to be next.
				return mutateCase(t, m.cases[fxCaseManagers], func(c map[string]any) {
					for _, wi := range caseWorkItems(c) {
						wi["output"] = map[string]any{"outcome": fixtureOutcomeNS + "approve"}
						wi["closeTimestamp"] = "2026-10-01T11:02:10.118Z"
						wi["performerRef"] = map[string]any{"oid": fxJdoe, "type": "c:UserType", "targetName": "jdoe"}
					}
				})
			}), session: rs(fxJdoe),
			about: "approved the managers' step; read back: case open, no next approver known (recording modified: item closed approved)"},

		// --- decide_work_item, writes off ---
		{name: "decide.dry-run-approve", tool: "decide_work_item", args: decide(fxCaseTwoStep, "6", "approve"),
			mp: approverPersona, session: dryRun, about: "writes disabled: the approval previewed"},
		{name: "decide.dry-run-reject", tool: "decide_work_item", args: decide(fxCaseTwoStep, "6", "reject", "Not needed for this project."),
			mp: approverPersona, session: dryRun, about: "writes disabled: the rejection previewed, with its reason"},

		// --- get_case ---
		{name: "case.approver", tool: "get_case", args: map[string]any{"oid": fxCaseTwoStep}, mp: approverPersona, session: rs(fxDlee),
			about: "the two-step case as dlee reads it: only dlee's own work item is visible"},
		{name: "case.manager-delegated", tool: "get_case", args: map[string]any{"oid": fxCaseTwoStep}, mp: managerPersona, session: rs(fxJdoe),
			about: "the two-step case as jdoe reads it: the delegated item"},
		{name: "case.manager-step", tool: "get_case", args: map[string]any{"oid": fxCaseManagers}, mp: managerPersona, session: rs(fxJdoe),
			about: "the managers' case as jdoe reads it"},
		{name: "case.removal", tool: "get_case", args: map[string]any{"oid": fxCaseRemoval}, mp: removalPersona, session: rs(fxMkovac),
			about: "the removal case as mkovac reads it"},

		// --- whoami ---
		{name: "whoami.approver", tool: "whoami", args: map[string]any{}, mp: approverPersona, session: rs(fxDlee),
			about: "dlee, resource-server mode"},
		{name: "whoami.approver-personal", tool: "whoami", args: map[string]any{}, mp: approverPersona, session: personal,
			about: "dlee as the server's own account, personal mode"},
		{name: "whoami.shared", tool: "whoami", args: map[string]any{}, mp: sharedPersona, session: shared,
			about: "a shared service account in personal mode: acting.sharedCredential true"},

		// --- errors ---
		{name: "error.list.shared-credential", tool: "list_work_items", args: map[string]any{}, mp: sharedPersona, session: shared, isError: true,
			about: "list_work_items refused: shared credential, no caller identity"},
		{name: "error.list.midpoint-unavailable", tool: "list_work_items", args: map[string]any{}, isError: true,
			mp: withFail(approverPersona, "POST /ws/rest/cases/search", http.StatusServiceUnavailable), session: rs(fxDlee),
			about: "the case search answered 503"},
		{name: "error.decide.not-in-inbox", tool: "decide_work_item", args: decide(fxCaseTwoStep, "7", "approve"), isError: true,
			mp: approverPersona, session: rs(fxDlee),
			about: "a work item the case doesn't show the approver (another approver's)"},
		{name: "error.decide.already-decided", tool: "decide_work_item", args: decide(fxCaseTwoStep, "6", "approve"), isError: true,
			mp: withCase(approverPersona, fxCaseTwoStep, "case_get_after_decision.json"), session: rs(fxDlee),
			about: "the item was decided meanwhile (the case read before writing is the after-decision recording)"},
		{name: "error.decide.request-closed", tool: "decide_work_item", args: decide(fxCaseTwoStep, "6", "approve"), isError: true,
			mp: withCase(approverPersona, fxCaseTwoStep, "case_get_closed.json"), session: rs(fxDlee),
			about: "the case closed meanwhile"},
		{name: "error.decide.not-authorized", tool: "decide_work_item", args: decide(fxCaseTwoStep, "6", "approve"), isError: true,
			mp: withFail(approverPersona, completePath, http.StatusForbidden), session: rs(fxDlee),
			about: "midPoint refused the completion (403)"},
		{name: "error.decide.shared-credential", tool: "decide_work_item", args: decide(fxCaseTwoStep, "6", "approve"), isError: true,
			mp: sharedPersona, session: shared, about: "decide refused: shared credential"},
		{name: "error.case.not-authorized", tool: "get_case", args: map[string]any{"oid": fxCaseTwoStep}, isError: true,
			mp: withFail(approverPersona, "GET /ws/rest/cases/"+fxCaseTwoStep, http.StatusForbidden), session: rs(fxDlee),
			about: "midPoint refused reading the case (403)"},
	}
}

func TestWriteViewFixtures(t *testing.T) {
	if os.Getenv(envWriteViewFixtures) == "" {
		t.Skipf("set %s=1 to regenerate %s", envWriteViewFixtures, viewFixtureDir)
	}
	if err := os.MkdirAll(viewFixtureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	written := map[string]bool{}
	for _, fx := range viewFixtures() {
		t.Run(fx.name, func(t *testing.T) {
			cs := fx.session.connect(t, fx.mp(t))
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: fx.tool, Arguments: fx.args})
			if err != nil {
				t.Fatalf("CallTool(%s): %v", fx.tool, err)
			}
			if res.IsError != fx.isError {
				t.Fatalf("isError = %v, want %v: %v", res.IsError, fx.isError, res.Content)
			}
			if !fx.isError {
				if sc, _ := res.StructuredContent.(map[string]any); sc["tool"] != fx.tool {
					t.Fatalf("structuredContent.tool = %v", sc["tool"])
				}
			}
			file := struct {
				Call   map[string]any      `json:"call"`
				About  string              `json:"about"`
				Result *mcp.CallToolResult `json:"result"`
			}{map[string]any{"name": fx.tool, "arguments": fx.args}, fx.about, res}
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(false)
			enc.SetIndent("", "  ")
			if err := enc.Encode(file); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(buf.String(), "127.0.0.1") {
				t.Fatalf("fixture carries the fake midPoint's address:\n%s", buf.String())
			}
			if err := os.WriteFile(filepath.Join(viewFixtureDir, fx.name+".json"), buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			written[fx.name+".json"] = true
		})
	}
	// The generator owns the inbox fixtures.
	entries, err := os.ReadDir(viewFixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	var stale []string
	for _, e := range entries {
		// Each other view suite owns its prefix and its own generator.
		if filepath.Ext(e.Name()) == ".json" && !written[e.Name()] &&
			!strings.HasPrefix(e.Name(), "my-requests.") && !strings.HasPrefix(e.Name(), "request-access.") && !strings.HasPrefix(e.Name(), "access-review.") {
			stale = append(stale, e.Name())
			_ = os.Remove(filepath.Join(viewFixtureDir, e.Name()))
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Logf("removed stale fixtures: %v", stale)
	}
}
