package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

// registerTaskTools installs the operator tools for server tasks and resource
// tests. They run on the caller's own midPoint rights: midPoint shows a person
// only the tasks it lets them read, and refuses the actions they may not take.
// run_task, suspend_task, resume_task and test_resource sit behind the write
// gate and return a dry-run preview when it is closed.
func registerTaskTools(server *mcp.Server, client *midpoint.Client, allowWrites bool) {
	registerListTasks(server, client)
	registerGetTask(server, client)
	registerTaskAction(server, client, allowWrites, midpoint.TaskRun)
	registerTaskAction(server, client, allowWrites, midpoint.TaskSuspend)
	registerTaskAction(server, client, allowWrites, midpoint.TaskResume)
	registerTestResource(server, client, allowWrites)
}

// Who wrote the untrusted text of the task tools, as their markers say it.
const (
	fromTaskRecord     textSource = "the task's midPoint record"
	fromTaskResult     textSource = "the task's operation result"
	fromResourceTest   textSource = "the resource test"
	fromMidpointAnswer textSource = "midPoint's answer"
)

// --- list_tasks ---

type listTasksInput struct {
	Name           string `json:"name,omitempty" jsonschema:"part of the task name, case-insensitive"`
	ExecutionState string `json:"executionState,omitempty" jsonschema:"running, runnable, waiting, suspended or closed; several separated by commas"`
	ResultStatus   string `json:"resultStatus,omitempty" jsonschema:"success, warning, partial_error, fatal_error, handled_error, in_progress, not_applicable or unknown; several separated by commas, e.g. partial_error,fatal_error"`
	FinishedSince  string `json:"finishedSince,omitempty" jsonschema:"only tasks whose last run finished at or after this: an RFC 3339 time, or a duration back from now such as 24h, 90m or 7d"`
	Limit          int    `json:"limit,omitempty" jsonschema:"maximum results, default 20, max 100"`
}

type listTasksOutput struct {
	Tasks []midpoint.TaskSummary `json:"tasks"`
	Count int                    `json:"count"`
}

func registerListTasks(server *mcp.Server, client *midpoint.Client) {
	addTool(server, &mcp.Tool{
		Name:  "list_tasks",
		Title: "List tasks",
		Description: "List the midPoint server tasks (reconciliations, live syncs, recomputes, scanners, ...) you may read, " +
			"filtered by name, execution state, result status and when the last run finished. midPoint decides " +
			"what you see: a plain end user sees only the tasks they own. With finishedSince the most recently " +
			"finished come first, otherwise they come by name. Use get_task for a task's result message and failed items.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listTasksInput) (*mcp.CallToolResult, listTasksOutput, error) {
		q := midpoint.TaskQuery{Name: in.Name, Limit: in.Limit}
		var err error
		if q.ExecutionStates, err = midpoint.ParseTaskStates(in.ExecutionState); err != nil {
			return nil, listTasksOutput{}, err
		}
		if q.ResultStatuses, err = midpoint.ParseResultStatuses(in.ResultStatus); err != nil {
			return nil, listTasksOutput{}, err
		}
		if strings.TrimSpace(in.FinishedSince) != "" {
			if q.FinishedSince, err = midpoint.ParseSince(in.FinishedSince, time.Now()); err != nil {
				return nil, listTasksOutput{}, err
			}
		}
		tasks, err := client.ListTasks(ctx, q)
		if err != nil {
			return nil, listTasksOutput{}, err
		}
		return text(tasksText(fmt.Sprintf("Found %d task(s).", len(tasks)), tasks)),
			listTasksOutput{Tasks: tasks, Count: len(tasks)}, nil
	})
}

// tasksText is list_tasks' text: line 1, then one line per task.
func tasksText(line1 string, tasks []midpoint.TaskSummary) string {
	t := newListText(line1)
	for _, task := range tasks {
		taskItem(t, task)
	}
	return t.String()
}

