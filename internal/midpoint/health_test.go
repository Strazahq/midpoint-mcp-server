package midpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Fixture OIDs (testdata/health_*.json, recorded on midPoint 4.10.3 and made
// neutral).
const (
	healthDirOID      = "b6f9f2c8-0d5a-4e77-9a1e-9d3c0e5a1c01" // "Lab Directory": account + entitlement types
	healthKeycloakOID = "b6f9f2c8-0d5a-4e77-9a1e-9d3c0e5a1c04" // "Keycloak (realm lab)": account type only
	healthShadowOID   = "7ce0f287-5f1a-4a54-99f1-49c570c3e118" // alice's Keycloak account, failed add
	healthAliceOID    = "b6f9f2c8-0d5a-4e77-9a1e-9d3c0e5a1c4a"
	healthTaskOID     = "e5e5e5e5-2c32-4704-b080-8665d778fad5"
)

// healthNow puts the recorded failures inside a 24-hour window: the task
// finished 2026-10-03T18:13Z, the account failed 2026-10-01T16:20Z.
var healthNow = time.Date(2026, 10, 3, 19, 0, 0, 0, time.UTC)

// healthFake is a midPoint that serves the recorded fixtures. A route named
// in status answers with that status instead; shadow answers a shadow search
// by its filter.
type healthFake struct {
	t      *testing.T
	mu     sync.Mutex
	reqs   []capturedRequest
	status map[string]int
	shadow func(filter string) (int, string)
	script string // the executeScript body; default: two audit records
	tasks  string // the task search body; default: the fixture
}

func newHealthFake(t *testing.T) *healthFake {
	return &healthFake{t: t, status: map[string]int{}}
}

func emptyList() string {
	return `{"object":{"@type":"http://midpoint.evolveum.com/xml/ns/public/common/api-types-3#ObjectListType"}}`
}

func (f *healthFake) client() *Client {
	mux := http.NewServeMux()
	route := func(pattern, name string, answer func(r *http.Request, body string) (int, string)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.reqs = append(f.reqs, capturedRequest{method: r.Method, path: r.URL.Path, rawQuery: r.URL.RawQuery, body: string(b)})
			st := f.status[name]
			f.mu.Unlock()
			if st != 0 {
				http.Error(w, `{"object":{"message":"refused"}}`, st)
				return
			}
			code, out := answer(r, string(b))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_, _ = io.WriteString(w, out)
		})
	}
	route("POST /ws/rest/tasks/search", "tasks", func(*http.Request, string) (int, string) {
		if f.tasks != "" {
			return 200, f.tasks
		}
		return 200, string(fixture(f.t, "health_tasks_failed_search.json"))
	})
	route("GET /ws/rest/tasks/{oid}", "task", func(*http.Request, string) (int, string) {
		return 200, string(fixture(f.t, "health_task_get_result.json"))
	})
	route("POST /ws/rest/resources/search", "resources", func(*http.Request, string) (int, string) {
		return 200, string(fixture(f.t, "health_resources_search.json"))
	})
	route("POST /ws/rest/shadows/search", "shadows", func(_ *http.Request, body string) (int, string) {
		filter := filterOf(body)
		if !strings.Contains(filter, "kind = ") {
			return 400, `{"object":{"message":"object class must be specified"}}`
		}
		if f.shadow != nil {
			return f.shadow(filter)
		}
		return defaultShadows(f.t, filter)
	})
	route("POST /ws/rest/users/search", "users", func(*http.Request, string) (int, string) {
		return 200, `{"object":{"object":[{"oid":"` + healthAliceOID + `","name":"alice",` +
			`"linkRef":[{"oid":"` + healthShadowOID + `","type":"c:ShadowType"},{"oid":"other"}]}]}}`
	})
	route("POST /ws/rest/rpc/executeScript", "script", func(*http.Request, string) (int, string) {
		if f.script != "" {
			return 200, f.script
		}
		return 200, auditScriptResponse(
			"2026-10-03T18:13:29.120Z\tmodifyObject\texecution\tfatal_error\thttp://midpoint.evolveum.com/xml/ns/public/common/channels-3#rest\tadministrator\t\tObject not found.",
			"2026-10-03T15:30:53.040Z\tdeleteObject\texecution\tpartial_error\t\tadministrator\tprobe-user\tResource reference seems to be invalid",
		)
	})
	srv := httptest.NewServer(mux)
	f.t.Cleanup(srv.Close)
	return NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})
}

