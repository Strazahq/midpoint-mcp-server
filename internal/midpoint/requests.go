package midpoint

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// RequestSummary is a request with the context needed to track its progress.
type RequestSummary struct {
	CaseSummary
	ObjectRef   *ObjectRef  `json:"objectRef,omitempty"`
	TargetRef   *ObjectRef  `json:"targetRef,omitempty"`
	Change      string      `json:"change"`
	RequestedAt string      `json:"requestedAt,omitempty"`
	ClosedAt    string      `json:"closedAt,omitempty"`
	Stage       *StageInfo  `json:"stage,omitempty"`
	WaitingFor  []ObjectRef `json:"waitingFor"`
	Validity    *Validity   `json:"validity,omitempty"`
}

// requestSummary reuses case enrichment and names only the open items' assignees.
func (c *Client) requestSummary(ctx context.Context, cj caseJSON, reader *refReader) RequestSummary {
	d := c.caseDetailWithReader(ctx, cj, reader)
	waiting := []ObjectRef{}
	seen := map[string]bool{}
	if cj.State == "created" || cj.State == "open" {
		for _, wi := range cj.items() {
			if !wi.open() {
				continue
			}
			for _, r := range reader.objectRefs(ctx, wi.assignees(), nil) {
				if !seen[r.OID] {
					waiting = append(waiting, r)
					seen[r.OID] = true
				}
			}
		}
	}
	return RequestSummary{CaseSummary: d.CaseSummary, ObjectRef: d.ObjectRef, TargetRef: d.TargetRef,
		Change: d.Change, RequestedAt: d.RequestedAt, ClosedAt: d.ClosedAt, Stage: d.Stage, WaitingFor: waiting, Validity: d.Validity}
}

// WithdrawableRequest is a case confirmed to be open and requested by the caller.
type WithdrawableRequest struct {
	Subject Subject
	Case    CaseDetail
}

// CheckWithdrawable reads as the caller and checks ownership and state before any write.
// Authorization to cancel is left to midPoint when the plan is applied.
func (c *Client) CheckWithdrawable(ctx context.Context, caseOID string) (WithdrawableRequest, error) {
	if err := requireOID(caseOID); err != nil {
		return WithdrawableRequest{}, &CodedError{Code: CodeInvalidInput, Err: fmt.Errorf("case %w", err)}
	}
	subj, err := c.subject(ctx)
	if err != nil {
		return WithdrawableRequest{}, err
	}
	var cj caseJSON
	if err := c.getObject(ctx, collCases, caseOID, true, &cj); err != nil {
		return WithdrawableRequest{}, err
	}
	label := caseLabel(cj.summary())
	if cj.State != "created" && cj.State != "open" {
		return WithdrawableRequest{}, &CodedError{Code: CodeRequestClosed, Err: fmt.Errorf("refused: case %s is %s, not open, so there is nothing to withdraw", label, orUnknown(cj.State))}
	}
	if cj.RequestorRef == nil || cj.RequestorRef.OID != subj.OID {
		return WithdrawableRequest{}, &CodedError{Code: CodeNotYourRequest, Err: fmt.Errorf("refused: case %s was requested by %s, not by %s; only the requester can withdraw a request", label, orUnknown(refName(cj.RequestorRef)), subj.Name)}
	}
	return WithdrawableRequest{Subject: subj, Case: c.caseDetail(ctx, cj)}, nil
}

// PlanCancelRequest builds the bodyless REST cancellation of an approval case.
func (c *Client) PlanCancelRequest(caseOID string) (Plan, error) {
	if err := requireOID(caseOID); err != nil {
		return Plan{}, &CodedError{Code: CodeInvalidInput, Err: fmt.Errorf("case %w", err)}
	}
	return Plan{Method: http.MethodPost, Path: "/cases/" + url.PathEscape(caseOID) + "/cancel", Summary: "Withdraw request " + caseOID}, nil
}