// taskItem adds a task's item line: its name, what it is doing, what it is,
// and its OID last.
func taskItem(t *listText, task midpoint.TaskSummary) {
	var owner, object string
	if task.Owner != nil {
		owner = cmp.Or(task.Owner.Name, task.Owner.OID)
	}
	if task.Object != nil {
		object = cmp.Or(task.Object.Name, task.Object.OID)
	}
	var progress, recurring string
	if task.Progress > 0 {
		progress = strconv.FormatInt(task.Progress, 10)
	}
	if task.Recurring {
		recurring = "true"
	}
	t.item(cmp.Or(task.Name, task.OID),
		textField{"state", task.ExecutionState},
		textField{"result", task.ResultStatus},
		textField{"started", task.LastRunStart},
		textField{"finished", task.LastRunFinish},
		textField{"owner", owner},
		textField{"archetype", refNames(task.Archetypes)},
		textField{"kind", task.Kind},
		textField{"on", object},
		textField{"progress", progress},
		textField{"recurring", recurring},
		textField{"oid", task.OID},
	)
}

// --- get_task ---

func registerGetTask(server *mcp.Server, client *midpoint.Client) {
	addTool(server, &mcp.Tool{
		Name:  "get_task",
		Title: "Get task",
		Description: "Fetch one midPoint server task by OID: its state, owner and kind, the status and message of its " +
			"last run's operation result, the items it processed by outcome, and the last item that failed. " +
			untrustedTextNote,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in oidInput) (*mcp.CallToolResult, midpoint.TaskDetail, error) {
		task, err := client.GetTask(ctx, in.OID)
		if err != nil {
			return nil, midpoint.TaskDetail{}, err
		}
		return text(taskDetailText(task)), task, nil
	})
}

// taskDetailText is get_task's text: line 1, the task's item line with its
// description and result message under it, then the last failed item.
func taskDetailText(task midpoint.TaskDetail) string {
	line1 := fmt.Sprintf("Task %s is %s", textValue(cmp.Or(task.Name, task.OID)), cmp.Or(task.ExecutionState, "in an unknown state"))
	if task.Result != nil {
		line1 += ", last result " + task.Result.Status
	} else if task.ResultStatus != "" {
		line1 += ", last result " + task.ResultStatus
	}
	if task.Items != nil {
		line1 += fmt.Sprintf("; items: %d succeeded, %d failed, %d skipped", task.Items.Success, task.Items.Failure, task.Items.Skip)
	}
	t := newListText(line1 + ".")
	taskItem(t, task.TaskSummary)
	t.untrusted(fieldDescription, fromTaskRecord, task.Description)
	if task.Result != nil {
		t.untrusted(fieldMessage, fromTaskResult, task.Result.Message)
	}
	if f := task.LastFailure; f != nil {
		t.group("Last failed item:")
		t.item(cmp.Or(f.Item, "(unnamed)"), textField{"at", f.At})
		t.untrusted(fieldMessage, fromTaskResult, f.Message)
	}
	return t.String()
}

// --- run_task / suspend_task / resume_task ---

type taskActionInput struct {
	TaskOID  string `json:"taskOid" jsonschema:"OID of the task"`
	TaskName string `json:"taskName" jsonschema:"the task's midPoint name, as list_tasks and get_task give it; must match taskOid"`
}

// taskActionOutput is a task action's result: the request, and the task as
// read after it (or, in a dry run, as it is now).
type taskActionOutput struct {
	writeOutput
	Task     *midpoint.TaskSummary `json:"task,omitempty" jsonschema:"the task as read after the action; in a dry run, as it is now"`
	ReadBack string                `json:"readBack,omitempty" jsonschema:"why the task could not be read after the action, when it could not"`
}

// taskActionNote ends the description of the task action tools.
const taskActionNote = "taskName is the task's midPoint name (its unique name attribute), as list_tasks and get_task give it; " +
	"the call is refused when it doesn't match taskOid, so the person confirming it reads the right name. " +
	"Requires the write gate; otherwise a dry-run preview. The result reports the task's executionState read back after the call. "

