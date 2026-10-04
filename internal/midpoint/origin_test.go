package midpoint

import (
	"context"
	"testing"
)

// The value metadata shape is the one midPoint 4.10.3 returned live on a plain
// GET of a user after an approved request: storage and process blocks, one
// value metadata, refs without targetName.
func TestAssignmentOrigin(t *testing.T) {
	const (
		oidRequester = "e5e5e5e5-0000-4000-8000-000000000001"
		oidApprover  = "e5e5e5e5-0000-4000-8000-000000000002"
	)
	user := `{"user":{"oid":"` + oidBstone + `","name":"bstone","assignment":[
		{"@id":1,"targetRef":{"oid":"role-a","type":"c:RoleType"},
		 "@metadata":{"storage":{"createTimestamp":"2026-10-03T15:28:48.604Z","creatorRef":{"oid":"` + oidRequester + `","type":"c:UserType"}},
		  "process":{"requestTimestamp":"2026-10-03T15:28:31.856Z","requestorRef":{"oid":"` + oidRequester + `","type":"c:UserType"},
		   "createApproverRef":{"oid":"` + oidApprover + `","type":"c:UserType"},"createApprovalComment":"quarter-end close",
		   "requestorComment":" typed at checkout "}}},
		{"@id":2,"targetRef":{"oid":"role-b","type":"c:RoleType"},
		 "metadata":{"createTimestamp":"2024-02-02T10:00:00Z","creatorRef":{"oid":"` + oidApprover + `","type":"c:UserType"},
		  "createApproverRef":[{"oid":"` + oidApprover + `","type":"c:UserType"},{"oid":"` + oidRequester + `","type":"c:UserType"}],
		  "createApprovalComment":["ok","  "]}},
		{"@id":3,"targetRef":{"oid":"role-c","type":"c:RoleType"}}]}}`
	mp := &fakeMidpoint{objects: map[string]string{
		"/users/" + oidBstone:    user,
		"/users/" + oidRequester: `{"user":{"oid":"` + oidRequester + `","name":"bstone","fullName":"Bob Stone"}}`,
		"/users/" + oidApprover:  `{"user":{"oid":"` + oidApprover + `","name":"jdoe","fullName":"Jane Doe"}}`,
	}}
	res, err := mp.client(t, Config{}).GetUserAssignments(context.Background(), oidBstone)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Assignments) != 3 {
		t.Fatalf("assignments = %d", len(res.Assignments))
	}

	o := res.Assignments[0].Origin
	if o == nil || o.CreatedAt != "2026-10-03T15:28:48.604Z" || o.RequestedAt != "2026-10-03T15:28:31.856Z" {
		t.Fatalf("value metadata origin = %+v", o)
	}
	if o.RequestedBy == nil || o.RequestedBy.DisplayName != "Bob Stone" || o.CreatedBy == nil || o.CreatedBy.Name != "bstone" {
		t.Errorf("requester = %+v, creator = %+v", o.RequestedBy, o.CreatedBy)
	}
	if len(o.ApprovedBy) != 1 || o.ApprovedBy[0].DisplayName != "Jane Doe" || len(o.ApprovalComments) != 1 || o.ApprovalComments[0] != "quarter-end close" {
		t.Errorf("approval = %+v %q", o.ApprovedBy, o.ApprovalComments)
	}
	if o.RequestComment != "typed at checkout" {
		t.Errorf("request comment = %q", o.RequestComment)
	}

	// midPoint before 4.10: one flat metadata container, multi-valued approvers.
	o = res.Assignments[1].Origin
	if o == nil || o.CreatedAt != "2024-02-02T10:00:00Z" || o.RequestedBy != nil || len(o.ApprovedBy) != 2 || len(o.ApprovalComments) != 1 {
		t.Errorf("container origin = %+v", o)
	}

	if res.Assignments[2].Origin != nil {
		t.Errorf("origin without metadata = %+v", res.Assignments[2].Origin)
	}
	if n := mp.readsOf("/users/" + oidApprover); n != 1 {
		t.Errorf("approver read %d times", n)
	}
}
