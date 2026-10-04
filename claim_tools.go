package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

// Approval work items offered to a group (docs/ui-contract.md Q4):
// list_work_items marks them offered; claim_work_item makes one the caller's
// to decide, and release_work_item gives a claimed one back to its group.
// Both are writes on the caller's own midPoint rights, behind the write gate,
// and check the case first, because midPoint answers a claim or release that
// changes nothing (a closed item, an item assigned directly) with a plain 204.

// registerClaimTools installs claim_work_item and release_work_item. The
// approval inbox view calls both.
func registerClaimTools(server *mcp.Server, client *midpoint.Client, allowWrites bool, info serverInfo) {
	registerGroupWorkItemTool(server, client, allowWrites, info, claimAction)
	registerGroupWorkItemTool(server, client, allowWrites, info, releaseAction)
}

// groupAction is claim or release.
type groupAction struct {
	tool, title, verb, past, description string
	check                                func(*midpoint.Client, context.Context, string, string) (midpoint.GroupWorkItem, error)
	plan                                 func(*midpoint.Client, string, string) (midpoint.Plan, error)
	// confirmed reports whether the read-back shows the action took effect.
	confirmed func(midpoint.WorkItemHolders) bool
}

var claimAction = groupAction{
	tool: "claim_work_item", title: "Claim work item", verb: "claim", past: "Claimed",
	description: "Claim an open approval work item offered to a group you belong to (offered: true in list_work_items), " +
		"so it becomes yours to approve or reject with decide_work_item; the rest of the group can no longer claim it. " +
		"Before writing anything it reads the case as you and refuses an item that is closed, already held by someone " +
		"(you included), or not offered to you. release_work_item gives it back. The claim executes as you, and the " +
		"result says whether midPoint now shows the item as yours. ",
	check: (*midpoint.Client).CheckClaimable,
	plan:  (*midpoint.Client).PlanClaimWorkItem,
	confirmed: func(h midpoint.WorkItemHolders) bool {
		return h.Visible && h.Open && h.Mine && len(h.Assignees) == 1
	},
}

var releaseAction = groupAction{
	tool: "release_work_item", title: "Release work item", verb: "release", past: "Released",
	description: "Give an approval work item you claimed (claimed: true in list_work_items) back to the group it was " +
		"offered to, undecided, so anyone in the group can claim it. Before writing anything it reads the case as you and " +
		"refuses an item that is closed, not claimed, held by someone else, or assigned to you directly rather than " +
		"offered to a group. The release executes as you. ",
	check: (*midpoint.Client).CheckReleasable,
	plan:  (*midpoint.Client).PlanReleaseWorkItem,
	// After a release a person who may read only their own work items no
	// longer sees the item: that is the release, not a failure.
	confirmed: func(h midpoint.WorkItemHolders) bool {
		return !h.Visible || (h.Open && len(h.Assignees) == 0)
	},
}

type groupWorkItemInput struct {
	CaseOID    string `json:"caseOid" jsonschema:"OID of the case (caseOid from list_work_items)"`
	WorkItemID string `json:"workItemId" jsonschema:"id of the work item within the case (id from list_work_items)"`
	UserName   string `json:"userName" jsonschema:"midPoint name (login) of the person the request is for; empty only when midPoint doesn't show you their name"`
	RoleName   string `json:"roleName" jsonschema:"midPoint name of the role (or other object) the request adds or removes; empty only when midPoint doesn't show you its name"`
}

type groupWorkItemOutput struct {
	viewFields
	writeOutput
	Subject    midpoint.Subject   `json:"subject" jsonschema:"the identity midPoint executed the call as (or, in a dry run, would)"`
	CaseOID    string             `json:"caseOid"`
	Case       string             `json:"case,omitempty" jsonschema:"the case name"`
	WorkItemID string             `json:"workItemId"`
	Object     string             `json:"object,omitempty" jsonschema:"the focus the case changes"`
	Target     string             `json:"target,omitempty" jsonschema:"what was requested"`
	OfferedTo  midpoint.ObjectRef `json:"offeredTo" jsonschema:"the group the item is offered to, or was claimed from"`
	Confirmed  bool               `json:"confirmed" jsonschema:"true when the case read back after the call shows the change: a claimed item held by you alone, or a released item held by nobody (or no longer shown to you)"`
}

