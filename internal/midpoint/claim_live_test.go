//go:build integration

// Live check of work items offered to a group (docs/ui-contract.md Q4)
// against a real midPoint. Compiled only under -tags=integration; it skips
// unless the environment names a midPoint and a person with an open work
// item offered to one of their groups. It WRITES: it claims that item,
// checks it can be decided, releases it and checks it is offered again, so
// the item ends as it started.
//
// Environment:
//
//	MIDPOINT_URL, MIDPOINT_USERNAME, MIDPOINT_PASSWORD  # the #proxy service account,
//	                                                     # with rest-3#claimWorkItem and rest-3#releaseWorkItem
//	MIDPOINT_IT_GROUP_APPROVER_OID  # the person to act as (Switch-To-Principal)
//
// Example:
//
//	MIDPOINT_URL=http://localhost:8080/midpoint MIDPOINT_USERNAME=svc-mcp MIDPOINT_PASSWORD=… \
//	MIDPOINT_IT_GROUP_APPROVER_OID=… go test -tags=integration ./internal/midpoint -run LiveClaim -v
package midpoint

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveClaimAndRelease(t *testing.T) {
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Skipf("skipping live claim test: %v", err)
	}
	who := strings.TrimSpace(os.Getenv("MIDPOINT_IT_GROUP_APPROVER_OID"))
	if who == "" {
		t.Skip("skipping live claim test: MIDPOINT_IT_GROUP_APPROVER_OID is not set")
	}
	c := NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	as := func() context.Context { return WithSelfMemo(WithPrincipal(ctx, who)) }

	res, err := c.ListWorkItems(as(), 50)
	if err != nil {
		t.Fatalf("ListWorkItems: %v", err)
	}
	var offered *InboxWorkItem
	for i, wi := range res.WorkItems {
		t.Logf("case %s item %s: offered=%v claimed=%v offeredTo=%+v reason=%s", wi.CaseOID, wi.ID, wi.Offered, wi.Claimed, wi.OfferedTo, wi.Context.Reason)
		if wi.Offered && offered == nil {
			offered = &res.WorkItems[i]
		}
	}
	if offered == nil {
		t.Skipf("%s has no work item offered to a group", res.Subject.Name)
	}
	caseOID, id := offered.CaseOID, offered.ID
	if offered.OfferedTo == nil || offered.Context.Reason != ReasonGroup || offered.Assignee != "" {
		t.Errorf("offered item = %+v", offered)
	}

	if _, err := c.CheckDecidable(as(), caseOID, id); !hasCode(err, CodeNotClaimed) {
		t.Errorf("CheckDecidable before the claim: %v, want not-claimed", err)
	}
	if _, err := c.CheckClaimable(as(), caseOID, id); err != nil {
		t.Fatalf("CheckClaimable: %v", err)
	}
	plan, _ := c.PlanClaimWorkItem(caseOID, id)
	if _, err := c.Apply(as(), plan); err != nil {
		t.Fatalf("claim: %v", err)
	}
	h, err := c.ReadWorkItemHolders(as(), caseOID, id)
	if err != nil || !h.Mine || len(h.Assignees) != 1 {
		t.Errorf("after the claim: %+v, %v", h, err)
	}
	if _, err := c.CheckDecidable(as(), caseOID, id); err != nil {
		t.Errorf("CheckDecidable after the claim: %v", err)
	}
	if _, err := c.CheckClaimable(as(), caseOID, id); !hasCode(err, CodeInvalidInput) {
		t.Errorf("CheckClaimable after the claim: %v, want invalid-input (already yours)", err)
	}
	listed, err := c.ListWorkItems(as(), 50)
	if err != nil {
		t.Fatalf("ListWorkItems after the claim: %v", err)
	}
	for _, wi := range listed.WorkItems {
		if wi.CaseOID == caseOID && wi.ID == id && (!wi.Claimed || wi.Offered) {
			t.Errorf("listed after the claim: %+v", wi)
		}
	}

	if _, err := c.CheckReleasable(as(), caseOID, id); err != nil {
		t.Fatalf("CheckReleasable: %v", err)
	}
	plan, _ = c.PlanReleaseWorkItem(caseOID, id)
	if _, err := c.Apply(as(), plan); err != nil {
		t.Fatalf("release: %v", err)
	}
	h, err = c.ReadWorkItemHolders(as(), caseOID, id)
	if err != nil || (h.Visible && len(h.Assignees) != 0) {
		t.Errorf("after the release: %+v, %v", h, err)
	}
	t.Logf("after the release: visible=%v open=%v assignees=%d", h.Visible, h.Open, len(h.Assignees))
}

func hasCode(err error, code string) bool {
	c, _ := ErrorCode(err)
	return c == code
}
