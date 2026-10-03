package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCancelRequest(t *testing.T) {
	for _, gate := range []bool{false, true} {
		for _, state := range []string{"open", "created", "closing", "closed", "unknown", ""} {
			for _, owner := range []bool{false, true} {
				name := state
				if gate {
					name += "/writes"
				}
				if owner {
					name += "/owner"
				}
				t.Run(name, func(t *testing.T) {
					f := newRequesterFixture(t)
					f.writes = gate
					f.before = mutateCase(t, f.before, func(c map[string]any) {
						c["state"] = state
						if !owner {
							delete(c, "requestorRef")
						}
					})
					cs := f.connect(t)
					res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "cancel_request", Arguments: map[string]any{"caseOid": fxCaseTwoStep, "userName": "bstone", "roleName": "db-admin"}})
					if err != nil {
						t.Fatal(err)
					}
					code := ""
					if state != "open" && state != "created" {
						code = "request-closed"
					} else if !owner {
						code = "not-your-request"
					}
					if code != "" {
						assertRequestErrorCode(t, res, code)
						if f.posts != 0 {
							t.Fatal("write before refusal")
						}
						return
					}
					if res.IsError {
						t.Fatalf("unexpected error: %v", res.Content)
					}
					var out cancelRequestOutput
					b, _ := json.Marshal(res.StructuredContent)
					if err := json.Unmarshal(b, &out); err != nil {
						t.Fatal(err)
					}
					if gate {
						if out.Withdrawal.Outcome != "withdrawn" || !out.Applied || f.posts != 1 || f.reads != 2 {
							t.Fatalf("out=%+v posts=%d reads=%d", out, f.posts, f.reads)
						}
					} else if out.Withdrawal.Outcome != "preview" || !out.DryRun || f.posts != 0 || f.reads != 1 {
						t.Fatalf("bad preview: %+v posts=%d reads=%d", out, f.posts, f.reads)
					}
					if f.body != "" {
						t.Fatalf("cancel body=%q", f.body)
					}
					if f.selfReads != 1 {
						t.Fatalf("self reads=%d", f.selfReads)
					}
					for _, p := range f.principals {
						if p != fxBstone {
							t.Fatalf("principal=%q", p)
						}
					}
				})
			}
		}
	}
}

func assertRequestErrorCode(t *testing.T, res *mcp.CallToolResult, code string) {
	t.Helper()
	b, _ := json.Marshal(res.Meta)
	if !res.IsError || !strings.Contains(string(b), `"code":"`+code+`"`) {
		t.Fatalf("want error %s: %+v meta=%s", code, res, string(b))
	}
}

func TestCancelRequestReadbackAndRefusals(t *testing.T) {
	for _, name := range []string{"open", "closing", "closed", "unreadable", "forbidden", "shared", "invalid"} {
		t.Run(name, func(t *testing.T) {
			f := newRequesterFixture(t)
			oid := fxCaseTwoStep
			switch name {
			case "open", "closing", "closed":
				f.after = mutateCase(t, f.after, func(c map[string]any) { c["state"] = name })
			case "unreadable":
				f.afterStatus = 403
			case "forbidden":
				f.cancelStatus = 403
			case "shared":
				f.personal = true
				f.shared = true
			case "invalid":
				oid = ""
			}
			res, err := f.connect(t).CallTool(context.Background(), &mcp.CallToolParams{Name: "cancel_request", Arguments: map[string]any{"caseOid": oid, "userName": "bstone", "roleName": "db-admin"}})
			if err != nil {
				t.Fatal(err)
			}
			code := map[string]string{"forbidden": "not-authorized", "shared": "shared-credential", "invalid": "invalid-input"}[name]
			if code != "" {
				assertRequestErrorCode(t, res, code)
				if name != "forbidden" && f.posts != 0 {
					t.Fatal("unexpected write")
				}
				return
			}
			var out cancelRequestOutput
			b, _ := json.Marshal(res.StructuredContent)
			_ = json.Unmarshal(b, &out)
			want := "withdrawn"
			if name == "open" || name == "unreadable" {
				want = "unconfirmed"
			}
			if res.IsError || out.Withdrawal.Outcome != want || !out.Applied || f.posts != 1 {
				t.Fatalf("result %+v", out)
			}
		})
	}
}

func TestMyRequestsEnrichmentAndText(t *testing.T) {
	f := newRequesterFixture(t)
	res, err := f.connect(t).CallTool(context.Background(), &mcp.CallToolParams{Name: "list_my_requests", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("%v %v", res, err)
	}
	var out listMyRequestsOutput
	b, _ := json.Marshal(res.StructuredContent)
	_ = json.Unmarshal(b, &out)
	if len(out.Requests) != 1 {
		t.Fatalf("%+v", out)
	}
	c := out.Requests[0]
	if c.Change != "add" || c.RequestedAt == "" || c.Validity == nil || c.Stage == nil || c.Stage.Count != 2 || c.ObjectRef.DisplayName != "Bob Stone" || c.TargetRef.DisplayName != "Database admin" || len(c.WaitingFor) != 1 || c.WaitingFor[0].DisplayName != "Mia Kovac" {
		t.Fatalf("enrichment: %+v", c)
	}
	if f.reads != 0 {
		t.Fatal("list re-read cases instead of enriching search results")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.HasPrefix(text, "bstone has initiated 1 request(s).") || !strings.Contains(text, "case="+fxCaseTwoStep) || !strings.Contains(text, "waitingFor=mkovac") {
		t.Fatalf("text %s", text)
	}
}
