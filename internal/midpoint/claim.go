package midpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Work items offered to a group (docs/ui-contract.md Q4). When an approval
// step names a group (a role, org or service) as its approver, midPoint's
// default groupExpansion, byClaimingWorkItem, creates one work item with the
// group in candidateRef and no assignee. A member claims it, which makes them
// its only assignee; releasing it clears the assignee, and the group's members
// can claim it again. Live on 4.10.3, through plain REST as the person
// (Switch-To-Principal):
//
//   - POST /cases/{oid}/workItems/{id}/claim and .../release take no body and
//     answer 204. The REST account needs rest-3#claimWorkItem and
//     rest-3#releaseWorkItem (403 without); the person needs no model
//     authorization to claim: midPoint checks only that a candidateRef is the
//     person or one of their roleMembershipRef targets (403 "You are not
//     authorized to claim the selected work item" otherwise).
//   - Claiming an item someone holds answers 500 ("already assigned to
//     another user" or "to the current user"); releasing an item the person
//     doesn't hold answers 500 too.
//   - Claiming or releasing a closed item, and releasing an item assigned
//     directly (no candidateRef), answer 204 and change nothing. So both are
//     checked here before anything is written, as decide_work_item does.
//   - The claimed item keeps candidateRef next to its new assigneeRef, and
//     the stock Approver role lets its holder read and complete it once it is
//     theirs. Before the claim the stock roles don't show the item at all.

// candidateSet holds the OIDs a work item's candidateRef may name for the
// item to be offered to a person: the person and every roleMembershipRef
// target, whatever the relation. That is the rule midPoint applies when the
// person claims the item (AuthorizationHelper.isAmongCandidates; deputies'
// memberships, which midPoint also accepts, are not read here).
type candidateSet map[string]bool

// candidatesOf returns the candidate set of a user.
func candidatesOf(u userJSON) candidateSet {
	s := candidateSet{}
	if u.OID != "" {
		s[u.OID] = true
	}
	for _, raw := range u.RoleMembershipRef {
		var r refJSON
		if json.Unmarshal(raw, &r) == nil && r.OID != "" {
			s[r.OID] = true
		}
	}
	return s
}

