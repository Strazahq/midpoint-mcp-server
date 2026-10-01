package main

import (
	"context"
	"maps"
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
	return p
}