// defaultShadows answers a shadow search from the fixtures: the failed add on
// the Keycloak account, the dead groups on the directory.
func defaultShadows(t *testing.T, filter string) (int, string) {
	switch {
	case strings.Contains(filter, healthKeycloakOID) && strings.Contains(filter, `kind = "account"`) &&
		strings.Contains(filter, "operationExecution matches"):
		return 200, string(fixture(t, "health_shadows_failed_ops.json"))
	case strings.Contains(filter, healthDirOID) && strings.Contains(filter, `kind = "entitlement"`) &&
		strings.Contains(filter, "dead = true"):
		return 200, string(fixture(t, "health_shadows_dead.json"))
	}
	return 200, emptyList()
}

func auditScriptResponse(records ...string) string {
	var items []string
	for _, r := range records {
		v, _ := json.Marshal(r)
		items = append(items, `{"value":{"@type":"xsd:string","@value":`+string(v)+`}}`)
	}
	return `{"object":{"output":{"consoleOutput":"","dataOutput":{"item":[` + strings.Join(items, ",") +
		`]}},"result":{"status":"success"}}}`
}

// filterOf returns the filter text of a search body.
func filterOf(body string) string {
	var req struct {
		Query struct {
			Filter struct {
				Text string `json:"text"`
			} `json:"filter"`
		} `json:"query"`
	}
	_ = json.Unmarshal([]byte(body), &req)
	return req.Query.Filter.Text
}

func (f *healthFake) requests(path string) []capturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []capturedRequest
	for _, r := range f.reqs {
		if r.path == path {
			out = append(out, r)
		}
	}
	return out
}

