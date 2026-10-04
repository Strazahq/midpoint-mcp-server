package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

// registerWriteTools installs the M2 write tools. When allowWrites is false,
// every tool returns a dry-run preview instead of calling midPoint.
func registerWriteTools(server *mcp.Server, client *midpoint.Client, allowWrites bool, info serverInfo) {
	registerCreateUser(server, client, allowWrites)
	registerSetUserEnabled(server, client, allowWrites, true)
	registerSetUserEnabled(server, client, allowWrites, false)
	registerAssignRole(server, client, allowWrites)
	registerUnassignRole(server, client, allowWrites, info)
	registerRecomputeUser(server, client, allowWrites)
}

// writeOutput is the structured result of a write tool: the planned request plus
// whether it was applied or previewed.
type writeOutput struct {
	Applied  bool   `json:"applied"`
	DryRun   bool   `json:"dryRun"`
	Summary  string `json:"summary"`
	Method   string `json:"method"`
	Endpoint string `json:"endpoint"`
	Body     any    `json:"body,omitempty"`
	Result   string `json:"result,omitempty"`
}

// previewWrite renders a plan as a dry-run preview (used when the write gate is
// closed).
func previewWrite(plan midpoint.Plan) (*mcp.CallToolResult, writeOutput) {
	out := writeOutput{
		DryRun:   true,
		Summary:  plan.Summary,
		Method:   plan.Method,
		Endpoint: plan.Endpoint(),
		Body:     plan.Body,
	}
	return text(fmt.Sprintf("DRY RUN — writes disabled. Would %s.\n%s\nSet %s=true to apply.",
		lowerFirst(plan.Summary), requestLine(plan), midpoint.EnvAllowWrites)), out
}

// runWrite applies the gate: preview when writes are disabled, otherwise apply.
func runWrite(ctx context.Context, allowWrites bool, client *midpoint.Client, plan midpoint.Plan) (*mcp.CallToolResult, writeOutput, error) {
	if !allowWrites {
		res, out := previewWrite(plan)
		return res, out, nil
	}

	res, err := client.Apply(ctx, plan)
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
	if res.OID != "" {
		out.Result = "oid=" + res.OID
	} else {
		out.Result = fmt.Sprintf("status=%d", res.StatusCode)
	}
	return text(fmt.Sprintf("Applied: %s.\n%s (%s)", plan.Summary, requestLine(plan), out.Result)), out, nil
}

// --- create_user ---

type createUserInput struct {
	Name         string `json:"name" jsonschema:"the user's login name (required)"`
	FullName     string `json:"fullName,omitempty" jsonschema:"display/full name"`
	GivenName    string `json:"givenName,omitempty" jsonschema:"given (first) name"`
	FamilyName   string `json:"familyName,omitempty" jsonschema:"family (last) name"`
	EmailAddress string `json:"emailAddress,omitempty" jsonschema:"email address"`
}

func registerCreateUser(server *mcp.Server, client *midpoint.Client, allowWrites bool) {
	addTool(server, &mcp.Tool{
		Name:        "create_user",
		Title:       "Create user",
		Description: "Create a new midPoint user. Requires the write gate; otherwise returns a dry-run preview.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in createUserInput) (*mcp.CallToolResult, writeOutput, error) {
		plan, err := client.PlanCreateUser(midpoint.UserSpec{
			Name:         in.Name,
			FullName:     in.FullName,
			GivenName:    in.GivenName,
			FamilyName:   in.FamilyName,
			EmailAddress: in.EmailAddress,
		})
		if err != nil {
			return nil, writeOutput{}, err
		}
		return runWrite(ctx, allowWrites, client, plan)
	})
}

// --- enable_user / disable_user ---

func registerSetUserEnabled(server *mcp.Server, client *midpoint.Client, allowWrites, enable bool) {
	const names = "userName is the user's midPoint name (their login, not the full name); the call is refused when it doesn't match the OID, " +
		"so the person confirming it reads the right name. "
	name, title, desc := "disable_user", "Disable user", "Disable a midPoint user (activation → disabled). "+names+"Requires the write gate; otherwise a dry-run preview."
	if enable {
		name, title, desc = "enable_user", "Enable user", "Enable a midPoint user (activation → enabled). "+names+"Requires the write gate; otherwise a dry-run preview."
	}
	addTool(server, &mcp.Tool{
		Name:        name,
		Title:       title,
		Description: desc,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in userWriteInput) (*mcp.CallToolResult, writeOutput, error) {
		plan, err := client.PlanSetUserEnabled(in.OID, enable)
		if err != nil {
			return nil, writeOutput{}, err
		}
		user := client.NamedRef(ctx, in.OID, "UserType")
		if err := confirmRef("userName", "user", in.UserName, &user); err != nil {
			return nil, writeOutput{}, err
		}
		verb := "Disable"
		if enable {
			verb = "Enable"
		}
		plan.Summary = fmt.Sprintf("%s user %s", verb, personLabel(user))
		return runWrite(ctx, allowWrites, client, plan)
	})
}

