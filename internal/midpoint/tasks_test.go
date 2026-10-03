package midpoint

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// taskServer answers task and resource REST calls from canned bodies and
// records what it was sent.
func taskServer(t *testing.T, routes map[string]func(w http.ResponseWriter, r *http.Request)) (*Client, *[]capturedRequest) {
	t.Helper()
	var reqs []capturedRequest
	mux := http.NewServeMux()
	for pattern, h := range routes {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			reqs = append(reqs, capturedRequest{r.Method, r.URL.Path, r.URL.RawQuery, string(b)})
			h(w, r)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"}), &reqs
}

func answer(status int, body []byte) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}
}

func TestListTasksRequestAndDecode(t *testing.T) {
	c, reqs := taskServer(t, map[string]func(http.ResponseWriter, *http.Request){
		"POST /ws/rest/tasks/search": answer(200, fixture(t, "tasks_search.json")),
	})
	since := time.Date(2026, 10, 2, 18, 0, 0, 0, time.UTC)
	tasks, err := c.ListTasks(context.Background(), TaskQuery{
		Name:            `Re"con`,
		ExecutionStates: []string{"closed", "suspended"},
		ResultStatuses:  []string{"partial_error"},
		FinishedSince:   since,
		Limit:           500,
	})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}

	req := (*reqs)[0]
	for _, q := range []string{"options=resolveNames", "exclude=operationStats", "exclude=activityState", "exclude=result"} {
		if !strings.Contains(req.rawQuery, q) {
			t.Errorf("query %q lacks %q", req.rawQuery, q)
		}
	}
	var body struct {
		Query struct {
			Filter struct{ Text string } `json:"filter"`
			Paging taskPaging            `json:"paging"`
		} `json:"query"`
	}
	if err := json.Unmarshal([]byte(req.body), &body); err != nil {
		t.Fatalf("search body: %v", err)
	}
	wantFilter := `name contains[polyStringNorm] "Re\"con" and (executionState = "closed" or executionState = "suspended")` +
		` and resultStatus = "partial_error" and lastRunFinishTimestamp >= "2026-10-02T18:00:00Z"`
	if body.Query.Filter.Text != wantFilter {
		t.Errorf("filter = %s\nwant     %s", body.Query.Filter.Text, wantFilter)
	}
	if p := body.Query.Paging; p.MaxSize != maxLimit || p.OrderBy != "lastRunFinishTimestamp" || p.OrderDirection != "descending" {
		t.Errorf("paging = %+v", p)
	}

	if len(tasks) != 4 {
		t.Fatalf("got %d tasks, want 4", len(tasks))
	}
	ts := tasks[0]
	if ts.Name != "Trigger Scanner" || ts.ExecutionState != "runnable" || ts.ResultStatus != "success" || !ts.Recurring ||
		ts.Kind != "triggerScan" || ts.Owner == nil || ts.Owner.Name != "administrator" || ts.Owner.Type != "User" {
		t.Errorf("trigger scanner = %+v", ts)
	}
	if got := []string{ts.Archetypes[0].Name, ts.Archetypes[1].Name}; !slices.Equal(got, []string{"System task", "Trigger scanner task"}) {
		t.Errorf("archetypes = %v", got)
	}
	fc := tasks[1]
	if fc.ResultStatus != "partial_error" || fc.Recurring || fc.Progress != 1 || fc.Kind != "explicitChangeExecution" ||
		fc.Completion == "" || len(fc.Archetypes) != 1 || fc.Archetypes[0].Name != "Utility task" {
		t.Errorf("failing change = %+v", fc)
	}
}

func TestListTasksDefaults(t *testing.T) {
	c, reqs := taskServer(t, map[string]func(http.ResponseWriter, *http.Request){
		// A caller who may read no task gets an empty list (End user, live).
		"POST /ws/rest/tasks/search": answer(200, []byte(`{"object":{"@type":"api:ObjectListType"}}`)),
	})
	tasks, err := c.ListTasks(context.Background(), TaskQuery{})
	if err != nil || len(tasks) != 0 {
		t.Fatalf("ListTasks = %v, %v", tasks, err)
	}
	body := (*reqs)[0].body
	if strings.Contains(body, "filter") || !strings.Contains(body, `"orderBy":"name"`) ||
		!strings.Contains(body, `"orderDirection":"ascending"`) || !strings.Contains(body, `"maxSize":20`) {
		t.Errorf("body = %s", body)
	}
}