func TestRecentErrorsAllSections(t *testing.T) {
	f := newHealthFake(t)
	res := f.client().RecentErrors(context.Background(), RecentErrorsQuery{Hours: 72, Now: healthNow})

	if res.Hours != 72 || res.Limit != 10 || res.Since != "2026-09-30T19:00:00Z" {
		t.Errorf("window = %d h, limit %d, since %s", res.Hours, res.Limit, res.Since)
	}

	// Failed tasks: the search names the window, orders newest first, and
	// asks for the owner's name; the message comes from a read of the task.
	ft := res.FailedTasks
	if ft.Status != SectionOK || ft.Count != 1 || ft.More || len(ft.Items) != 1 {
		t.Fatalf("failedTasks = %+v", ft)
	}
	if got, want := ft.Items[0], (FailedTask{OID: healthTaskOID, Name: "Nightly change run", Status: "partial_error",
		State: "closed", Finished: "2026-10-03T18:13:29.281Z", OwnerOID: "00000000-0000-0000-0000-000000000002",
		OwnerName: "administrator",
		Message:   "Object of type 'UserType' with OID 'e5e5e5e5-dead-4000-8000-0000decafbad' was not found."}); got != want {
		t.Errorf("failed task\n got %+v\nwant %+v", got, want)
	}
	search := f.requests("/ws/rest/tasks/search")
	if len(search) != 1 || search[0].rawQuery != "options=resolveNames" ||
		filterOf(search[0].body) != `(resultStatus = "fatal_error" or resultStatus = "partial_error") and lastRunFinishTimestamp > "2026-09-30T19:00:00Z"` ||
		!strings.Contains(search[0].body, `"orderBy":"lastRunFinishTimestamp","orderDirection":"descending"`) ||
		!strings.Contains(search[0].body, `"maxSize":11`) {
		t.Errorf("task search = %+v", search)
	}
	if get := f.requests("/ws/rest/tasks/" + healthTaskOID); len(get) != 1 || get[0].rawQuery != "include=result" {
		t.Errorf("task read = %+v", get)
	}

	// Accounts with failed operations: the failed add, named, with its owner.
	fa := res.FailedAccounts
	if fa.Status != SectionOK || fa.Count != 1 || len(fa.Items) != 1 {
		t.Fatalf("failedAccounts = %+v", fa)
	}
	a := fa.Items[0]
	if a.OID != healthShadowOID || a.Name != "alice" || a.Kind != "account" || a.Intent != "default" ||
		a.ResourceOID != healthKeycloakOID || a.ResourceName != "Keycloak (realm lab)" ||
		a.OwnerOID != healthAliceOID || a.OwnerName != "alice" || a.Status != "fatal_error" ||
		a.Time != "2026-10-01T16:20:08.780Z" || a.Change != "add" ||
		!strings.HasPrefix(a.Message, "Object already exists on the resource (Keycloak (realm lab)") {
		t.Errorf("failed account = %+v", a)
	}
	if !strings.Contains(strings.Join(fa.Notes, "\n"), "last few operation records") {
		t.Errorf("failedAccounts notes = %q", fa.Notes)
	}

	// Broken accounts: the four dead groups, newest death first.
	b := res.BrokenAccounts
	if b.Status != SectionOK || b.Count != 4 || b.Dead != 4 || b.Pending != 0 || len(b.Items) != 4 {
		t.Fatalf("brokenAccounts = %+v", b)
	}
	if first := b.Items[0]; first.Name != "lab-group-scouts" || !first.Dead || first.Kind != "entitlement" ||
		first.ResourceName != "Lab Directory" || first.Died != "2026-10-01T16:24:04.427Z" {
		t.Errorf("first broken = %+v", first)
	}

	// Systems: both up.
	s := res.Systems
	if s.Status != SectionOK || s.Total != 2 || s.Up != 2 || s.Count != 0 || len(s.Items) != 0 || s.Capped {
		t.Errorf("systems = %+v", s)
	}

	// Audit: both records, newest first as the script returned them.
	au := res.Audit
	if au.Status != SectionOK || au.Count != 2 || au.Items[0].Outcome != "fatal_error" ||
		au.Items[1].Target != "probe-user" || au.Items[0].EventStage != "execution" {
		t.Errorf("audit = %+v", au)
	}

	// The resource list leaves the schema out; the shadow searches go
	// resource by resource, kind by kind, from the repository only.
	rs := f.requests("/ws/rest/resources/search")
	if len(rs) != 1 || !strings.Contains(rs[0].rawQuery, "exclude=schema") || !strings.Contains(rs[0].body, `"maxSize":21`) {
		t.Errorf("resource search = %+v", rs)
	}
	shadows := f.requests("/ws/rest/shadows/search")
	// Directory: account + entitlement; Keycloak: account. Two searches each.
	if len(shadows) != 6 {
		t.Errorf("%d shadow searches, want 6", len(shadows))
	}
	for _, r := range shadows {
		if r.rawQuery != "options=noFetch" {
			t.Errorf("shadow search query = %q", r.rawQuery)
		}
		if strings.Contains(r.body, `kind = \"entitlement\"`) && strings.Contains(r.body, healthKeycloakOID) {
			t.Errorf("searched a kind the resource does not define: %s", r.body)
		}
	}
	users := f.requests("/ws/rest/users/search")
	if len(users) != 1 || !strings.Contains(users[0].body, `linkRef matches (oid = \"`+healthShadowOID+`\")`) {
		t.Errorf("owner search = %+v", users)
	}
	if script := f.requests("/ws/rest/rpc/executeScript"); len(script) != 1 {
		t.Errorf("%d audit scripts, want 1", len(script))
	}
}

