package main

import (
	"context"
	"fmt"
	"strconv"
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
	registerListRequestTargets(server, client, info)
	registerListMyRequests(server, client, info)
	registerListWorkItems(server, client, info)
	registerGetCase(server, client, info)
	registerDecideWorkItem(server, client, allowWrites, info)
	registerCancelRequest(server, client, allowWrites, info)
}

// --- list_requestable_roles ---

type listRequestableRolesInput struct {
	Query   string `json:"query,omitempty" jsonschema:"case-insensitive substring of role name or display name; at most 100 characters"`
	Limit   int    `json:"limit,omitempty" jsonschema:"maximum results, default 20, max 100"`
	ForUser string `json:"forUser,omitempty" jsonschema:"OID of a user to list requestable roles FOR — e.g. a direct report from list_my_team; returns roles they do not already hold, so you can request one for them. Omit to list your own."`
}

type listRequestableRolesOutput struct {
	ForUserRef   *midpoint.ObjectRef   `json:"forUserRef,omitempty"`
	LimitReached bool                  `json:"limitReached"`
	Query        string                `json:"query,omitempty"`
	Form         *midpoint.RequestForm `json:"form,omitempty"`
	viewFields
	Preview midpoint.CatalogBasis  `json:"preview" jsonschema:"how the roles were chosen: from midPoint's request rules for the caller (basis rules) or the roles flagged requestable"`
	Roles   []midpoint.OfferedRole `json:"roles"`
	Count   int                    `json:"count"`
	ForUser string                 `json:"forUser,omitempty"`
}

func registerListRequestableRoles(server *mcp.Server, client *midpoint.Client, info serverInfo) {
	addTool(server, &mcp.Tool{
		Name:  "list_requestable_roles",
		Title: "List requestable roles",
		Description: "List the roles the caller may request, for themselves or with forUser for someone else " +
			"(list_request_targets says for whom). The roles come from midPoint's request rules for the caller (the " +
			"#assign authorizations of their roles, read by the server's account): each role says the relations it may " +
			"be requested with (default is member), whether start and end dates and which request fields may be set, " +
			"and which rules allow it. When the rules can't be read, the roles flagged requestable are listed instead " +
			"(preview.basis says which). Roles the person already holds are left out. midPoint still decides on " +
			"submit. Pair with request_role, then list_my_requests / list_work_items to track approval. " + untrustedTextNote,
	}, viewTool("list_requestable_roles", client, info, func(ctx context.Context, _ *mcp.CallToolRequest, in listRequestableRolesInput) (*mcp.CallToolResult, listRequestableRolesOutput, error) {
		target := strings.TrimSpace(in.ForUser)
		catalog, err := client.RequestCatalogFor(ctx, target, in.Query, in.Limit)
		if err != nil {
			return nil, listRequestableRolesOutput{}, err
		}
		roles := catalog.Roles
		msg := fmt.Sprintf("Found %d requestable role(s).", len(roles))
		if target != "" {
			msg = fmt.Sprintf("Found %d role(s) you can request for user %s.", len(roles), target)
		}
		if catalog.Basis.Basis == "rules" {
			msg = fmt.Sprintf("Found %d role(s) your midPoint request rules let you request.", len(roles))
			if target != "" {
				msg = fmt.Sprintf("Found %d role(s) your midPoint request rules let you request for user %s.", len(roles), target)
			}
		}
		form := client.RequestForm()
		return text(requestCatalogText(msg, catalog, form)), listRequestableRolesOutput{Roles: roles, Count: len(roles), ForUser: target, ForUserRef: catalog.ForUserRef,
			LimitReached: catalog.LimitReached, Query: catalog.Query, Form: form, Preview: catalog.Basis}, nil
	}))
}

// --- request_role ---