func registerTaskAction(server *mcp.Server, client *midpoint.Client, allowWrites bool, action midpoint.TaskAction) {
	tool := &mcp.Tool{Name: string(action) + "_task"}
	switch action {
	case midpoint.TaskRun:
		tool.Title = "Run task now"
		tool.Description = "Run a midPoint server task now (midPoint's \"Run now\"): starts a runnable or closed task " +
			"at once; a recurring task then continues on its schedule. A suspended task is refused: use resume_task. "
	case midpoint.TaskSuspend:
		tool.Title = "Suspend task"
		tool.Description = "Suspend a midPoint server task: a running task stops after its current item (midPoint " +
			"waits up to 2 seconds) and a scheduled one stops being scheduled, until resume_task. "
	case midpoint.TaskResume:
		tool.Title = "Resume task"
		tool.Description = "Resume a suspended midPoint server task (or a closed recurring one), so it runs or is " +
			"scheduled again. A task that is not suspended is refused. "
	}
	tool.Description += taskActionNote + untrustedTextNote
	addTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in taskActionInput) (*mcp.CallToolResult, taskActionOutput, error) {
		task, err := client.TaskBrief(ctx, in.TaskOID)
		if err != nil {
			return nil, taskActionOutput{}, err
		}
		if err := confirmName("taskName", "task", in.TaskName, task.Name); err != nil {
			return nil, taskActionOutput{}, err
		}
		plan, err := client.PlanTaskAction(task, action)
		if err != nil {
			return nil, taskActionOutput{}, err
		}
		if !allowWrites {
			res, out := previewWrite(plan)
			appendText(res, taskStateLine(task))
			return res, taskActionOutput{writeOutput: out, Task: &task}, nil
		}

		status, message, err := client.ApplyTaskAction(ctx, plan)
		if err != nil {
			return nil, taskActionOutput{}, actionError(err)
		}
		out := taskActionOutput{writeOutput: writeOutput{
			Applied:  true,
			Summary:  plan.Summary,
			Method:   plan.Method,
			Endpoint: plan.Endpoint(),
			Result:   fmt.Sprintf("status=%d", status),
		}}
		t := newListText(fmt.Sprintf("Applied: %s (%s).", plan.Summary, out.Result))
		t.untrusted(fieldMessage, fromMidpointAnswer, message)
		after, err := client.TaskBrief(ctx, task.OID)
		if err != nil {
			out.ReadBack = err.Error()
			t.lines = append(t.lines, "The task could not be read back: "+err.Error())
		} else {
			out.Task = &after
			t.lines = append(t.lines, taskStateLine(after))
		}
		return text(t.String()), out, nil
	})
}

// taskStateLine says what state a task is in.
func taskStateLine(task midpoint.TaskSummary) string {
	s := "The task is now " + cmp.Or(task.ExecutionState, "in an unknown state")
	if task.ResultStatus != "" {
		s += " (result " + task.ResultStatus + ")"
	}
	return s + "."
}

// appendText adds a line to a result's text.
func appendText(res *mcp.CallToolResult, line string) {
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			tc.Text += "\n" + line
			return
		}
	}
	res.Content = append(res.Content, &mcp.TextContent{Text: line})
}

// actionError puts the message midPoint gave with a refusal under the error's
// text, as untrusted text. Its code stays the refusal's.
func actionError(err error) error {
	var ae *midpoint.ActionError
	if !errors.As(err, &ae) || ae.Message == "" {
		return err
	}
	t := newListText(err.Error())
	t.untrusted(fieldMessage, fromMidpointAnswer, ae.Message)
	return &explainedError{text: t.String(), err: err}
}

// explainedError is an error with text added; it unwraps to the error, so its
// code is the error's.
type explainedError struct {
	text string
	err  error
}

func (e *explainedError) Error() string { return e.text }
func (e *explainedError) Unwrap() error { return e.err }

// --- test_resource ---