// One refused source leaves the others answering.
func TestRecentErrorsRefusedSources(t *testing.T) {
	cases := []struct {
		name   string
		status map[string]int
		check  func(t *testing.T, r RecentErrors)
	}{
		{"tasks refused", map[string]int{"tasks": 403}, func(t *testing.T, r RecentErrors) {
			if r.FailedTasks.Status != SectionRefused || r.FailedTasks.Code != CodeNotAuthorized ||
				!strings.Contains(r.FailedTasks.Reason, "/tasks/search") || r.FailedTasks.Items == nil {
				t.Errorf("failedTasks = %+v", r.FailedTasks)
			}
			if r.FailedAccounts.Status != SectionOK || r.Systems.Status != SectionOK || r.Audit.Status != SectionOK {
				t.Errorf("other sections: %s %s %s", r.FailedAccounts.Status, r.Systems.Status, r.Audit.Status)
			}
		}},
		{"script refused", map[string]int{"script": 403}, func(t *testing.T, r RecentErrors) {
			if r.Audit.Status != SectionSkipped || r.Audit.Code != CodeNotAuthorized ||
				!strings.HasPrefix(r.Audit.Reason, "needs script access") {
				t.Errorf("audit = %+v", r.Audit)
			}
			if r.FailedTasks.Status != SectionOK {
				t.Errorf("failedTasks = %s", r.FailedTasks.Status)
			}
		}},
		{"script broken", map[string]int{"script": 500}, func(t *testing.T, r RecentErrors) {
			if r.Audit.Status != SectionFailed || r.Audit.Code != CodeAuditUnavailable {
				t.Errorf("audit = %+v", r.Audit)
			}
		}},
		{"resources refused", map[string]int{"resources": 403}, func(t *testing.T, r RecentErrors) {
			for name, h := range map[string]SectionHead{"failedAccounts": r.FailedAccounts.SectionHead,
				"brokenAccounts": r.BrokenAccounts.SectionHead, "systems": r.Systems.SectionHead} {
				if h.Status != SectionRefused || h.Code != CodeNotAuthorized || !strings.HasPrefix(h.Reason, "listing systems: ") {
					t.Errorf("%s = %+v", name, h)
				}
			}
			if r.FailedTasks.Status != SectionOK || r.Audit.Status != SectionOK {
				t.Errorf("other sections: %s %s", r.FailedTasks.Status, r.Audit.Status)
			}
		}},
		{"shadows refused", map[string]int{"shadows": 403}, func(t *testing.T, r RecentErrors) {
			if r.FailedAccounts.Status != SectionRefused || r.BrokenAccounts.Status != SectionRefused {
				t.Errorf("shadow sections: %+v %+v", r.FailedAccounts.SectionHead, r.BrokenAccounts.SectionHead)
			}
			if r.Systems.Status != SectionOK || r.Systems.Total != 2 {
				t.Errorf("systems = %+v", r.Systems)
			}
		}},
		{"owners refused", map[string]int{"users": 403}, func(t *testing.T, r RecentErrors) {
			fa := r.FailedAccounts
			if fa.Status != SectionOK || len(fa.Items) != 1 || fa.Items[0].OwnerName != "" ||
				!strings.Contains(strings.Join(fa.Notes, "\n"), "could not look up the owners") {
				t.Errorf("failedAccounts = %+v", fa)
			}
		}},
		{"task message unreadable", map[string]int{"task": 403}, func(t *testing.T, r RecentErrors) {
			ft := r.FailedTasks
			if ft.Status != SectionOK || len(ft.Items) != 1 || ft.Items[0].Message != "" ||
				!strings.Contains(strings.Join(ft.Notes, "\n"), "could not read the message of 1 task") {
				t.Errorf("failedTasks = %+v", ft)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newHealthFake(t)
			f.status = c.status
			c.check(t, f.client().RecentErrors(context.Background(), RecentErrorsQuery{Hours: 72, Now: healthNow}))
		})
	}
}

// A search that fails for one resource becomes a note; the section still
// answers from the others.
func TestRecentErrorsPartialShadowFailure(t *testing.T) {
	f := newHealthFake(t)
	f.shadow = func(filter string) (int, string) {
		if strings.Contains(filter, healthDirOID) {
			return 502, `{"object":{"message":"No object type definition"}}`
		}
		return defaultShadows(t, filter)
	}
	r := f.client().RecentErrors(context.Background(), RecentErrorsQuery{Hours: 72, Now: healthNow})
	if r.FailedAccounts.Status != SectionOK || len(r.FailedAccounts.Items) != 1 {
		t.Errorf("failedAccounts = %+v", r.FailedAccounts)
	}
	notes := strings.Join(r.FailedAccounts.Notes, "\n")
	if !strings.Contains(notes, "Lab Directory (account): search failed: midPoint /shadows/search: unexpected status 502") {
		t.Errorf("notes = %q", notes)
	}
	// Every broken-account search on the directory failed, Keycloak's worked.
	if r.BrokenAccounts.Status != SectionOK || r.BrokenAccounts.Count != 0 {
		t.Errorf("brokenAccounts = %+v", r.BrokenAccounts.SectionHead)
	}

	// When every search fails, the section fails with the first error.
	f2 := newHealthFake(t)
	f2.shadow = func(string) (int, string) { return 502, `{}` }
	r2 := f2.client().RecentErrors(context.Background(), RecentErrorsQuery{Now: healthNow})
	if h := r2.FailedAccounts.SectionHead; h.Status != SectionFailed || h.Code != CodeMidpointUnavailable ||
		!strings.HasPrefix(h.Reason, "searching ") {
		t.Errorf("failedAccounts = %+v", h)
	}
}

// A failure record older than the window is not reported, whatever midPoint
// returned.
func TestRecentErrorsWindowAppliesToRecords(t *testing.T) {
	f := newHealthFake(t)
	r := f.client().RecentErrors(context.Background(), RecentErrorsQuery{Hours: 1, Now: healthNow})
	if r.FailedAccounts.Status != SectionOK || len(r.FailedAccounts.Items) != 0 {
		t.Errorf("failedAccounts = %+v", r.FailedAccounts)
	}
	for _, req := range f.requests("/ws/rest/shadows/search") {
		if f := filterOf(req.body); strings.Contains(f, "operationExecution") &&
			!strings.HasSuffix(f, `operationExecution matches ((status = "fatal_error" or status = "partial_error") and timestamp > "2026-10-03T18:00:00Z")`) {
			t.Errorf("shadow search without the window: %s", f)
		}
	}
}

// Past the limit the section says there is more.
func TestRecentErrorsMore(t *testing.T) {
	f := newHealthFake(t)
	f.tasks = `{"object":{"object":[` +
		`{"oid":"t1","name":"one","resultStatus":"fatal_error","lastRunFinishTimestamp":"2026-10-03T18:00:00Z"},` +
		`{"oid":"t2","name":"two","resultStatus":"fatal_error","lastRunFinishTimestamp":"2026-10-03T17:00:00Z"}]}}`
	f.script = auditScriptResponse("a\tb\texecution\tfatal_error", "c\td\texecution\tfatal_error")
	r := f.client().RecentErrors(context.Background(), RecentErrorsQuery{Limit: 1, Now: healthNow})
	if !r.FailedTasks.More || len(r.FailedTasks.Items) != 1 || r.FailedTasks.Items[0].OID != "t1" {
		t.Errorf("failedTasks = %+v", r.FailedTasks)
	}
	if !r.Audit.More || len(r.Audit.Items) != 1 {
		t.Errorf("audit = %+v", r.Audit)
	}
	if !r.BrokenAccounts.More || len(r.BrokenAccounts.Items) != 1 {
		t.Errorf("brokenAccounts = %+v", r.BrokenAccounts.SectionHead)
	}
	if req := f.requests("/ws/rest/tasks/search"); len(req) != 1 || !strings.Contains(req[0].body, `"maxSize":2`) {
		t.Errorf("task search = %+v", req)
	}
}

// Past 20 systems, only the first 20 are checked, and every section says so.
func TestRecentErrorsCapsSystems(t *testing.T) {
	var objs []string
	for i := range maxScannedResources + 1 {
		objs = append(objs, fmt.Sprintf(`{"oid":"r%d","name":"sys%d","operationalState":{"lastAvailabilityStatus":"up"},`+
			`"schemaHandling":{"objectType":{"kind":"account"}}}`, i, i))
	}
	mux := http.NewServeMux()
	var mu sync.Mutex
	shadowCalls := 0
	mux.HandleFunc("POST /ws/rest/resources/search", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"object":{"object":[`+strings.Join(objs, ",")+`]}}`)
	})
	mux.HandleFunc("POST /ws/rest/shadows/search", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		shadowCalls++
		mu.Unlock()
		_, _ = io.WriteString(w, emptyList())
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, emptyList()) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})
	r := c.RecentErrors(context.Background(), RecentErrorsQuery{Now: healthNow})
	if !r.Systems.Capped || r.Systems.Total != maxScannedResources || r.Systems.Up != maxScannedResources {
		t.Errorf("systems = %+v", r.Systems)
	}
	if shadowCalls != 2*maxScannedResources {
		t.Errorf("%d shadow searches, want %d", shadowCalls, 2*maxScannedResources)
	}
	for _, notes := range [][]string{r.FailedAccounts.Notes, r.BrokenAccounts.Notes, r.Systems.Notes} {
		if len(notes) == 0 || !strings.HasPrefix(notes[0], "checked the first 20 systems only") {
			t.Errorf("notes = %q", notes)
		}
	}
}

func TestSystemsSection(t *testing.T) {
	var rs []errorResourceJSON
	for _, raw := range []string{
		`{"oid":"a","name":"Up","operationalState":{"lastAvailabilityStatus":"up"}}`,
		`{"oid":"b","name":"Down","operationalState":{"lastAvailabilityStatus":"down","message":"Connection refused","timestamp":"2026-10-03T10:00:00Z"}}`,
		`{"oid":"c","name":"Broken","operationalState":{"lastAvailabilityStatus":"broken","message":"Configuration error"}}`,
		`{"oid":"d","name":"Resting","operationalState":{"lastAvailabilityStatus":"up"},"administrativeOperationalState":{"administrativeAvailabilityStatus":"maintenance"}}`,
		`{"oid":"e","name":"New"}`,
	} {
		var r errorResourceJSON
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			t.Fatal(err)
		}
		rs = append(rs, r)
	}
	s := systemsSection(rs, false)
	if s.Total != 5 || s.Up != 1 || s.Untested != 1 || s.Count != 3 {
		t.Errorf("systems = %+v", s)
	}
	want := []SystemDown{
		{OID: "b", Name: "Down", Status: "down", Since: "2026-10-03T10:00:00Z", Message: "Connection refused"},
		{OID: "c", Name: "Broken", Status: "broken", Message: "Configuration error"},
		{OID: "d", Name: "Resting", Status: "maintenance"},
	}
	for i, w := range want {
		if i >= len(s.Items) || s.Items[i] != w {
			t.Errorf("item %d = %+v, want %+v", i, s.Items, w)
		}
	}
}

// Templates and abstract resources are not systems: they are neither listed
// nor searched.
func TestRecentErrorsSkipsTemplates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /ws/rest/resources/search", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"object":{"object":[`+
			`{"oid":"t","name":"Template","template":true,"schemaHandling":{"objectType":{"kind":"account"}}},`+
			`{"oid":"a","name":"Abstract","abstract":true,"schemaHandling":{"objectType":{"kind":"account"}}},`+
			`{"oid":"n","name":"No types","operationalState":{"lastAvailabilityStatus":"up"}}]}}`)
	})
	mux.HandleFunc("POST /ws/rest/shadows/search", func(http.ResponseWriter, *http.Request) {
		t.Error("searched shadows")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, emptyList()) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	r := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"}).RecentErrors(context.Background(), RecentErrorsQuery{})
	if r.Systems.Total != 1 || r.Systems.Up != 1 {
		t.Errorf("systems = %+v", r.Systems)
	}
	if notes := strings.Join(r.FailedAccounts.Notes, "\n"); !strings.Contains(notes, "not searched: No types") ||
		strings.Contains(notes, "last few operation records") {
		t.Errorf("notes = %q", notes)
	}
}