type requestRoleInput struct {
	ValidFrom string         `json:"validFrom,omitempty" jsonschema:"inclusive start in RFC 3339 with offset; no earlier than today"`
	ValidTo   string         `json:"validTo,omitempty" jsonschema:"end in RFC 3339 with offset; after the start and in the future"`
	Relation  string         `json:"relation,omitempty" jsonschema:"the relation to request: default (member, when left out), or one list_requestable_roles offers for this role, such as approver or owner"`
	Fields    map[string]any `json:"fields,omitempty" jsonschema:"the request's fields: values keyed by the names in list_requestable_roles form.items (its Request form fields); a list for a multiple field, an option value for a choice. midPoint decides on submit who must fill what"`
	RoleOID   string         `json:"roleOid" jsonschema:"OID of the role to request"`
	RoleName  string         `json:"roleName" jsonschema:"the role's midPoint name (its unique name attribute, not the display name); must match roleOid"`
	UserOID   string         `json:"userOid,omitempty" jsonschema:"OID of the user the role is for; defaults to the authenticated user (self-service)"`
	UserName  string         `json:"userName,omitempty" jsonschema:"the user's midPoint name (their login, not the full name); required with userOid and must match it"`
}

func registerRequestRole(server *mcp.Server, client *midpoint.Client, allowWrites bool, info serverInfo) {
	addTool(server, &mcp.Tool{
		Name:  "request_role",
		Title: "Request role",
		Description: "Request a role for yourself or a report. Submits an assignment-add delta; midPoint policy " +
			"decides whether that opens an approval case or executes immediately — so by default this refuses roles " +
			"that midPoint's catalog does not flag requestable (see list_requestable_roles), because for those it " +
			"would grant rather than request. Use assign_role for a deliberate grant. The requester is always the " +
			"authenticated user. " + nameArgsNote + " Respects the write gate.",
	}, viewTool("request_role", client, info, func(ctx context.Context, _ *mcp.CallToolRequest, in requestRoleInput) (*mcp.CallToolResult, requestRoleOutput, error) {
		res, out, err := requestRole(ctx, client, allowWrites, in)
		return res, out, err
	}))
}

// requestRole is request_role's handler, apart from the view fields.
func requestRole(ctx context.Context, client *midpoint.Client, allowWrites bool, in requestRoleInput) (*mcp.CallToolResult, requestRoleOutput, error) {
	target := strings.TrimSpace(in.UserOID)
	if target == "" {
		self, err := client.Self(ctx)
		if err != nil {
			return nil, requestRoleOutput{}, fmt.Errorf("resolving self: %w", err)
		}
		target = self.OID
	}

	// Checked before the dry-run preview too: a preview that says "would
	// request" for a role the rules don't offer is the same lie (D44, D45).
	if err := client.CheckRequestOffer(ctx, target, in.RoleOID, in.Relation); err != nil {
		return nil, requestRoleOutput{}, err
	}

	plan, fields, err := client.PlanRequestRoleWithValues(target, in.RoleOID, in.Relation, in.ValidFrom, in.ValidTo, in.Fields)
	if err != nil {
		return nil, requestRoleOutput{}, err
	}
	user, role := client.RequestRefs(ctx, target, in.RoleOID)
	if err := confirmRef("roleName", "role", in.RoleName, &role); err != nil {
		return nil, requestRoleOutput{}, err
	}
	if strings.TrimSpace(in.UserOID) != "" || strings.TrimSpace(in.UserName) != "" {
		if err := confirmRef("userName", "user", in.UserName, &user); err != nil {
			return nil, requestRoleOutput{}, err
		}
	}
	plan.Summary = fmt.Sprintf("Request role %s%s for %s (subject to approval policy)", thingLabel(role), relationPhrase(in.Relation), personLabel(user))
	req := requestOutcome{Role: role, User: user, Relation: relationName(in.Relation), Approvers: []midpoint.ObjectRef{}, Fields: fields}
	if in.ValidFrom != "" || in.ValidTo != "" {
		req.Validity = &midpoint.Validity{ValidFrom: in.ValidFrom, ValidTo: in.ValidTo}
	}
	if !allowWrites {
		res, out := previewWrite(plan)
		req.Outcome = "preview"
		return res, requestRoleOutput{writeOutput: out, Request: req}, nil
	}

	applied, err := client.Apply(ctx, plan)
	if err != nil {
		return nil, requestRoleOutput{}, err
	}
	out := requestRoleOutput{Request: req, writeOutput: writeOutput{
		Applied:  true,
		Summary:  plan.Summary,
		Method:   plan.Method,
		Endpoint: plan.Endpoint(),
		Body:     plan.Body,
	}}
	// Best-effort: surface the approval case, if policy created one. This is
	// robust to whether midPoint signals approval via status code.
	if caseOID := client.FindRequestCase(ctx, target, in.RoleOID); caseOID != "" {
		out.Request.Outcome = "pending-approval"
		out.Request.CaseOID = caseOID
		if detail, err := client.GetCase(ctx, caseOID); err == nil {
			out.Request.Approvers = detail.NextApprovers
		}
		out.Result = "pending approval; caseOid=" + caseOID
		return text(fmt.Sprintf("Requested role %s%s for %s; it is waiting for approval.\nCase: %s\n%s", thingLabel(role), relationPhrase(in.Relation), personLabel(user), caseOID, requestLine(plan))), out, nil
	}
	// No case means midPoint applied the assignment then and there. Say so
	// plainly: the caller asked to request access and instead received it,
	// and a hedge like "likely executed directly" leaves that ambiguous.
	out.Request.Outcome = "granted"
	out.Result = fmt.Sprintf("GRANTED directly — no approval case was created (status=%d)", applied.StatusCode)
	return text(fmt.Sprintf("Role %s%s was GRANTED to %s immediately: midPoint applied the assignment and no "+
		"approval policy matched, so this was not a request.\n%s (status=%d)", thingLabel(role), relationPhrase(in.Relation), personLabel(user), requestLine(plan), applied.StatusCode)), out, nil
}