type testResourceInput struct {
	ResourceOID  string `json:"resourceOid" jsonschema:"OID of the resource"`
	ResourceName string `json:"resourceName" jsonschema:"the resource's midPoint name, as list_resources and get_resource give it; must match resourceOid"`
}

type testResourceOutput struct {
	writeOutput
	Resource midpoint.ResourceBrief `json:"resource" jsonschema:"the resource; availability as read after the test, or in a dry run as it is now"`
	Test     *midpoint.ResourceTest `json:"test,omitempty" jsonschema:"the test's outcome, when it ran"`
}

func registerTestResource(server *mcp.Server, client *midpoint.Client, allowWrites bool) {
	addTool(server, &mcp.Tool{
		Name:  "test_resource",
		Title: "Test resource",
		Description: "Test a midPoint resource's connection (midPoint's \"Test connection\"): the connector's " +
			"initialization, the connection to the target system, its capabilities and schema. Reports the overall " +
			"status and the message of every check that failed. It changes no identity data, but it is gated like a " +
			"write: midPoint contacts the target system and stores the outcome on the resource (its availability, " +
			"which provisioning acts on, and possibly a refreshed schema), so with writes disabled it returns a " +
			"dry-run preview. resourceName is the resource's midPoint name, as list_resources and get_resource give it; " +
			"the call is refused when it doesn't match resourceOid, so the person confirming it reads the right name. " +
			untrustedTextNote,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in testResourceInput) (*mcp.CallToolResult, testResourceOutput, error) {
		resource, err := client.ResourceBriefOf(ctx, in.ResourceOID)
		if err != nil {
			return nil, testResourceOutput{}, err
		}
		if err := confirmName("resourceName", "resource", in.ResourceName, resource.Name); err != nil {
			return nil, testResourceOutput{}, err
		}
		plan, err := client.PlanTestResource(resource)
		if err != nil {
			return nil, testResourceOutput{}, err
		}
		if !allowWrites {
			res, out := previewWrite(plan)
			if resource.Availability != "" {
				appendText(res, fmt.Sprintf("The resource's availability is now %s.", resource.Availability))
			}
			return res, testResourceOutput{writeOutput: out, Resource: resource}, nil
		}

		test, err := client.TestResource(ctx, plan)
		if err != nil {
			return nil, testResourceOutput{}, actionError(err)
		}
		out := testResourceOutput{
			writeOutput: writeOutput{
				Applied:  true,
				Summary:  plan.Summary,
				Method:   plan.Method,
				Endpoint: plan.Endpoint(),
				Result:   "status=" + test.Status,
			},
			Resource: resource,
			Test:     &test,
		}
		if after, err := client.ResourceBriefOf(ctx, resource.OID); err == nil {
			out.Resource = after
		}
		return text(resourceTestText(out.Resource, test)), out, nil
	})
}

// resourceTestText is test_resource's text: line 1 with the overall status,
// midPoint's message under it, then each check that failed with its message.
// A message already shown is not repeated.
func resourceTestText(resource midpoint.ResourceBrief, test midpoint.ResourceTest) string {
	failed := 0
	for _, c := range test.Checks {
		if c.Failed() {
			failed++
		}
	}
	line1 := fmt.Sprintf("Tested resource %s: %s, %d of %d check(s) failed", textValue(cmp.Or(resource.Name, resource.OID)),
		test.Status, failed, len(test.Checks))
	if resource.Availability != "" {
		line1 += "; availability now " + resource.Availability
	}
	t := newListText(line1 + ".")
	shown := map[string]bool{}
	note := func(msg string) {
		msg = strings.TrimSpace(msg)
		if msg == "" || shown[msg] {
			return
		}
		shown[msg] = true
		t.untrusted(fieldMessage, fromResourceTest, msg)
	}
	note(test.Message)
	if failed > 0 {
		t.group("Failed checks:")
		for _, c := range test.Checks {
			if c.Failed() {
				t.item(c.Name, textField{"status", c.Status})
				note(c.Message)
			}
		}
	}
	return t.String()
}
