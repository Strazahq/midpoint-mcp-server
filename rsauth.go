package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
	"github.com/strazahq/midpoint-mcp-server/internal/oidcauth"
)

// principalClaimKey is the TokenInfo.Extra key carrying the correlated midPoint
// user OID from the verifier to the request middleware.
const principalClaimKey = "midpointOID"

// bearerVerifier verifies an OAuth bearer token and correlates it to a midPoint
// user. correlationAttribute is the midPoint attribute the token's correlation
// claim is matched against ("" = the default, name). Any failure returns an
// ErrInvalidToken-wrapped error, which the SDK surfaces as a 401. The verify +
// correlation run as the service account (no principal in the context), which is
// exactly the identity that holds #proxy.
func bearerVerifier(authn *oidcauth.Authenticator, client *midpoint.Client, correlationAttribute string) sdkauth.TokenVerifier {
	return func(ctx context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
		claims, err := authn.Verify(ctx, token)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", sdkauth.ErrInvalidToken, err)
		}
		oid, err := client.CorrelateUser(ctx, claims.Subject, claims.CorrelationValue, correlationAttribute)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", sdkauth.ErrInvalidToken, err)
		}
		return &sdkauth.TokenInfo{
			// UserID binds the session to this subject so the transport rejects a
			// different user's token reusing the session (hijack prevention).
			UserID:     claims.Subject,
			Expiration: claims.Expiry,
			Extra:      map[string]any{principalClaimKey: oid},
		}, nil
	}
}

// anonymousDiscoveryMethods are the MCP methods the discovery gate answers
// without a bearer token. They expose the server's static tool surface — names,
// descriptions, input schemas — and nothing from midPoint: not one of them
// reaches the REST client.
//
// "ping" here is the JSON-RPC protocol ping, not this server's ping *tool*.
// The tool is reached through tools/call, which is deliberately absent from
// this set, so the tool's /ws/rest/self lookup stays behind the token.
var anonymousDiscoveryMethods = map[string]bool{
	"initialize":                true,
	"notifications/initialized": true,
	"ping":                      true,
	"tools/list":                true,
}

// maxDiscoveryBody caps how much of an untrusted body the gate will buffer to
// classify it. Discovery requests are tiny; anything larger is not one, so it
// falls through to the bearer requirement instead of being read. An endpoint
// reachable without a token must not let a caller size an allocation.
const maxDiscoveryBody = 64 << 10

// discoveryGate serves the MCP handshake and tool listing to callers with no
// bearer token, while every other method — tools/call above all — goes through
// requireAuth. It is installed only when the operator opts in
// (MIDPOINT_MCP_ANONYMOUS_DISCOVERY) and only in resource-server mode.
//
// The classification has to happen here, at the HTTP layer, rather than in an
// mcp.MethodHandler middleware where the method name is already parsed: the
// SDK's token-info context key is unexported, so RequireBearerToken is the only
// way to get a verified TokenInfo onto a request. Deciding per method means
// deciding before the SDK dispatches.
func discoveryGate(requireAuth func(http.Handler) http.Handler, next http.Handler) http.Handler {
	authed := requireAuth(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A caller presenting a token is verified whatever it is asking for.
		// Verification is what binds the MCP session to a user ID and puts the
		// correlated principal in the context; letting a token-bearing
		// initialize skip it would hand back a session with nobody bound to it,
		// which is exactly the session-hijack case the SDK guards against.
		if r.Header.Get("Authorization") != "" {
			authed.ServeHTTP(w, r)
			return
		}
		// GET (open the stream) and DELETE (tear the session down) carry no
		// method to classify. They act only on an existing session, and the SDK
		// refuses one created with a bound user unless that user's token is
		// present — so an anonymous caller can only ever reach a session that
		// was itself anonymous, which has no tool access without a token.
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxDiscoveryBody+1))
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		// Unreadable, oversized, or not discovery-only: fail closed. requireAuth
		// rejects it for the missing token rather than this gate guessing why.
		if err != nil || len(body) > maxDiscoveryBody || !discoveryOnly(body) {
			authed.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// discoveryOnly reports whether every JSON-RPC message in body names a method
// in anonymousDiscoveryMethods. A batch qualifies only when all of its members
// do: one tools/call among ten discovery calls makes the whole request
// privileged. Anything that does not parse is not discovery.
func discoveryOnly(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return false
	}
	type rpcMessage struct {
		Method string `json:"method"`
	}
	if trimmed[0] == '[' {
		var batch []rpcMessage
		if err := json.Unmarshal(trimmed, &batch); err != nil || len(batch) == 0 {
			return false
		}
		for _, m := range batch {
			if !anonymousDiscoveryMethods[m.Method] {
				return false
			}
		}
		return true
	}
	var single rpcMessage
	if err := json.Unmarshal(trimmed, &single); err != nil {
		return false
	}
	return anonymousDiscoveryMethods[single.Method]
}

// principalMiddleware copies the correlated midPoint OID from the per-request
// TokenInfo into the context, so the client executes the request as that user
// (Switch-To-Principal). It is a no-op with no token, keeping personal mode
// unchanged.
func principalMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if extra := req.GetExtra(); extra != nil && extra.TokenInfo != nil {
			if oid, ok := extra.TokenInfo.Extra[principalClaimKey].(string); ok {
				ctx = midpoint.WithPrincipal(ctx, oid)
			}
		}
		return next(ctx, method, req)
	}
}