// --- list_my_requests ---

type listMyRequestsOutput struct {
	viewFields
	Subject  midpoint.Subject          `json:"subject" jsonschema:"the identity this answered for"`
	Requests []midpoint.RequestSummary `json:"requests"`
	Count    int                       `json:"count"`
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
		return text(requestsText(fmt.Sprintf("%s has initiated %d request(s).%s",
				res.Subject.Name, n, subjectHint(res.Subject, n == 0)), res.Requests)),
			listMyRequestsOutput{Subject: res.Subject, Requests: res.Requests, Count: n}, nil
	}))
}

// --- list_work_items ---

type listWorkItemsOutput struct {
	viewFields
	Subject   midpoint.Subject         `json:"subject" jsonschema:"the identity whose inbox this is"`
	WorkItems []midpoint.InboxWorkItem `json:"workItems"`
	Count     int                      `json:"count"`
}

func registerListWorkItems(server *mcp.Server, client *midpoint.Client, info serverInfo) {
	addTool(server, &mcp.Tool{
		Name:  "list_work_items",
		Title: "List work items",
		Description: "List the authenticated user's approval inbox: open work items assigned to them, and open work " +
			"items offered to a group they belong to that nobody has claimed yet (offered: true, with offeredTo naming the " +
			"group; midPoint shows those only to people allowed to read them). An offered item must be claimed " +
			"(claim_work_item) before it can be approved or rejected; an item claimed: true can be given back " +
			"(release_work_item). The result names whose inbox it is — in personal mode that is the server's configured " +
			"account, not necessarily the caller. " + untrustedTextNote,
	}, viewTool("list_work_items", client, info, func(ctx context.Context, _ *mcp.CallToolRequest, in limitInput) (*mcp.CallToolResult, listWorkItemsOutput, error) {
		res, err := client.ListWorkItems(ctx, in.Limit)
		if err != nil {
			return nil, listWorkItemsOutput{}, err
		}
		n := len(res.WorkItems)
		return text(workItemsText(fmt.Sprintf("%d work item(s) in the approval inbox of %s.%s",
				n, res.Subject.Name, subjectHint(res.Subject, n == 0)), res.WorkItems)),
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
		Description: "Fetch an approval case by OID, including its work items. " + untrustedTextNote,
	}, viewTool("get_case", client, info, func(ctx context.Context, _ *mcp.CallToolRequest, in oidInput) (*mcp.CallToolResult, getCaseOutput, error) {
		c, err := client.GetCase(ctx, in.OID)
		if err != nil {
			return nil, getCaseOutput{}, err
		}
		return text(caseText(fmt.Sprintf("Case %s: state=%s, %d work item(s).", c.OID, c.State, len(c.WorkItems)), c)), getCaseOutput{CaseDetail: c}, nil
	}))
}

// --- decide_work_item ---

type decideWorkItemInput struct {
	CaseOID    string `json:"caseOid" jsonschema:"OID of the case (caseOid from list_work_items or get_case)"`
	WorkItemID string `json:"workItemId" jsonschema:"id of the work item within the case (id from list_work_items or get_case)"`
	UserName   string `json:"userName" jsonschema:"midPoint name (login) of the person the request is for; empty only when midPoint doesn't show you their name"`
	RoleName   string `json:"roleName" jsonschema:"midPoint name of the role (or other object) the request adds or removes; empty only when midPoint doesn't show you its name"`
	Decision   string `json:"decision" jsonschema:"approve or reject"`
	Comment    string `json:"comment,omitempty" jsonschema:"optional comment recorded with the decision"`
}

type decideWorkItemOutput struct {
	viewFields
	writeOutput
	Subject         midpoint.Subject     `json:"subject" jsonschema:"the identity midPoint executed the decision as (or, in a dry run, would)"`
	CaseOID         string               `json:"caseOid"`
	Case            string               `json:"case,omitempty" jsonschema:"the case name"`
	WorkItemID      string               `json:"workItemId"`
	Decision        string               `json:"decision" jsonschema:"approve or reject, as submitted"`
	Comment         string               `json:"comment,omitempty"`
	Object          string               `json:"object,omitempty" jsonschema:"the focus the case changes"`
	Target          string               `json:"target,omitempty" jsonschema:"what was requested"`
	Requestor       string               `json:"requestor,omitempty"`
	RecordedOutcome string               `json:"recordedOutcome,omitempty" jsonschema:"the outcome midPoint shows on the work item, read back after the decision"`
	CaseState       string               `json:"caseState,omitempty" jsonschema:"the case state read back after the decision; it stays open while later approval stages remain"`
	NextApprovers   []midpoint.ObjectRef `json:"nextApprovers" jsonschema:"assignees of the case's open work items, read back after the decision"`
}

// parseDecision maps the decision argument to approve (true) or reject (false).
func parseDecision(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "approve":
		return true, nil
	case "reject":
		return false, nil
	}
	return false, &midpoint.CodedError{Code: midpoint.CodeInvalidInput,
		Err: fmt.Errorf("decision must be %q or %q, got %q", "approve", "reject", s)}
}

