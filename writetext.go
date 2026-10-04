package main

import (
	"fmt"
	"strings"

	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

// A write's text says what it does in names a person recognises, for example
// "Would assign role End user to Carol Jensen (carol)". The OIDs follow on a
// "Request:" line, for agents and audits. The person approving the call on a
// phone reads the first line, not a 36-character OID.

// personLabel names a person: "Carol Jensen (carol)", or the one name known.
func personLabel(r midpoint.ObjectRef) string {
	switch {
	case r.DisplayName != "" && r.Name != "" && r.DisplayName != r.Name:
		return fmt.Sprintf("%s (%s)", r.DisplayName, r.Name)
	case r.DisplayName != "":
		return r.DisplayName
	case r.Name != "":
		return r.Name
	}
	return "a person you can't see in midPoint"
}

// thingLabel names a role, org, service, task or resource by its display
// name, else its midPoint name.
func thingLabel(r midpoint.ObjectRef) string {
	switch {
	case r.DisplayName != "":
		return r.DisplayName
	case r.Name != "":
		return r.Name
	}
	return "an item you can't see in midPoint"
}

// requestLine is the technical line under a write's sentence.
func requestLine(plan midpoint.Plan) string {
	return fmt.Sprintf("Request: %s %s", plan.Method, plan.Endpoint())
}

// lowerFirst lets a summary ("Assign role …") follow "Would".
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// derefRef reads an optional reference, empty when absent.
func derefRef(r *midpoint.ObjectRef) midpoint.ObjectRef {
	if r == nil {
		return midpoint.ObjectRef{}
	}
	return *r
}