func TestParseEnumLists(t *testing.T) {
	got, err := ParseResultStatuses("partialError, FATAL_ERROR,partial-error,,success")
	if err != nil || !slices.Equal(got, []string{"partial_error", "fatal_error", "success"}) {
		t.Errorf("ParseResultStatuses = %v, %v", got, err)
	}
	got, err = ParseTaskStates(" Running,closed ")
	if err != nil || !slices.Equal(got, []string{"running", "closed"}) {
		t.Errorf("ParseTaskStates = %v, %v", got, err)
	}
	if got, err := ParseTaskStates(""); err != nil || got != nil {
		t.Errorf("empty = %v, %v", got, err)
	}
	_, err = ParseTaskStates("paused")
	if code, _ := ErrorCode(err); code != CodeInvalidInput || !strings.Contains(err.Error(), "running, runnable, waiting, suspended, closed") {
		t.Errorf("unknown state: %v (%s)", err, code)
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{
		"24h":                       now.Add(-24 * time.Hour),
		"90m":                       now.Add(-90 * time.Minute),
		"7d":                        now.AddDate(0, 0, -7),
		"2026-10-01T08:00:00+02:00": time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC),
	} {
		got, err := ParseSince(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("ParseSince(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"yesterday", "-1h", "0d", "1.5d"} {
		if _, err := ParseSince(in, now); err == nil {
			t.Errorf("ParseSince(%q) succeeded", in)
		} else if code, _ := ErrorCode(err); code != CodeInvalidInput {
			t.Errorf("ParseSince(%q) code = %s", in, code)
		}
	}
}

func TestGetTaskDetail(t *testing.T) {
	c, reqs := taskServer(t, map[string]func(http.ResponseWriter, *http.Request){
		"GET /ws/rest/tasks/{oid}": answer(200, fixture(t, "task_get_partial_error.json")),
	})
	d, err := c.GetTask(context.Background(), "e5e5e5e5-2c32-4704-b080-8665d778fad5")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	q := (*reqs)[0].rawQuery
	if !strings.Contains(q, "include=result") || strings.Contains(q, "exclude=activityState") || strings.Contains(q, "exclude=result") {
		t.Errorf("query = %s", q)
	}
	const msg = "Object of type 'UserType' with OID 'e5e5e5e5-dead-4000-8000-0000decafbad' was not found."
	if d.Result == nil || d.Result.Status != "partial_error" || d.Result.Message != msg {
		t.Errorf("result = %+v", d.Result)
	}
	if d.Items == nil || *d.Items != (ItemCounts{Failure: 1}) {
		t.Errorf("items = %+v", d.Items)
	}
	if f := d.LastFailure; f == nil || f.Item != "#1" || f.Message != msg || f.At == "" {
		t.Errorf("last failure = %+v", f)
	}
	if d.Owner == nil || d.Owner.Name != "administrator" || d.ExecutionState != "closed" {
		t.Errorf("summary = %+v", d.TaskSummary)
	}
}

func TestTaskReadErrors(t *testing.T) {
	c, _ := taskServer(t, map[string]func(http.ResponseWriter, *http.Request){
		"GET /ws/rest/tasks/{oid}": answer(403, []byte(`{"object":{"@type":"c:OperationResultType","status":"fatal_error","message":"Access denied"}}`)),
	})
	_, err := c.TaskBrief(context.Background(), "t-1")
	if code, _ := ErrorCode(err); code != CodeNotAuthorized {
		t.Errorf("403 read: %v (%s)", err, code)
	}
	_, err = c.TaskBrief(context.Background(), " ")
	if code, _ := ErrorCode(err); code != CodeInvalidInput {
		t.Errorf("empty oid: %v (%s)", err, code)
	}
}

func TestOpResultMessageFallsBackToFailedChild(t *testing.T) {
	body := []byte(`{"object":{"@type":"c:OperationResultType","operation":"run","status":"fatal_error","partialResults":[
		{"operation":"a","status":"success","message":"fine"},
		{"operation":"b","status":"fatal_error","partialResults":{"operation":"c","status":"fatal_error","message":"deep cause"}}]}}`)
	r, ok := decodeOpResult(body)
	if !ok || r.message() != "deep cause" {
		t.Errorf("message = %q (%v)", r.message(), ok)
	}
	if _, ok := decodeOpResult([]byte("<!DOCTYPE html>")); ok {
		t.Error("an HTML body decoded as an operation result")
	}
	ok200, _ := decodeOpResult([]byte(`{"object":{"status":"success"}}`))
	if ok200.message() != "" {
		t.Error("a successful result without a message has one")
	}
}

func TestPlanTaskAction(t *testing.T) {
	c := NewClient(Config{})
	task := func(state, scheduling string, recurring bool) TaskSummary {
		return TaskSummary{OID: "t-1", Name: "Nightly recon", ExecutionState: state, SchedulingState: scheduling, Recurring: recurring}
	}
	for _, tc := range []struct {
		action  TaskAction
		task    TaskSummary
		refused string
	}{
		{TaskRun, task("runnable", "ready", true), ""},
		{TaskRun, task("closed", "closed", false), ""},
		{TaskRun, task("suspended", "suspended", true), "use resume_task"},
		{TaskSuspend, task("running", "ready", true), ""},
		{TaskSuspend, task("closed", "closed", false), ""},
		{TaskResume, task("suspended", "suspended", false), ""},
		{TaskResume, task("closed", "closed", true), ""},
		{TaskResume, task("closed", "closed", false), "use run_task"},
		{TaskResume, task("running", "ready", true), "is running, not suspended"},
	} {
		plan, err := c.PlanTaskAction(tc.task, tc.action)
		if tc.refused != "" {
			code, _ := ErrorCode(err)
			if err == nil || code != CodeInvalidInput || !strings.Contains(err.Error(), tc.refused) {
				t.Errorf("%s on %s: err = %v (%s), want refusal %q", tc.action, tc.task.SchedulingState, err, code, tc.refused)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s on %s: %v", tc.action, tc.task.SchedulingState, err)
			continue
		}
		if plan.Method != http.MethodPost || plan.Path != "/tasks/t-1/"+string(tc.action) || plan.Body != nil ||
			!strings.Contains(plan.Summary, `task "Nightly recon" (t-1)`) {
			t.Errorf("%s plan = %+v", tc.action, plan)
		}
	}
}

func TestApplyTaskAction(t *testing.T) {
	// Resuming a task that runs is refused with 500 and the reason (live).
	refused := fixture(t, "task_resume_refused.json")
	c, _ := taskServer(t, map[string]func(http.ResponseWriter, *http.Request){
		"POST /ws/rest/tasks/ok/run":         answer(204, nil),
		"POST /ws/rest/tasks/partial/run":    answer(250, []byte(`{"object":{"status":"partial_error","message":"half done"}}`)),
		"POST /ws/rest/tasks/state/resume":   answer(500, refused),
		"POST /ws/rest/tasks/denied/suspend": answer(403, []byte("<!DOCTYPE html><html></html>")),
	})
	ctx := context.Background()

	status, msg, err := c.ApplyTaskAction(ctx, Plan{Method: http.MethodPost, Path: "/tasks/ok/run"})
	if err != nil || status != 204 || msg != "" {
		t.Errorf("204: %d %q %v", status, msg, err)
	}
	status, msg, err = c.ApplyTaskAction(ctx, Plan{Method: http.MethodPost, Path: "/tasks/partial/run"})
	if err != nil || status != 250 || msg != "half done" {
		t.Errorf("250: %d %q %v", status, msg, err)
	}

	_, _, err = c.ApplyTaskAction(ctx, Plan{Method: http.MethodPost, Path: "/tasks/state/resume"})
	var ae *ActionError
	if !errors.As(err, &ae) || !strings.HasPrefix(ae.Message, "Attempted to resume non-recurring task that was not suspended.") {
		t.Fatalf("500: %v", err)
	}
	if code, _ := ErrorCode(err); code != CodeMidpointUnavailable || err.Error() != "midPoint /tasks/state/resume: unexpected status 500 Internal Server Error" {
		t.Errorf("500 text %q code %s", err, code)
	}

	_, _, err = c.ApplyTaskAction(ctx, Plan{Method: http.MethodPost, Path: "/tasks/denied/suspend"})
	if errors.As(err, &ae) {
		t.Errorf("an HTML 403 became an ActionError: %v", err)
	}
	if code, _ := ErrorCode(err); code != CodeNotAuthorized {
		t.Errorf("403 code %s", code)
	}
}

func TestTaskKindAndRecurrence(t *testing.T) {
	for body, want := range map[string]struct {
		kind      string
		recurring bool
	}{
		// An approval task has a legacy handler and no activity (live).
		`{"oid":"a","handlerUri":"http://midpoint.evolveum.com/xml/ns/public/model/operation/handler-3"}`:    {"", false},
		`{"oid":"b","activity":{"work":{"reconciliation":{}}},"schedule":{"cronLikePattern":"0 0 1 * * ?"}}`: {"reconciliation", true},
		`{"oid":"c","activity":{"work":{"noOp":{}}},"schedule":{"recurrence":"single","interval":60}}`:       {"noOp", false},
		`{"oid":"d","schedule":{"recurrence":"recurring"}}`:                                                  {"", true},
	} {
		var tj taskJSON
		if err := json.Unmarshal([]byte(body), &tj); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if s := tj.summary(); s.Kind != want.kind || s.Recurring != want.recurring {
			t.Errorf("%s: kind %q recurring %v", body, s.Kind, s.Recurring)
		}
	}
}