func registerDecideWorkItem(server *mcp.Server, client *midpoint.Client, allowWrites bool, info serverInfo) {
	addTool(server, &mcp.Tool{
		Name:  "decide_work_item",
		Title: "Decide work item",
		Description: "Approve or reject an open approval work item assigned to the authenticated user, with an " +
			"optional comment. Before writing anything it reads the case as that user and refuses a work item that is " +
			"not open or not in their approval inbox (list_work_items), and one offered to their group that nobody has " +
			"claimed yet: claim it first (claim_work_item). The decision executes as that user, and the " +
			"result names the case, the outcome midPoint recorded, and the identity it ran as. " + nameArgsNote + " Respects the write gate.",
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
		if err := confirmRef("userName", "person", in.UserName, &d.Object); err != nil {
			return nil, decideWorkItemOutput{}, err
		}
		if err := confirmRef("roleName", "role", in.RoleName, &d.Target); err != nil {
			return nil, decideWorkItemOutput{}, err
		}
		plan, err := client.PlanCompleteWorkItem(d.WorkItem.CaseOID, d.WorkItem.ID, approve, in.Comment)
		if err != nil {
			return nil, decideWorkItemOutput{}, err
		}

		out := decideWorkItemOutput{
			Subject:       d.Subject,
			CaseOID:       d.WorkItem.CaseOID,
			Case:          d.Case.Name,
			WorkItemID:    d.WorkItem.ID,
			Decision:      decision,
			Object:        d.Case.Object,
			Target:        d.Case.Target,
			Requestor:     d.Case.Requestor,
			NextApprovers: []midpoint.ObjectRef{},
		}
		if strings.TrimSpace(in.Comment) != "" {
			out.Comment = in.Comment // sent as given, like the plan body
		}
		what := decidedWhat(d.Target, d.Object, d.Case.Requestor)
		as := fmt.Sprintf("%s (%s mode)", d.Subject.Name, d.Subject.Mode)
		where := caseLine(out.Case, out.CaseOID, out.WorkItemID)
		plan.Summary = fmt.Sprintf("%s %s", strings.ToUpper(decision[:1])+decision[1:], what)

		if !allowWrites {
			_, out.writeOutput = previewWrite(plan)
			return text(fmt.Sprintf("DRY RUN — writes disabled. Would %s %s as %s.\n%s\n%s\nSet %s=true to apply.",
				decision, what, as, where, requestLine(plan), midpoint.EnvAllowWrites)), out, nil
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
			out.NextApprovers = after.NextApprovers
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
		return text(fmt.Sprintf("%s %s as %s: %s.\n%s", verb, what, as, recorded, where)), out, nil
	}))
}

