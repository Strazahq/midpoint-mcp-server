package main

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

// Stable error codes on tool results (docs/ui-contract.md 6.8, S18).

// errorMetaKey names the error payload in an error result's _meta.
const errorMetaKey = "midpoint-mcp-server/error"

// sdkValidationPrefix starts the text of the error the SDK returns, before any
// handler runs, for arguments that fail the tool's input schema.
const sdkValidationPrefix = `validating "arguments"`

// errorPayload is the value under errorMetaKey.
type errorPayload struct {
	V     int    `json:"v"`
	Code  string `json:"code"`
	Field string `json:"field,omitempty"`
	// Reason is midPoint's message for people about a refusal (D42): a
	// policy rule's own text, for example. Untrusted text from midPoint.
	Reason string `json:"reason,omitempty"`
}

// errorCodes is the receiving middleware that codes every tool error result.
//
// Tool handlers return typed errors (midpoint.CodedError, midpoint.StatusError)
// and the SDK turns a returned error into the result, so the text is exactly
// what it was. Building the result in the handler instead would not keep the
// shape: with a typed output the SDK adds the zero output as structuredContent
// to any result a handler returns without an error. The SDK keeps the error
// on the result (GetError), and the code is read from it here. An error the
// SDK raised itself is invalid-input when the arguments failed validation;
// any other error without a code is internal. JSON-RPC errors, such as an
// unknown tool, are not tool results and pass unchanged.
func errorCodes(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		res, err := next(ctx, method, req)
		r, ok := res.(*mcp.CallToolResult)
		if err != nil || !ok || r == nil || !r.IsError {
			return res, err
		}
		if _, coded := r.Meta[errorMetaKey]; coded {
			return res, nil
		}
		out := *r
		out.Meta = maps.Clone(r.Meta)
		if out.Meta == nil {
			out.Meta = mcp.Meta{}
		}
		out.Meta[errorMetaKey] = classifyError(r.GetError())
		out.Content = withMidpointSaid(r.Content, r.GetError())
		return &out, nil
	}
}

// classifyError returns the error payload for a tool error.
func classifyError(err error) errorPayload {
	p := errorPayload{V: 1, Code: midpoint.CodeInternal}
	if code, field := midpoint.ErrorCode(err); code != "" {
		p.Code, p.Field = code, field
	} else if err != nil && strings.HasPrefix(err.Error(), sdkValidationPrefix) {
		p.Code = midpoint.CodeInvalidInput
	}
	p.Reason, _ = midpoint.MidpointSaid(err)
	return p
}

// withMidpointSaid adds what midPoint said about a refusal under the error's
// text, as an untrusted line (D42): its reason for people when it gave one,
// else its technical message. The content is copied, not changed in place.
func withMidpointSaid(content []mcp.Content, err error) []mcp.Content {
	reason, message := midpoint.MidpointSaid(err)
	said := cmp.Or(reason, message)
	if said == "" || len(content) == 0 {
		return content
	}
	tc, ok := content[0].(*mcp.TextContent)
	if !ok {
		return content
	}
	t := newListText(tc.Text)
	t.untrusted(fieldAnswer, fromMidpointAnswer, said)
	out := slices.Clone(content)
	text := *tc
	text.Text = t.String()
	out[0] = &text
	return out
}