// oids returns the set's OIDs, the subject's first, then in a stable order.
func (s candidateSet) oids(first string) []string {
	out := []string{}
	if s[first] {
		out = append(out, first)
	}
	rest := make([]string, 0, len(s))
	for o := range s {
		if o != first {
			rest = append(rest, o)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// inboxFilter selects the open cases with a work item assigned to the subject
// or offered to one of mine and not claimed yet.
func inboxFilter(subjectOID string, mine candidateSet) string {
	assigned := fmt.Sprintf(`workItem/assigneeRef matches (oid = %s)`, quoteQueryString(subjectOID))
	oids := mine.oids(subjectOID)
	if len(oids) == 0 {
		return `state = "open" and ` + assigned
	}
	quoted := make([]string, len(oids))
	for i, o := range oids {
		quoted[i] = quoteQueryString(o)
	}
	return fmt.Sprintf(`state = "open" and (%s or workItem matches (candidateRef matches (oid = (%s)) `+
		`and assigneeRef not exists and closeTimestamp not exists))`, assigned, strings.Join(quoted, ", "))
}

// GroupWorkItem is a work item confirmed claimable or releasable by the
// subject, with the case it belongs to.
type GroupWorkItem struct {
	Subject  Subject
	Case     CaseSummary
	WorkItem WorkItem
	// Object and Target name the case's objectRef and targetRef the way the
	// inbox does, read as the subject.
	Object, Target ObjectRef
	// OfferedTo is the group the item is offered to, or was claimed from.
	OfferedTo ObjectRef
}

// CheckClaimable reads the case as the subject and refuses a work item the
// subject can't claim: the case is not open, the item is closed or not
// visible, someone holds it (the subject included), or none of its candidates
// is the subject or one of their groups. It only reads.
func (c *Client) CheckClaimable(ctx context.Context, caseOID, workItemID string) (GroupWorkItem, error) {
	o, err := c.readOpenWorkItem(ctx, caseOID, workItemID)
	if err != nil {
		return GroupWorkItem{}, err
	}
	wi, subj := o.wi, o.subj
	if wi.assignedTo(subj.OID) != nil {
		return GroupWorkItem{}, &CodedError{Code: CodeInvalidInput, Err: fmt.Errorf("refused: work item %s in case %s is already yours "+
			"(%s holds it), so there is nothing to claim; decide it with decide_work_item", wi.ID.s, o.label, subj.Name)}
	}
	if n := wi.assigneeNames(); n != "" {
		return GroupWorkItem{}, &CodedError{Code: CodeNotInInbox, Err: fmt.Errorf("refused: work item %s in case %s is assigned to %s, "+
			"not to %s, so it can't be claimed", wi.ID.s, o.label, n, subj.Name)}
	}
	g := wi.offeredTo(o.mine)
	if g == nil {
		offered := "no group"
		if cs := wi.candidates(); len(cs) > 0 {
			offered = groupName(&cs[0])
		}
		return GroupWorkItem{}, &CodedError{Code: CodeNotInInbox, Err: fmt.Errorf("refused: work item %s in case %s is assigned to no one "+
			"and offered to %s, which %s doesn't belong to, so %s can't claim it", wi.ID.s, o.label, offered, subj.Name, subj.Name)}
	}
	return o.group(ctx, c, nil, g), nil
}

// CheckReleasable reads the case as the subject and refuses a work item the
// subject can't release: the case is not open, the item is closed or not
// visible, nobody has claimed it, someone else holds it, or it was assigned
// to the subject directly, so there is no group to give it back to. It only
// reads.
func (c *Client) CheckReleasable(ctx context.Context, caseOID, workItemID string) (GroupWorkItem, error) {
	o, err := c.readOpenWorkItem(ctx, caseOID, workItemID)
	if err != nil {
		return GroupWorkItem{}, err
	}
	wi, subj := o.wi, o.subj
	a := wi.assignees()
	if len(a) == 0 {
		return GroupWorkItem{}, &CodedError{Code: CodeNotClaimed, Err: fmt.Errorf("refused: work item %s in case %s is offered to %s "+
			"and nobody has claimed it yet, so there is nothing to release", wi.ID.s, o.label, groupName(wi.candidateIn(o.mine)))}
	}
	mine := wi.assignedTo(subj.OID)
	if mine == nil || len(a) > 1 {
		return GroupWorkItem{}, &CodedError{Code: CodeNotInInbox, Err: fmt.Errorf("refused: work item %s in case %s is assigned to %s; "+
			"%s can release only an item they alone claimed", wi.ID.s, o.label, wi.assigneeNames(), subj.Name)}
	}
	g := wi.claimedFrom(subj.OID, o.mine)
	if g == nil {
		return GroupWorkItem{}, &CodedError{Code: CodeInvalidInput, Err: fmt.Errorf("refused: work item %s in case %s was given to %s "+
			"directly, not offered to a group, so there is no group to release it to", wi.ID.s, o.label, subj.Name)}
	}
	return o.group(ctx, c, mine, g), nil
}

// group is the checked item in GroupWorkItem's shape.
func (o openWorkItem) group(ctx context.Context, c *Client, assignee, offered *refJSON) GroupWorkItem {
	object, target := o.names(ctx, c)
	return GroupWorkItem{
		Subject:   o.subj,
		Case:      o.cj.summary(),
		Object:    object,
		Target:    target,
		WorkItem:  o.workItem(assignee),
		OfferedTo: newRefReader(c).objectRef(ctx, *offered, false),
	}
}

// PlanClaimWorkItem builds the bodyless REST claim of a work item.
func (c *Client) PlanClaimWorkItem(caseOID, workItemID string) (Plan, error) {
	return planWorkItemAction(caseOID, workItemID, "claim", "Claim")
}

// PlanReleaseWorkItem builds the bodyless REST release of a work item.
func (c *Client) PlanReleaseWorkItem(caseOID, workItemID string) (Plan, error) {
	return planWorkItemAction(caseOID, workItemID, "release", "Release")
}

func planWorkItemAction(caseOID, workItemID, action, verb string) (Plan, error) {
	if err := requireOID(caseOID); err != nil {
		return Plan{}, &CodedError{Code: CodeInvalidInput, Err: fmt.Errorf("case %w", err)}
	}
	workItemID = strings.TrimSpace(workItemID)
	if workItemID == "" {
		return Plan{}, &CodedError{Code: CodeInvalidInput, Err: fmt.Errorf("workItemId is required")}
	}
	return Plan{
		Method:  http.MethodPost,
		Path:    fmt.Sprintf("/%s/%s/workItems/%s/%s", collCases, url.PathEscape(caseOID), url.PathEscape(workItemID), action),
		Summary: fmt.Sprintf("%s work item %s in case %s", verb, workItemID, caseOID),
	}, nil
}

// WorkItemHolders is a work item as read back after a claim or release.
type WorkItemHolders struct {
	// Visible is false when the case no longer shows the subject the item, as
	// after a release when the subject may read only their own work items.
	Visible bool
	Open    bool
	// Assignees are the item's assignees, named as the case names them.
	Assignees []ObjectRef
	// Mine reports whether the subject is among them.
	Mine bool
}

// ReadWorkItemHolders reads one work item back as the subject.
func (c *Client) ReadWorkItemHolders(ctx context.Context, caseOID, workItemID string) (WorkItemHolders, error) {
	subj, err := c.subject(ctx)
	if err != nil {
		return WorkItemHolders{}, err
	}
	var cj caseJSON
	if err := c.getObject(ctx, collCases, caseOID, true, &cj); err != nil {
		return WorkItemHolders{}, err
	}
	for _, wi := range cj.items() {
		if wi.ID.s != strings.TrimSpace(workItemID) {
			continue
		}
		h := WorkItemHolders{Visible: true, Open: wi.open(), Assignees: []ObjectRef{}, Mine: wi.assignedTo(subj.OID) != nil}
		for _, r := range wi.assignees() {
			h.Assignees = append(h.Assignees, ObjectRef{OID: r.OID, Type: "User", Name: r.TargetName.value()})
		}
		return h, nil
	}
	return WorkItemHolders{Assignees: []ObjectRef{}}, nil
}