// decidedWhat names what a decision is about in words a person recognises:
// the role and who it is for, and who asked.
func decidedWhat(target, object midpoint.ObjectRef, requestor string) string {
	s := fmt.Sprintf("%s for %s", thingLabel(target), personLabel(object))
	if requestor != "" {
		s += ", requested by " + requestor
	}
	return s
}

// caseLine is the reference line under a decision: the case and work item.
func caseLine(name, oid, workItemID string) string {
	if name != "" {
		return fmt.Sprintf("Case: %q (%s), work item %s", name, oid, workItemID)
	}
	return fmt.Sprintf("Case: %s, work item %s", oid, workItemID)
}

// caseStateText renders a case state read back after a decision.
func caseStateText(s string) string {
	if s == "" {
		return "in an unknown state"
	}
	return s
}

// requestOutcome describes what the request changed, including previews.
type requestOutcome struct {
	Role      midpoint.ObjectRef   `json:"role"`
	User      midpoint.ObjectRef   `json:"user"`
	Relation  string               `json:"relation" jsonschema:"the relation requested, by local name; default is member"`
	Outcome   string               `json:"outcome"`
	CaseOID   string               `json:"caseOid,omitempty"`
	Approvers []midpoint.ObjectRef `json:"approvers"`
	Validity  *midpoint.Validity   `json:"validity,omitempty"`
	Fields    map[string]any       `json:"fields,omitempty"`
}
type requestRoleOutput struct {
	viewFields
	writeOutput
	Request requestOutcome `json:"request"`
}

// requestCatalogText keeps line one stable and names every role and form item.
// With midPoint's request rules (D44) each role also says its relations,
// what may be filled in, and the rules that allow it.
func requestCatalogText(first string, cat midpoint.CatalogResult, form *midpoint.RequestForm) string {
	t := newListText(first)
	rules := cat.Basis.Basis == "rules"
	if form != nil || rules || cat.Basis.Reason != "" {
		t.group("Roles:")
	}
	for _, r := range cat.Roles {
		fields := []textField{{"oid", r.OID}, {"displayName", r.DisplayName}, {"risk", r.RiskLevel}}
		if rules {
			fields = append(fields, textField{"relations", strings.Join(r.Relations, "|")}, textField{"fields", offeredFields(r)},
				textField{"dates", strconv.FormatBool(r.Validity)}, textField{"because", strings.Join(r.Because, "; ")})
		}
		t.item(r.Name, fields...)
		t.untrusted(fieldDescription, fromRoleRecord, r.Description)
	}
	if rules && len(cat.Basis.Unsure) > 0 {
		t.group("Rules the preview could not evaluate exactly:")
		for _, u := range cat.Basis.Unsure {
			t.item(u)
		}
	}
	if !rules && cat.Basis.Reason != "" {
		t.group("Request rules not used:")
		t.item(cat.Basis.Reason)
	}
	if form != nil {
		t.group("Request form fields:")
		for _, i := range form.Items {
			var opts []string
			for _, o := range i.Options {
				opts = append(opts, o.Value)
			}
			t.item(i.Name, textField{"type", i.Type}, textField{"required", strconv.FormatBool(i.Required)}, textField{"label", i.DisplayName},
				textField{"multiple", boolText(i.Multiple)}, textField{"options", strings.Join(opts, "|")})
		}
		if len(form.Other) > 0 {
			t.group("Fields only midPoint's own page can fill:")
			for _, o := range form.Other {
				t.item(o.Name, textField{"label", o.DisplayName})
			}
		}
	}
	return t.String()
}

