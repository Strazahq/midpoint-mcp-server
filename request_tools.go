package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

// registerRequestTools installs the M3 requests & approvals tools. The mutating
// ones (request_role, decide_work_item) respect the write gate.
//
// Every tool here is one a view renders or calls, so each result carries
// viewFields.
func registerRequestTools(server *mcp.Server, client *midpoint.Client, allowWrites bool, info serverInfo) {
	registerListRequestableRoles(server, client, info)
	registerRequestRole(server, client, allowWrites, info)
	registerListMyRequests(server, client, info)
	registerListWorkItems(server, client, info)
	registerGetCase(server, client, info)
	registerDecideWorkItem(server, client, allowWrites, info)
}

// --- list_requestable_roles ---

type listRequestableRolesInput struct {
	Limit   int    `json:"limit,omitempty" jsonschema:"maximum results, default 20, max 100"`
	ForUser string `json:"forUser,omitempty" jsonschema:"OID of a user to list requestable roles FOR — e.g. a direct report from list_my_team; returns roles they do not already hold, so you can request one for them. Omit to list your own."`
}

type listRequestableRolesOutput struct {
	viewFields
	Roles   []midpoint.RoleSummary `json:"roles"`
	Count   int                    `json:"count"`
	ForUser string                 `json:"forUser,omitempty"`
}

func registerListRequestableRoles(server *mcp.Server, client *midpoint.Client, info serverInfo) {
	addTool(server, &mcp.Tool{
		Name:  "list_requestable_roles",
		Title: "List requestable roles",
		Description: "List requestable roles (self-service, or for a report via forUser): roles flagged " +
			"requestable in midPoint's catalog, filtered to what the caller is authorized to see. With forUser, " +
			"returns roles that report does not already hold. Pair with request_role (which accepts the same target " +
			"user) to submit one, then list_my_requests / list_work_items to track approval.",
	}, viewTool("list_requestable_roles", client, info, func(ctx context.Context, _ *mcp.CallToolRequest, in listRequestableRolesInput) (*mcp.CallToolResult, listRequestableRolesOutput, error) {
		target := strings.TrimSpace(in.ForUser)
		roles, err := client.ListRequestableRolesFor(ctx, target, in.Limit)
		if err != nil {
			return nil, listRequestableRolesOutput{}, err
		}
		msg := fmt.Sprintf("Found %d requestable role(s).", len(roles))
		if target != "" {
			msg = fmt.Sprintf("Found %d role(s) you can request for user %s.", len(roles), target)
		}
		return text(msg), listRequestableRolesOutput{Roles: roles, Count: len(roles), ForUser: target}, nil
	}))
}

// --- request_role ---

type requestRoleInput struct {
	RoleOID string `json:"roleOid" jsonschema:"OID of the role to request"`
	UserOID string `json:"userOid,omitempty" jsonschema:"OID of the user the role is for; defaults to the authenticated user (self-service)"`
}

func registerRequestRole(server *mcp.Server, client *midpoint.Client, allowWrites bool, info serverInfo) {
	addTool(server, &mcp.Tool{
		Name:  "request_role",
		Title: "Request role",
		Description: "Request a role for yourself or a report. Submits an assignment-add delta; midPoint policy " +
			"decides whether that opens an approval case or executes immediately — so by default this refuses roles " +
			"that midPoint's catalog does not flag requestable (see list_requestable_roles), because for those it " +
			"would grant rather than request. Use assign_role for a deliberate grant. The requester is always the " +
			"authenticated user. Respects the write gate.",
	}, viewTool("request_role", client, info, func(ctx context.Context, _ *mcp.CallToolRequest, in requestRoleInput) (*mcp.CallToolResult, viewWriteOutput, error) {
		res, out, err := requestRole(ctx, client, allowWrites, in)
		return res, viewWriteOutput{writeOutput: out}, err
	}))
}