func TestBrokenAccountPending(t *testing.T) {
	res := errorResourceJSON{OID: "r"}
	for _, c := range []struct {
		raw    string
		ok     bool
		ops    int
		status string
	}{
		// Completed operations midPoint still keeps do not make it broken.
		{`{"oid":"s","name":"x","pendingOperation":{"executionStatus":"completed","resultStatus":"success"}}`, false, 0, ""},
		{`{"oid":"s","name":"x","pendingOperation":[` +
			`{"executionStatus":"executing","resultStatus":"in_progress","requestTimestamp":"2026-10-03T10:00:00Z"},` +
			`{"executionStatus":"executionPending","resultStatus":"fatal_error","requestTimestamp":"2026-10-03T11:00:00Z"},` +
			`{"executionStatus":"completed"}]}`, true, 2, "executionPending fatal_error"},
		{`{"oid":"s","name":"x","dead":true,"deathTimestamp":"2026-10-03T09:00:00Z"}`, true, 0, ""},
	} {
		a, ok := brokenAccount(json.RawMessage(c.raw), res)
		if ok != c.ok || a.PendingOps != c.ops || a.PendingStatus != c.status {
			t.Errorf("%s: ok=%v %+v", c.raw, ok, a)
		}
	}
}

func TestResourceKinds(t *testing.T) {
	for raw, want := range map[string]string{
		`{"schemaHandling":{"objectType":[{"kind":"entitlement"},{"kind":"account"},{"kind":"generic"}]}}`: "account,entitlement",
		`{"schemaHandling":{"objectType":{"intent":"default"}}}`:                                           "account",
		`{"schemaHandling":{"objectType":[{"kind":"entitlement"},{"kind":"entitlement"}]}}`:                "entitlement",
		`{}`: "",
	} {
		var r errorResourceJSON
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(r.kinds(), ","); got != want {
			t.Errorf("%s: kinds %q, want %q", raw, got, want)
		}
	}
}