// offeredFields says which request fields a role offers: all, none, or their names.
func offeredFields(r midpoint.OfferedRole) string {
	switch {
	case r.AllFields:
		return "all"
	case len(r.Fields) == 0:
		return "none"
	}
	return strings.Join(r.Fields, ",")
}

// relationName is a requested relation's local name, "default" for member.
func relationName(rel string) string {
	if i := strings.LastIndexAny(rel, "#:"); i >= 0 {
		rel = rel[i+1:]
	}
	if rel = strings.TrimSpace(rel); rel == "" {
		return "default"
	}
	return rel
}

// relationPhrase says a relation other than member in a summary: " as approver".
func relationPhrase(rel string) string {
	if r := relationName(rel); r != "default" {
		return " as " + r
	}
	return ""
}

// --- list_request_targets ---

type listRequestTargetsInput struct {
	Query string `json:"query,omitempty" jsonschema:"case-insensitive substring of the person's name or full name; at most 100 characters"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum results, default 20, max 100"`
}

type listRequestTargetsOutput struct {
	viewFields
	Preview      midpoint.CatalogBasis    `json:"preview" jsonschema:"how the people were chosen: from midPoint's request rules for the caller (basis rules), or the caller alone"`
	People       []midpoint.OfferedPerson `json:"people"`
	Count        int                      `json:"count"`
	LimitReached bool                     `json:"limitReached"`
	Query        string                   `json:"query,omitempty"`
}

func registerListRequestTargets(server *mcp.Server, client *midpoint.Client, info serverInfo) {
	addTool(server, &mcp.Tool{
		Name:  "list_request_targets",
		Title: "List whom I may request for",
		Description: "List the people the caller may request roles for, according to midPoint's request rules for " +
			"the caller (the #assign authorizations of their roles, read by the server's account): themselves, the " +
			"people in orgs they manage, or whoever a rule names, each with the rules that allow it. Pass one as forUser " +
			"to list_requestable_roles and as userOid to request_role. When the rules can't be read, only the caller is " +
			"listed. midPoint still decides on submit.",
	}, viewTool("list_request_targets", client, info, func(ctx context.Context, _ *mcp.CallToolRequest, in listRequestTargetsInput) (*mcp.CallToolResult, listRequestTargetsOutput, error) {
		res, err := client.RequestTargets(ctx, in.Query, in.Limit)
		if err != nil {
			return nil, listRequestTargetsOutput{}, err
		}
		first := fmt.Sprintf("You may request roles for %d person(s), according to your midPoint request rules.", len(res.People))
		if res.Basis.Basis != "rules" {
			first = "You may request roles for yourself."
		}
		t := newListText(first)
		for _, p := range res.People {
			t.item(p.Name, textField{"oid", p.OID}, textField{"fullName", p.DisplayName}, textField{"because", strings.Join(p.Because, "; ")})
		}
		if len(res.Basis.Unsure) > 0 {
			t.group("Rules the preview could not evaluate exactly:")
			for _, u := range res.Basis.Unsure {
				t.item(u)
			}
		}
		if res.Basis.Reason != "" {
			t.group("Request rules not used:")
			t.item(res.Basis.Reason)
		}
		return text(t.String()), listRequestTargetsOutput{Preview: res.Basis, People: res.People, Count: len(res.People), LimitReached: res.LimitReached, Query: res.Query}, nil
	}))
}