// --- assign_role / unassign_role ---

type roleAssignmentInput struct {
	UserOID  string `json:"userOid" jsonschema:"OID of the user"`
	UserName string `json:"userName" jsonschema:"the user's midPoint name (their login, not the full name); must match userOid"`
	RoleOID  string `json:"roleOid" jsonschema:"OID of the role"`
	RoleName string `json:"roleName" jsonschema:"the role's midPoint name (its unique name attribute, not the display name); must match roleOid"`
}

// confirmAssignmentNames checks a role assignment's names against the objects
// its OIDs point to, read as the acting identity.
func confirmAssignmentNames(ctx context.Context, client *midpoint.Client, in roleAssignmentInput) (user, role midpoint.ObjectRef, err error) {
	user, role = client.AssignmentRefs(ctx, in.UserOID, in.RoleOID)
	if err = confirmRef("userName", "user", in.UserName, &user); err != nil {
		return user, role, err
	}
	return user, role, confirmRef("roleName", "role", in.RoleName, &role)
}

func registerAssignRole(server *mcp.Server, client *midpoint.Client, allowWrites bool) {
	addTool(server, &mcp.Tool{
		Name:        "assign_role",
		Title:       "Assign role",
		Description: "Assign a role to a user. " + nameArgsNote + " Requires the write gate; otherwise a dry-run preview.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in roleAssignmentInput) (*mcp.CallToolResult, writeOutput, error) {
		plan, err := client.PlanAssignRole(in.UserOID, in.RoleOID)
		if err != nil {
			return nil, writeOutput{}, err
		}
		user, role, err := confirmAssignmentNames(ctx, client, in)
		if err != nil {
			return nil, writeOutput{}, err
		}
		plan.Summary = fmt.Sprintf("Assign role %s to %s", thingLabel(role), personLabel(user))
		return runWrite(ctx, allowWrites, client, plan)
	})
}

// viewWriteOutput is a write tool's result in the shape views read.
type viewWriteOutput struct {
	viewFields
	writeOutput
}

func registerUnassignRole(server *mcp.Server, client *midpoint.Client, allowWrites bool, info serverInfo) {
	addTool(server, &mcp.Tool{
		Name:        "unassign_role",
		Title:       "Unassign role",
		Description: "Remove a user's assignment to a role. " + nameArgsNote + " Requires the write gate; otherwise a dry-run preview.",
	}, viewTool("unassign_role", client, info, func(ctx context.Context, _ *mcp.CallToolRequest, in roleAssignmentInput) (*mcp.CallToolResult, revocationOutput, error) {
		// Resolves the assignment id via a read even in dry-run, so the preview is accurate.
		plan, err := client.PlanUnassignRole(ctx, in.UserOID, in.RoleOID)
		if err != nil {
			return nil, revocationOutput{}, err
		}
		user, role, err := confirmAssignmentNames(ctx, client, in)
		if err != nil {
			return nil, revocationOutput{}, err
		}
		plan.Summary = fmt.Sprintf("Remove role %s from %s", thingLabel(role), personLabel(user))
		res, out, err := runWrite(ctx, allowWrites, client, plan)
		if err != nil {
			return nil, revocationOutput{}, err
		}
		revocation, err := client.ReadRevocation(ctx, in.UserOID, in.RoleOID, out.Applied)
		return res, revocationOutput{viewWriteOutput: viewWriteOutput{writeOutput: out}, Revocation: revocation}, err
	}))
}

// --- recompute_user ---

// userWriteInput names one user by OID and midPoint name (D38).
type userWriteInput struct {
	OID      string `json:"oid" jsonschema:"OID of the user"`
	UserName string `json:"userName" jsonschema:"the user's midPoint name (their login, not the full name); must match oid"`
}

func registerRecomputeUser(server *mcp.Server, client *midpoint.Client, allowWrites bool) {
	addTool(server, &mcp.Tool{
		Name:  "recompute_user",
		Title: "Recompute user",
		Description: "Recompute (reconcile) a user so midPoint re-evaluates policies and propagates changes. " +
			"userName is the user's midPoint name (their login, not the full name); the call is refused when it doesn't match the OID, " +
			"so the person confirming it reads the right name. Requires the write gate; otherwise a dry-run preview.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in userWriteInput) (*mcp.CallToolResult, writeOutput, error) {
		plan, err := client.PlanRecomputeUser(in.OID)
		if err != nil {
			return nil, writeOutput{}, err
		}
		user := client.NamedRef(ctx, in.OID, "UserType")
		if err := confirmRef("userName", "user", in.UserName, &user); err != nil {
			return nil, writeOutput{}, err
		}
		plan.Summary = "Recompute user " + personLabel(user)
		return runWrite(ctx, allowWrites, client, plan)
	})
}
