//go:build integration

// Live check of the approval inbox data (docs/ui-contract.md 7.1, 7.3) against
// a real midPoint. Compiled only under -tags=integration; it skips unless the
// environment names a midPoint and an approver with at least one open work
// item. It only reads.
//
// Environment:
//
//	MIDPOINT_URL, MIDPOINT_USERNAME, MIDPOINT_PASSWORD  # the #proxy service account
//	MIDPOINT_IT_APPROVER_OID   # the person to act as (Switch-To-Principal)
//	MIDPOINT_MCP_CONFIG        # optional; with requests.justificationItem set,
//	                           # the justification is checked too
//	MIDPOINT_IT_CASE_OID       # optional: a case to read back as the approver,
//	                           # e.g. right after they decided it, to see who
//	                           # decide_work_item would report as next
//
// Example:
//
//	MIDPOINT_URL=http://localhost:8080/midpoint MIDPOINT_USERNAME=svc-mcp MIDPOINT_PASSWORD=… \
//	MIDPOINT_IT_APPROVER_OID=… go test -tags=integration ./internal/midpoint -run LiveInbox -v
package midpoint

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveInboxData(t *testing.T) {
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Skipf("skipping live inbox test: %v", err)
	}
	approver := strings.TrimSpace(os.Getenv("MIDPOINT_IT_APPROVER_OID"))
	if approver == "" {
		t.Skip("skipping live inbox test: MIDPOINT_IT_APPROVER_OID is not set")
	}
	c := NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ctx = WithSelfMemo(WithPrincipal(ctx, approver))

	if caseOID := strings.TrimSpace(os.Getenv("MIDPOINT_IT_CASE_OID")); caseOID != "" {
		d, err := c.GetCase(ctx, caseOID)
		if err != nil {
			t.Fatalf("GetCase(%s): %v", caseOID, err)
		}
		for _, n := range d.NextApprovers {
			t.Logf("case %s (%s, step %+v): next approver %s %q readable=%v",
				d.OID, d.State, d.Stage, n.OID, n.DisplayName, n.Readable)
		}
		if len(d.NextApprovers) == 0 {
			t.Logf("case %s (%s): no next approver", d.OID, d.State)
		}
	}

	res, err := c.ListWorkItems(ctx, 50)
	if err != nil {
		t.Fatalf("ListWorkItems: %v", err)
	}
	if len(res.WorkItems) == 0 {
		t.Skipf("%s has no open work item", res.Subject.Name)
	}
	_, wantReason := cfg.File.Requests.Justification()
	for _, wi := range res.WorkItems {
		wc := wi.Context
		t.Logf("case %s item %s: change=%s stage=%+v reason=%s requestee=%s/%v target=%s/%s requested=%s deadline=%s "+
			"validity=%+v justification=%t coAssignees=%d stageApprovers=%d access=%t/%d",
			wi.CaseOID, wi.ID, wc.Change, wc.Stage, wc.Reason, wc.Requestee.DisplayName, wc.Requestee.Readable,
			wc.Target.DisplayName, wc.Target.RiskLevel, wc.RequestedAt, wc.Deadline, wc.Validity,
			wc.Justification != "", len(wc.CoAssignees), len(wc.StageApprovers),
			wc.RequesteeAccess.Visible, len(wc.RequesteeAccess.Roles))
		if wc.Change == ChangeUnknown {
			t.Errorf("item %s: change unknown; approvalContext missing from the search result?", wi.ID)
		}
		if wc.RequestedAt == "" || wc.CreatedAt == "" || wc.Stage.Number == 0 || wc.Stage.Count == 0 {
			t.Errorf("item %s: requestedAt=%q createdAt=%q stage=%+v", wi.ID, wc.RequestedAt, wc.CreatedAt, wc.Stage)
		}
		for _, r := range append(wc.CoAssignees, wc.StageApprovers...) {
			if r.OID == res.Subject.OID {
				t.Errorf("item %s: the acting identity is among the other approvers", wi.ID)
			}
		}
		if wantReason && wc.Change == ChangeAdd && wc.Justification == "" {
			t.Logf("item %s: no justification (none given, or the item is not on the request)", wi.ID)
		}
	}

	d, err := c.GetCase(ctx, res.WorkItems[0].CaseOID)
	if err != nil {
		t.Fatalf("GetCase: %v", err)
	}
	t.Logf("case %s: change=%s requested=%s stages=%+v stage=%+v items=%d next=%d",
		d.OID, d.Change, d.RequestedAt, d.Stages, d.Stage, len(d.WorkItems), len(d.NextApprovers))
	if len(d.Stages) == 0 || d.Stage == nil || d.RequestedAt == "" {
		t.Errorf("case %s: stages=%+v stage=%+v requestedAt=%q", d.OID, d.Stages, d.Stage, d.RequestedAt)
	}
	for _, wi := range d.WorkItems {
		if len(wi.Assignees) == 0 {
			t.Errorf("case %s item %s: no assignees", d.OID, wi.ID)
		}
	}
}