// requestRole is request_role's handler, apart from the view fields.
func requestRole(ctx context.Context, client *midpoint.Client, allowWrites bool, in requestRoleInput) (*mcp.CallToolResult, writeOutput, error) {
	target := strings.TrimSpace(in.UserOID)
	if target == "" {
		self, err := client.Self(ctx)
		if err != nil {
			return nil, writeOutput{}, fmt.Errorf("resolving self: %w", err)
		}
		target = self.OID
	}

	// Checked before the dry-run preview too: a preview that says "would
	// request" for a role that would in fact be granted is the same lie.
	if err := client.EnsureRequestable(ctx, in.RoleOID); err != nil {
		return nil, writeOutput{}, err
	}

	plan, err := client.PlanRequestRole(target, in.RoleOID)
	if err != nil {
		return nil, writeOutput{}, err
	}
	if !allowWrites {
		res, out := previewWrite(plan)
		return res, out, nil
	}

	applied, err := client.Apply(ctx, plan)
	if err != nil {
		return nil, writeOutput{}, err
	}
	out := writeOutput{
		Applied:  true,
		Summary:  plan.Summary,
		Method:   plan.Method,
		Endpoint: plan.Endpoint(),
		Body:     plan.Body,
	}
	// Best-effort: surface the approval case, if policy created one. This is
	// robust to whether midPoint signals approval via status code.
	if caseOID := client.FindRequestCase(ctx, target, in.RoleOID); caseOID != "" {
		out.Result = "pending approval; caseOid=" + caseOID
		return text(fmt.Sprintf("Requested role %s for %s — pending approval (case %s).", in.RoleOID, target, caseOID)), out, nil
	}
	// No case means midPoint applied the assignment then and there. Say so
	// plainly: the caller asked to request access and instead received it,
	// and a hedge like "likely executed directly" leaves that ambiguous.
	out.Result = fmt.Sprintf("GRANTED directly — no approval case was created (status=%d)", applied.StatusCode)
	return text(fmt.Sprintf("Role %s was GRANTED to %s immediately: midPoint applied the assignment and no "+
		"approval policy matched, so this was not a request. (status=%d)", in.RoleOID, target, applied.StatusCode)), out, nil
}

// --- list_my_requests ---

type listMyRequestsOutput struct {
	viewFields
	Subject  midpoint.Subject       `json:"subject" jsonschema:"the identity this answered for"`
	Requests []midpoint.CaseSummary `json:"requests"`
	Count    int                    `json:"count"`
}

func registerListMyRequests(server *mcp.Server, client *midpoint.Client, info serverInfo) {
	addTool(server, &mcp.Tool{
		Name:  "list_my_requests",
		Title: "List my requests",
		Description: "List approval cases the authenticated user initiated. The result names the identity it " +
			"answered for — in personal mode that is the server's configured account, not necessarily the caller.",
	}, viewTool("list_my_requests", client, info, func(ctx context.Context, _ *mcp.CallToolRequest, in limitInput) (*mcp.CallToolResult, listMyRequestsOutput, error) {
		res, err := client.ListMyRequests(ctx, in.Limit)
		if err != nil {
			return nil, listMyRequestsOutput{}, err
		}
		n := len(res.Requests)
		return text(fmt.Sprintf("%s has initiated %d request(s).%s",
				res.Subject.Name, n, subjectHint(res.Subject, n == 0))),
			listMyRequestsOutput{Subject: res.Subject, Requests: res.Requests, Count: n}, nil
	}))
}

// --- list_work_items ---

type listWorkItemsOutput struct {
	viewFields
	Subject   midpoint.Subject    `json:"subject" jsonschema:"the identity whose inbox this is"`
	WorkItems []midpoint.WorkItem `json:"workItems"`
	Count     int                 `json:"count"`
}

func registerListWorkItems(server *mcp.Server, client *midpoint.Client, info serverInfo) {
	addTool(server, &mcp.Tool{
		Name:  "list_work_items",
		Title: "List work items",
		Description: "List the authenticated user's approval inbox: open work items assigned to them. The result " +
			"names whose inbox it is — in personal mode that is the server's configured account, not necessarily the caller.",
	}, viewTool("list_work_items", client, info, func(ctx context.Context, _ *mcp.CallToolRequest, in limitInput) (*mcp.CallToolResult, listWorkItemsOutput, error) {
		res, err := client.ListWorkItems(ctx, in.Limit)
		if err != nil {
			return nil, listWorkItemsOutput{}, err
		}
		n := len(res.WorkItems)
		return text(fmt.Sprintf("%d work item(s) in the approval inbox of %s.%s",
				n, res.Subject.Name, subjectHint(res.Subject, n == 0))),
			listWorkItemsOutput{Subject: res.Subject, WorkItems: res.WorkItems, Count: n}, nil
	}))
}

