package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestEmbeddedMyRequests(t *testing.T) {
	client, cfg := offlineClient()
	ui := connectSession(t, newMCPServerWithViews(client, cfg, embeddedViews()), true)
	plain := connectSession(t, newMCPServerWithViews(client, cfg, embeddedViews()), false)
	const uri = "ui://midpoint/my-requests"
	var document string
	for _, cs := range []*mcp.ClientSession{ui, plain} {
		res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Contents) != 1 || res.Contents[0].MIMEType != viewMIMEType {
			t.Fatalf("resource %+v", res)
		}
		if document != "" && res.Contents[0].Text != document {
			t.Fatal("document differs between callers")
		}
		document = res.Contents[0].Text
	}
	if !strings.Contains(document, "midpoint-my-requests") {
		t.Fatal("wrong view")
	}
	res, err := ui.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	linked := map[string]bool{"list_my_requests": false, "get_case": false, "cancel_request": false}
	for _, tool := range res.Tools {
		if _, ok := linked[tool.Name]; !ok {
			continue
		}
		b, _ := json.Marshal(tool.Meta)
		if !strings.Contains(string(b), uri) {
			t.Fatalf("%s has no view link", tool.Name)
		}
		linked[tool.Name] = true
		if tool.Name == "cancel_request" {
			b, _ := json.Marshal(tool.InputSchema)
			var schema struct {
				Properties map[string]any `json:"properties"`
				Required   []string       `json:"required"`
			}
			if err := json.Unmarshal(b, &schema); err != nil {
				t.Fatal(err)
			}
			if len(schema.Properties) != 1 || schema.Properties["caseOid"] == nil || len(schema.Required) != 1 || schema.Required[0] != "caseOid" {
				t.Fatalf("cancel schema %s", b)
			}
		}
	}
	for name, seen := range linked {
		if !seen {
			t.Errorf("missing %s", name)
		}
	}
}
