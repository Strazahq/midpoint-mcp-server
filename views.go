package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

// MCP Apps views (docs/ui-contract.md). A view is a self-contained HTML
// document the host renders in a sandboxed iframe; it gets its data from the
// results of the tools linked to it and calls tools back through the host.

// uiContractVersion is the version of docs/ui-contract.md this server
// implements, reported to views as server.uiContract.
const uiContractVersion = "1.0-draft.9"

const (
	// uiExtension is the client capability extension a host advertises when it
	// renders MCP Apps views.
	uiExtension = "io.modelcontextprotocol/ui"
	// viewMIMEType is the MIME type of every view document.
	viewMIMEType = "text/html;profile=mcp-app"
	// viewURIPrefix starts every view's resource URI.
	viewURIPrefix = "ui://midpoint/"
)

// viewFiles holds the view documents, embedded at build time so every caller
// gets the same bytes (contract 3.2).
//
//go:embed views
var viewFiles embed.FS

// viewDef is one view of contract 3.2.
type viewDef struct {
	URI         string
	Name        string
	Description string
	File        string // path inside the view directory
}

// viewCatalog lists the four views. A view is served only once its document
// is in the view directory; until then its tools carry no resourceUri.
var viewCatalog = []viewDef{
	{"ui://midpoint/approval-inbox", "approval_inbox",
		"Open approval work items assigned to you, with context, approve and reject.", "approval-inbox.html"},
	{"ui://midpoint/request-access", "request_access",
		"Requestable roles for you or a direct report, and a request button.", "request-access.html"},
	{"ui://midpoint/my-requests", "my_requests",
		"Approval cases you started, where each one stands, and withdrawing one.", "my-requests.html"},
	{"ui://midpoint/access-review", "access_review",
		"Your team and each person's access; managers can remove a direct report's role.", "access-review.html"},
}

// toolViews links each tool a view renders to that view (contract 3.3).
var toolViews = map[string]string{
	"list_work_items":        "ui://midpoint/approval-inbox",
	"decide_work_item":       "ui://midpoint/approval-inbox",
	"list_requestable_roles": "ui://midpoint/request-access",
	"request_role":           "ui://midpoint/request-access",
	"list_my_requests":       "ui://midpoint/my-requests",
	"get_case":               "ui://midpoint/my-requests",
	"cancel_request":         "ui://midpoint/my-requests",
	"get_user_assignments":   "ui://midpoint/access-review",
	"unassign_role":          "ui://midpoint/access-review",
	"list_my_team":           "ui://midpoint/access-review",
}

// viewCalledTools are the tools views call that render no view of their own.
// Every tool that is neither here nor in toolViews is model-only in UI
// sessions, so a host refuses a view that tries to call it.
var viewCalledTools = map[string]bool{
	"list_my_managers": true,
	"whoami":           true,
}

// views is the set of views this build serves.
type views struct {
	files fs.FS
	byURI map[string]viewDef
}

// loadViews returns the catalog's views whose documents exist in files.
func loadViews(files fs.FS) views {
	v := views{files: files, byURI: map[string]viewDef{}}
	for _, d := range viewCatalog {
		if _, err := fs.Stat(files, d.File); err == nil {
			v.byURI[d.URI] = d
		}
	}
	return v
}

// embeddedViews returns the views compiled into this binary.
func embeddedViews() views {
	sub, err := fs.Sub(viewFiles, "views")
	if err != nil {
		panic(err) // the directive above guarantees the directory
	}
	return loadViews(sub)
}

// viewResourceMeta is the _meta of every view resource (contract 3.2): the host
// draws the frame. csp, permissions and domain are omitted on purpose, which
// gives the strictest sandbox the specification defines.
func viewResourceMeta() mcp.Meta {
	return mcp.Meta{"ui": map[string]any{"prefersBorder": true}}
}

// install registers the view resources and the middleware that shows UI
// sessions what other sessions don't see.
func (v views) install(server *mcp.Server) {
	for _, d := range viewCatalog {
		if _, ok := v.byURI[d.URI]; !ok {
			continue
		}
		server.AddResource(&mcp.Resource{
			URI:         d.URI,
			Name:        d.Name,
			Description: d.Description,
			MIMEType:    viewMIMEType,
			Meta:        viewResourceMeta(),
		}, v.read)
	}
	server.AddReceivingMiddleware(v.middleware)
}

// read serves a view document. Reads are answered in every session: the
// document is static and the same for everyone.
func (v views) read(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	d, ok := v.byURI[req.Params.URI]
	if !ok {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	doc, err := fs.ReadFile(v.files, d.File)
	if err != nil {
		return nil, fmt.Errorf("reading view %s: %w", d.URI, err)
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
		URI:      d.URI,
		MIMEType: viewMIMEType,
		Text:     string(doc),
		Meta:     viewResourceMeta(),
	}}}, nil
}

// middleware adapts tools/list and resources/list to the session. A UI session
// gets the tool linkage; any other session gets exactly what it got before
// views existed.
func (v views) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		res, err := next(ctx, method, req)
		if err != nil {
			return res, err
		}
		ui := isUISession(req.GetSession())
		switch r := res.(type) {
		case *mcp.ListToolsResult:
			if ui {
				return v.linkTools(r), nil
			}
		case *mcp.ListResourcesResult:
			if !ui {
				return withoutViews(r), nil
			}
		}
		return res, nil
	}
}