// --- get_case ---

type getCaseOutput struct {
	viewFields
	midpoint.CaseDetail
}

func registerGetCase(server *mcp.Server, client *midpoint.Client, info serverInfo) {
	addTool(server, &mcp.Tool{
		Name:        "get_case",
		Title:       "Get case",
		Description: "Fetch an approval case by OID, including its work items.",
	}, viewTool("get_case", client, info, func(ctx context.Context, _ *mcp.CallToolRequest, in oidInput) (*mcp.CallToolResult, getCaseOutput, error) {
		c, err := client.GetCase(ctx, in.OID)
		if err != nil {
			return nil, getCaseOutput{}, err
		}
		return text(fmt.Sprintf("Case %s: state=%s, %d work item(s).", c.OID, c.State, len(c.WorkItems))), getCaseOutput{CaseDetail: c}, nil
	}))
}

// --- decide_work_item ---

type decideWorkItemInput struct {
	CaseOID    string `json:"caseOid" jsonschema:"OID of the case (caseOid from list_work_items or get_case)"`
	WorkItemID string `json:"workItemId" jsonschema:"id of the work item within the case (id from list_work_items or get_case)"`
	Decision   string `json:"decision" jsonschema:"approve or reject"`
	Comment    string `json:"comment,omitempty" jsonschema:"optional comment recorded with the decision"`
}

type decideWorkItemOutput struct {
	viewFields
	writeOutput
	Subject         midpoint.Subject `json:"subject" jsonschema:"the identity midPoint executed the decision as (or, in a dry run, would)"`
	CaseOID         string           `json:"caseOid"`
	Case            string           `json:"case,omitempty" jsonschema:"the case name"`
	WorkItemID      string           `json:"workItemId"`
	Decision        string           `json:"decision" jsonschema:"approve or reject, as submitted"`
	Comment         string           `json:"comment,omitempty"`
	Object          string           `json:"object,omitempty" jsonschema:"the focus the case changes"`
	Target          string           `json:"target,omitempty" jsonschema:"what was requested"`
	Requestor       string           `json:"requestor,omitempty"`
	RecordedOutcome string           `json:"recordedOutcome,omitempty" jsonschema:"the outcome midPoint shows on the work item, read back after the decision"`
	CaseState       string           `json:"caseState,omitempty" jsonschema:"the case state read back after the decision; it stays open while later approval stages remain"`
}

// parseDecision maps the decision argument to approve (true) or reject (false).
func parseDecision(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "approve":
		return true, nil
	case "reject":
		return false, nil
	}
	return false, fmt.Errorf("decision must be %q or %q, got %q", "approve", "reject", s)
}