func TestNormalizeErrorWindow(t *testing.T) {
	for _, c := range []struct{ h, l, wh, wl int }{
		{0, 0, 24, 10}, {-5, -1, 24, 10}, {1, 1, 1, 1}, {168, 50, 168, 50}, {720, 500, 168, 50},
	} {
		if h, l := NormalizeErrorWindow(c.h, c.l); h != c.wh || l != c.wl {
			t.Errorf("NormalizeErrorWindow(%d, %d) = %d, %d", c.h, c.l, h, l)
		}
	}
}

func TestAuditErrorsGroovy(t *testing.T) {
	g := auditErrorsGroovy(time.Date(2026, 10, 2, 19, 0, 0, 0, time.FixedZone("x", 3600)), 11)
	for _, want := range []string{
		"newXMLGregorianCalendar('2026-10-02T18:00:00Z')",
		".item(AuditEventRecordType.F_TIMESTAMP).gt(since)",
		".item(AuditEventRecordType.F_EVENT_STAGE).eq(AuditEventStageType.EXECUTION)",
		".item(AuditEventRecordType.F_OUTCOME).eq(OperationResultStatusType.FATAL_ERROR)",
		".or().item(AuditEventRecordType.F_OUTCOME).eq(OperationResultStatusType.PARTIAL_ERROR)",
		".desc(AuditEventRecordType.F_TIMESTAMP).maxSize(11)",
	} {
		if !strings.Contains(g, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	body, _ := json.Marshal(auditScriptBody(g))
	if !strings.Contains(string(body), `"type":"execute-script"`) {
		t.Errorf("script body = %s", body)
	}
}

func TestParseAuditErrorItems(t *testing.T) {
	items := []json.RawMessage{
		json.RawMessage(`{"@type":"xsd:string","@value":"t1\tmodifyObject\texecution\tfatal_error\trest\tadmin\tjoe\tboom"}`),
		json.RawMessage(`{"@type":"xsd:string","@value":""}`),
		json.RawMessage(`{"@type":"xsd:string","@value":"t2\taddObject"}`),
		json.RawMessage(`42`),
	}
	got := parseAuditErrorItems(items)
	want := []AuditRecord{
		{Timestamp: "t1", EventType: "modifyObject", EventStage: "execution", Outcome: "fatal_error",
			Channel: "rest", Initiator: "admin", Target: "joe", Message: "boom"},
		{Timestamp: "t2", EventType: "addObject"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("parsed %+v", got)
	}
}

// Under impersonation every call carries the caller.
func TestRecentErrorsRunsAsCaller(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]bool{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.Header.Get(SwitchToPrincipalHeader)] = true
		mu.Unlock()
		_, _ = io.WriteString(w, emptyList())
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})
	c.RecentErrors(WithPrincipal(context.Background(), "carol-oid"), RecentErrorsQuery{})
	if len(seen) != 1 || !seen["carol-oid"] {
		t.Errorf("principals seen: %v", seen)
	}
}
