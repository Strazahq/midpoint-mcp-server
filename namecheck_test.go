package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

func TestConfirmName(t *testing.T) {
	for _, tc := range []struct {
		name, given, actual string
		want                string // substring of the error; empty for none
	}{
		{name: "match", given: "bstone", actual: "bstone"},
		{name: "case and spaces", given: " BStone ", actual: "bstone"},
		{name: "hidden and empty", given: "", actual: ""},
		{name: "missing", given: "", actual: "bstone", want: `userName is required: the midPoint name (the unique name attribute, e.g. a login, not the display name) of the user, which is "bstone" here`},
		{name: "display name", given: "Bob Stone", actual: "bstone", want: `refused: userName "Bob Stone" doesn't match the user; its midPoint name is "bstone"`},
		{name: "hidden but given", given: "bstone", actual: "", want: `refused: userName "bstone" can't be confirmed, because midPoint doesn't show you this user's name; leave userName empty`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := confirmName("userName", "user", tc.given, tc.actual)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			var coded *midpoint.CodedError
			if !errors.As(err, &coded) || coded.Code != midpoint.CodeInvalidInput {
				t.Fatalf("error %v is not coded %s", err, midpoint.CodeInvalidInput)
			}
		})
	}
}

// A decision whose names don't match its case is refused before anything is
// written, gate open or closed: the person confirming the call read those names.
func TestDecideWorkItemRefusesWrongNames(t *testing.T) {
	for _, gate := range []bool{true, false} {
		for _, tc := range []struct {
			user, role, want string
		}{
			{"Someone Else", "Superuser", `userName "Someone Else" doesn't match`},
			{"Jane Doe", "Auditor", `roleName "Auditor" doesn't match`},
			{"", "Superuser", "userName is required"},
		} {
			mp := newDecideMidpoint(t)
			cs := connectRequests(t, mp.srv, gate)
			msg := callToolErr(t, cs, "decide_work_item",
				map[string]any{"caseOid": "case-1", "userName": tc.user, "roleName": tc.role, "workItemId": "1", "decision": "approve"})
			if !strings.Contains(msg, tc.want) {
				t.Errorf("gate=%v: refusal %q does not mention %q", gate, msg, tc.want)
			}
			if done := mp.completions(); len(done) != 0 {
				t.Errorf("gate=%v: refused decision still completed: %+v", gate, done)
			}
		}
	}
}

// request_role checks the role's name, and the user's name whenever a user is
// named, before the preview and before the write. This fake serves no user
// read, so a named user can't be confirmed.
func TestRequestRoleRefusesWrongNames(t *testing.T) {
	srv, reqs := mockMidpointCases(t)
	cs := connectRequests(t, srv, true)
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"roleOid": "role-su", "roleName": "Auditor"}, `roleName "Auditor" doesn't match the role; its midPoint name is "Superuser"`},
		{map[string]any{"roleOid": "role-su", "roleName": "Superuser", "userName": "someone"}, `userName "someone" can't be confirmed`},
	} {
		*reqs = nil
		msg := callToolErr(t, cs, "request_role", tc.args)
		if !strings.Contains(msg, tc.want) {
			t.Errorf("refusal %q does not mention %q", msg, tc.want)
		}
		for _, r := range *reqs {
			if r.method != "GET" {
				t.Errorf("refused request still wrote: %s %s", r.method, r.path)
			}
		}
	}
}