func registerGroupWorkItemTool(server *mcp.Server, client *midpoint.Client, allowWrites bool, info serverInfo, act groupAction) {
	addTool(server, &mcp.Tool{
		Name:        act.tool,
		Title:       act.title,
		Description: act.description + nameArgsNote + " Respects the write gate.",
	}, viewTool(act.tool, client, info, func(ctx context.Context, _ *mcp.CallToolRequest, in groupWorkItemInput) (*mcp.CallToolResult, groupWorkItemOutput, error) {
		plan, err := act.plan(client, in.CaseOID, in.WorkItemID)
		if err != nil {
			return nil, groupWorkItemOutput{}, err
		}
		// Checked before the dry-run preview too: a preview that says "would
		// claim" an item nobody may claim is a false promise.
		g, err := act.check(client, ctx, in.CaseOID, in.WorkItemID)
		if err != nil {
			return nil, groupWorkItemOutput{}, err
		}
		if err := confirmRef("userName", "person", in.UserName, &g.Object); err != nil {
			return nil, groupWorkItemOutput{}, err
		}
		if err := confirmRef("roleName", "role", in.RoleName, &g.Target); err != nil {
			return nil, groupWorkItemOutput{}, err
		}

		out := groupWorkItemOutput{
			Subject:    g.Subject,
			CaseOID:    g.WorkItem.CaseOID,
			Case:       g.Case.Name,
			WorkItemID: g.WorkItem.ID,
			Object:     g.Case.Object,
			Target:     g.Case.Target,
			OfferedTo:  g.OfferedTo,
		}
		what := fmt.Sprintf("%s for %s", thingLabel(g.Target), personLabel(g.Object))
		as := fmt.Sprintf("%s (%s mode)", g.Subject.Name, g.Subject.Mode)
		where := caseLine(out.Case, out.CaseOID, out.WorkItemID)
		plan.Summary = fmt.Sprintf("%s%s %s", strings.ToUpper(act.verb[:1]), act.verb[1:], what)
		if !allowWrites {
			_, out.writeOutput = previewWrite(plan)
			return text(fmt.Sprintf("DRY RUN — writes disabled. Would %s %s as %s.\n%s\n%s\nSet %s=true to apply.",
				act.verb, what, as, where, requestLine(plan), midpoint.EnvAllowWrites)), out, nil
		}

		applied, err := client.Apply(ctx, plan)
		if err != nil {
			return nil, groupWorkItemOutput{}, raceError(ctx, client, out, err)
		}
		out.writeOutput = writeOutput{
			Applied:  true,
			Summary:  plan.Summary,
			Method:   plan.Method,
			Endpoint: plan.Endpoint(),
			Result:   fmt.Sprintf("status=%d", applied.StatusCode),
		}
		// Read back: midPoint answers 204 also when it changed nothing.
		h, err := client.ReadWorkItemHolders(ctx, out.CaseOID, out.WorkItemID)
		switch {
		case err != nil:
			return text(fmt.Sprintf("Submitted %s on %s as %s, but the case could not be re-read to confirm it.\n%s", act.verb, what, as, where)), out, nil
		case act.confirmed(h):
			out.Confirmed = true
			return text(fmt.Sprintf("%s %s as %s: %s.\n%s", act.past, what, as, groupAfter(act, h, out.OfferedTo), where)), out, nil
		}
		return text(fmt.Sprintf("Submitted %s on %s as %s, but midPoint doesn't show the change yet: %s.\n%s",
			act.verb, what, as, holdersText(h), where)), out, nil
	}))
}

// raceError explains a claim or release midPoint refused with a server error
// (live on 4.10.3: HTTP 500 when someone else claimed the item between the
// check and the call): when the item, read again, is now closed or held by
// someone else, the refusal says so with that code. Any other failure is
// returned as it was.
func raceError(ctx context.Context, client *midpoint.Client, o groupWorkItemOutput, err error) error {
	var se *midpoint.StatusError
	if !errors.As(err, &se) || se.StatusCode < 500 {
		return err
	}
	h, rerr := client.ReadWorkItemHolders(ctx, o.CaseOID, o.WorkItemID)
	switch {
	case rerr != nil || !h.Visible:
		return err
	case !h.Open:
		return &midpoint.CodedError{Code: midpoint.CodeAlreadyDecided, Err: fmt.Errorf("refused by midPoint (%w): %s is already closed", err, groupWhat(o))}
	case len(h.Assignees) > 0 && !h.Mine:
		return &midpoint.CodedError{Code: midpoint.CodeNotInInbox, Err: fmt.Errorf("refused by midPoint (%w): %s is assigned to %s now; someone else claimed it first",
			err, groupWhat(o), refNames(h.Assignees))}
	}
	return err
}

// groupWhat names a work item for a claim or release message.
func groupWhat(o groupWorkItemOutput) string {
	s := fmt.Sprintf("work item %s in case %s", o.WorkItemID, o.CaseOID)
	if o.Case != "" {
		s = fmt.Sprintf("work item %s in case %q (%s)", o.WorkItemID, o.Case, o.CaseOID)
	}
	if o.Target != "" && o.Object != "" {
		s += fmt.Sprintf(" — %s for %s", o.Target, o.Object)
	}
	return s
}

// groupAfter says what a confirmed claim or release means.
func groupAfter(act groupAction, h midpoint.WorkItemHolders, group midpoint.ObjectRef) string {
	name := groupLabel(group)
	if act.verb == "claim" {
		return fmt.Sprintf("it is yours now, taken from %s; approve or reject it with decide_work_item, or give it back with release_work_item", name)
	}
	if !h.Visible {
		return fmt.Sprintf("it is back with %s, and midPoint no longer shows it to you", name)
	}
	return fmt.Sprintf("it is back with %s, and anyone there can claim it", name)
}

// groupLabel names a group by display name, then name, then OID.
func groupLabel(g midpoint.ObjectRef) string {
	for _, s := range []string{g.DisplayName, g.Name, g.OID} {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return "its group"
}

// holdersText describes an item read back that doesn't show the change.
func holdersText(h midpoint.WorkItemHolders) string {
	switch {
	case !h.Visible:
		return "the case no longer shows you the item"
	case !h.Open:
		return "the item is closed"
	case len(h.Assignees) == 0:
		return "the item is held by nobody"
	}
	return "the item is held by " + refNames(h.Assignees)
}
