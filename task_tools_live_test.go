//go:build integration

// Live check of the task and resource-test tools against a real midPoint.
// Compiled only under -tags=integration; it skips unless the environment names
// a midPoint. It drives the tools over MCP with the write gate open, so give it
// only a throwaway task: the task is suspended when the test ends.
//
// Environment:
//
//	MIDPOINT_URL, MIDPOINT_USERNAME, MIDPOINT_PASSWORD
//	MIDPOINT_IT_TASK_OID       # optional: a throwaway task to suspend, resume and run
//	MIDPOINT_IT_RESOURCE_OID   # optional: a resource to test (contacts its target system)
//	MIDPOINT_IT_PRINCIPAL_OID  # optional: the person to act as (Switch-To-Principal);
//	                           # their refusals must come back as not-authorized
//
// Example:
//
//	MIDPOINT_URL=http://localhost:8080/midpoint MIDPOINT_USERNAME=administrator MIDPOINT_PASSWORD=… \
//	MIDPOINT_IT_TASK_OID=… go test -tags=integration . -run LiveTaskTools -v
package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

func TestLiveTaskTools(t *testing.T) {
	cfg, err := midpoint.ConfigFromEnv()
	if err != nil {
		t.Skipf("skipping live task tools test: %v", err)
	}
	principal := strings.TrimSpace(os.Getenv("MIDPOINT_IT_PRINCIPAL_OID"))
	server := mcp.NewServer(&mcp.Implementation{Name: "live", Version: "t"}, nil)
	registerTaskTools(server, midpoint.NewClient(cfg), true)
	server.AddReceivingMiddleware(errorCodes)
	if principal != "" {
		server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
			return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
				return next(midpoint.WithPrincipal(ctx, principal), method, req)
			}
		})
	}
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "t"}, nil).Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	// call runs a tool and logs its text; an error result must carry a code
	// other than internal.
	call := func(tool string, args map[string]any) (map[string]any, bool) {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		var text strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text.WriteString(tc.Text)
			}
		}
		if res.IsError {
			payload, _ := res.Meta[errorMetaKey].(map[string]any)
			t.Logf("%s %v → error %v:\n%s", tool, args, payload["code"], text.String())
			if payload["code"] == midpoint.CodeInternal {
				t.Errorf("%s: internal error", tool)
			}
			return payload, false
		}
		t.Logf("%s %v →\n%s", tool, args, text.String())
		out, _ := res.StructuredContent.(map[string]any)
		return out, true
	}

	call("list_tasks", map[string]any{"limit": 5})
	call("list_tasks", map[string]any{"finishedSince": "24h", "resultStatus": "partial_error,fatal_error", "limit": 5})

	if oid := strings.TrimSpace(os.Getenv("MIDPOINT_IT_TASK_OID")); oid != "" {
		task, ok := call("get_task", map[string]any{"oid": oid})
		if !ok {
			t.Fatalf("get_task %s failed", oid)
		}
		name, _ := task["name"].(string)
		if _, ok := call("run_task", map[string]any{"taskOid": oid, "taskName": name + " (not)"}); ok {
			t.Error("run_task with a wrong name succeeded")
		}
		args := map[string]any{"taskOid": oid, "taskName": name}
		// Some of these are refused by state (run on a suspended task, resume
		// on a running one) and, for a person without the REST task actions,
		// by midPoint (not-authorized); call logs each answer.
		for _, tool := range []string{"suspend_task", "run_task", "resume_task", "run_task", "resume_task", "suspend_task"} {
			if out, ok := call(tool, args); ok && out["applied"] != true {
				t.Errorf("%s: not applied: %v", tool, out)
			}
		}
	}

	if oid := strings.TrimSpace(os.Getenv("MIDPOINT_IT_RESOURCE_OID")); oid != "" {
		brief, err := midpoint.NewClient(cfg).ResourceBriefOf(ctx, oid)
		if err != nil {
			t.Fatalf("reading resource %s: %v", oid, err)
		}
		call("test_resource", map[string]any{"resourceOid": oid, "resourceName": brief.Name})
	}
}