func registerDecideWorkItem(server *mcp.Server, client *midpoint.Client, allowWrites bool, info serverInfo) {
	addTool(server, &mcp.Tool{
		Name:  "decide_work_item",
		Title: "Decide work item",
		Description: "Approve or reject an open approval work item assigned to the authenticated user, with an " +
			"optional comment. Before writing anything it reads the case as that user and refuses a work item that is " +
			"not open or not in their approval inbox (list_work_items). The decision executes as that user, and the " +
			"result names the case, the outcome midPoint recorded, and the identity it ran as. Respects the write gate.",
	}, viewTool("decide_work_item", client, info, func(ctx context.Context, _ *mcp.CallToolRequest, in decideWorkItemInput) (*mcp.CallToolResult, decideWorkItemOutput, error) {
		approve, err := parseDecision(in.Decision)
		if err != nil {
			return nil, decideWorkItemOutput{}, err
		}
		decision := "reject"
		if approve {
			decision = "approve"
		}

		// Checked before the dry-run preview too: a preview that says "would
		// approve" a work item that is not the caller's to decide is a false promise.
		d, err := client.CheckDecidable(ctx, in.CaseOID, in.WorkItemID)
		if err != nil {
			return nil, decideWorkItemOutput{}, err
		}
		plan, err := client.PlanCompleteWorkItem(d.WorkItem.CaseOID, d.WorkItem.ID, approve, in.Comment)
		if err != nil {
			return nil, decideWorkItemOutput{}, err
		}

		out := decideWorkItemOutput{
			Subject:    d.Subject,
			CaseOID:    d.WorkItem.CaseOID,
			Case:       d.Case.Name,
			WorkItemID: d.WorkItem.ID,
			Decision:   decision,
			Object:     d.Case.Object,
			Target:     d.Case.Target,
			Requestor:  d.Case.Requestor,
		}
		if strings.TrimSpace(in.Comment) != "" {
			out.Comment = in.Comment // sent as given, like the plan body
		}
		what := decidedWhat(out)
		as := fmt.Sprintf("%s (%s mode)", d.Subject.Name, d.Subject.Mode)

		if !allowWrites {
			_, out.writeOutput = previewWrite(plan)
			return text(fmt.Sprintf("DRY RUN — writes disabled. Would %s %s as %s via %s %s.\nSet %s=true to apply.",
				decision, what, as, plan.Method, plan.Endpoint(), midpoint.EnvAllowWrites)), out, nil
		}

		applied, err := client.Apply(ctx, plan)
		if err != nil {
			return nil, decideWorkItemOutput{}, err
		}
		out.writeOutput = writeOutput{
			Applied:  true,
			Summary:  plan.Summary,
			Method:   plan.Method,
			Endpoint: plan.Endpoint(),
			Body:     plan.Body,
			Result:   fmt.Sprintf("status=%d", applied.StatusCode),
		}

		// Read back what midPoint recorded. The decision is already made, so a
		// failed read is reported rather than returned as an error, and only a
		// confirmed outcome is reported as the decision taken.
		verb := fmt.Sprintf("Submitted %s on", decision)
		recorded := "the case could not be re-read to confirm the recorded outcome"
		if after, err := client.GetCase(ctx, out.CaseOID); err == nil {
			out.CaseState = after.State
			for _, wi := range after.WorkItems {
				if wi.ID == out.WorkItemID {
					out.RecordedOutcome = wi.Outcome
				}
			}
			switch {
			case out.RecordedOutcome == decision:
				verb = "Rejected"
				if approve {
					verb = "Approved"
				}
				recorded = fmt.Sprintf("midPoint recorded outcome %s; the case is now %s", decision, caseStateText(out.CaseState))
			case out.RecordedOutcome != "":
				recorded = fmt.Sprintf("midPoint shows outcome %s on this work item, not the %s submitted, so it "+
					"was most likely decided by someone else first; the case is now %s",
					out.RecordedOutcome, decision, caseStateText(out.CaseState))
			default:
				recorded = fmt.Sprintf("midPoint accepted the request (status=%d) but the work item shows no recorded "+
					"outcome yet; the case is %s", applied.StatusCode, caseStateText(out.CaseState))
			}
		}
		return text(fmt.Sprintf("%s %s as %s: %s.", verb, what, as, recorded)), out, nil
	}))
}

// decidedWhat names a work item for a decision message: the item, its case,
// and what the case would change, as far as midPoint named them.
func decidedWhat(o decideWorkItemOutput) string {
	s := fmt.Sprintf("work item %s in case %s", o.WorkItemID, o.CaseOID)
	if o.Case != "" {
		s = fmt.Sprintf("work item %s in case %q (%s)", o.WorkItemID, o.Case, o.CaseOID)
	}
	if o.Target != "" && o.Object != "" {
		s += fmt.Sprintf(" — %s for %s", o.Target, o.Object)
	}
	if o.Requestor != "" {
		s += ", requested by " + o.Requestor
	}
	return s
}

// caseStateText renders a case state read back after a decision.
func caseStateText(s string) string {
	if s == "" {
		return "in an unknown state"
	}
	return s
}