// isUISession reports whether the session's client renders MCP Apps views: its
// initialize named the UI extension with the view MIME type (contract 3.1).
func isUISession(s mcp.Session) bool {
	ss, ok := s.(*mcp.ServerSession)
	return ok && rendersViews(ss.InitializeParams())
}

// rendersViews reports whether an initialize request advertised MCP Apps
// support with the view MIME type.
func rendersViews(params *mcp.InitializeParams) bool {
	if params == nil || params.Capabilities == nil {
		return false
	}
	ext, ok := params.Capabilities.Extensions[uiExtension].(map[string]any)
	if !ok {
		return false
	}
	// Decoded from JSON the list is []any; a client in the same process may
	// hand over a []string.
	switch types := ext["mimeTypes"].(type) {
	case []any:
		return slices.Contains(types, any(viewMIMEType))
	case []string:
		return slices.Contains(types, viewMIMEType)
	}
	return false
}

// linkTools returns a copy of a tools/list result with each tool's _meta.ui:
// the view that renders it, or model-only visibility for tools no view calls.
// The server's own tool definitions are shared by every session, so they are
// copied, never changed.
func (v views) linkTools(r *mcp.ListToolsResult) *mcp.ListToolsResult {
	out := *r
	out.Tools = make([]*mcp.Tool, len(r.Tools))
	for i, t := range r.Tools {
		ui := map[string]any{}
		if uri, ok := toolViews[t.Name]; ok {
			if _, served := v.byURI[uri]; served {
				ui["resourceUri"] = uri
			}
		} else if !viewCalledTools[t.Name] {
			ui["visibility"] = []string{"model"}
		}
		if len(ui) == 0 {
			out.Tools[i] = t
			continue
		}
		tc := *t
		tc.Meta = maps.Clone(t.Meta)
		if tc.Meta == nil {
			tc.Meta = mcp.Meta{}
		}
		tc.Meta["ui"] = ui
		out.Tools[i] = &tc
	}
	return &out
}

// withoutViews returns a resources/list result without the view resources,
// which only a UI session can use.
func withoutViews(r *mcp.ListResourcesResult) *mcp.ListResourcesResult {
	out := *r
	out.Resources = []*mcp.Resource{}
	for _, res := range r.Resources {
		if !strings.HasPrefix(res.URI, viewURIPrefix) {
			out.Resources = append(out.Resources, res)
		}
	}
	return &out
}

// --- fields every view-bearing result carries (contract 4.2) ---

// serverInfo is a result's server block (contract 4.3).
type serverInfo struct {
	WritesEnabled      bool   `json:"writesEnabled" jsonschema:"false when writes return a dry-run preview"`
	RequireRequestable bool   `json:"requireRequestable" jsonschema:"request_role refuses roles not flagged requestable"`
	RequestReason      bool   `json:"requestReason" jsonschema:"requests can carry a reason (requests.justificationItem is set)"`
	UIContract         string `json:"uiContract" jsonschema:"version of the views contract this server implements"`
	Version            string `json:"version" jsonschema:"server version"`
}

// newServerInfo describes this deployment to views.
func newServerInfo(cfg midpoint.Config) serverInfo {
	_, reason := cfg.File.Requests.Justification()
	return serverInfo{
		WritesEnabled:      cfg.AllowWrites,
		RequireRequestable: cfg.File.Requests.RequestableRequired(),
		RequestReason:      reason,
		UIContract:         uiContractVersion,
		Version:            version,
	}
}

// viewFields lead the result of every tool a view renders or calls.
type viewFields struct {
	Tool   string                  `json:"tool" jsonschema:"the tool that produced this result"`
	Acting midpoint.ActingIdentity `json:"acting" jsonschema:"who midPoint executed this call as"`
	Server serverInfo              `json:"server" jsonschema:"write gate, features and versions of this server"`
}

func (f *viewFields) setViewFields(v viewFields) { *f = v }

// viewOutput is a tool output that embeds viewFields.
type viewOutput[T any] interface {
	*T
	setViewFields(viewFields)
}

// viewTool wraps the handler of a tool views render or call so its result
// carries viewFields. The acting identity is resolved before the handler runs,
// so a write never happens without it, and the handler's own self lookups
// share that one read.
func viewTool[In, Out any, P viewOutput[Out]](name string, client *midpoint.Client, info serverInfo,
	h mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		ctx = midpoint.WithSelfMemo(ctx)
		acting, err := client.Acting(ctx)
		if err != nil {
			var zero Out
			return nil, zero, fmt.Errorf("resolving the acting identity: %w", err)
		}
		res, out, err := h(ctx, req, in)
		if err != nil {
			return res, out, err
		}
		P(&out).setViewFields(viewFields{Tool: name, Acting: acting, Server: info})
		return res, out, nil
	}
}

// --- readable chat (contract S24) ---

// serverInstructions go to the client in the initialize result.
const serverInstructions = "When you write to a person, name people, roles and requests by their display " +
	"names. OIDs are identifiers for tool calls; mention one only when the person asks or when two objects " +
	"would otherwise be confused."

// readableChatNote closes the description of every tool whose text carries
// OIDs, which is all of them.
const readableChatNote = " When you write to a person, name people, roles and requests by their display names " +
	"rather than OIDs."

// addTool registers a tool with the readable-chat note on its description.
func addTool[In, Out any](server *mcp.Server, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	t.Description += readableChatNote
	mcp.AddTool(server, t, h)
}
