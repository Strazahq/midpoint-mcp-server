package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

// registerTeamTools installs the M6 manager/team read tools. They are read-only
// (outside the write gate) and, in resource-server mode, run as the caller so
// midPoint scopes results to what that manager may see.
func registerTeamTools(server *mcp.Server, client *midpoint.Client) {
	registerListMyTeam(server, client)
	registerListMyManagers(server, client)
	registerListMyTeammates(server, client)
}

// teamOutput carries the users found plus the identity and org links they were
// derived from, so a zero count is self-explaining.
type teamOutput struct {
	Subject midpoint.Subject       `json:"subject" jsonschema:"the identity this answered for"`
	Orgs    []midpoint.OrgLink     `json:"orgs" jsonschema:"the org links the answer was derived from"`
	Users   []midpoint.UserSummary `json:"users"`
	Count   int                    `json:"count"`
}

func registerListMyTeam(server *mcp.Server, client *midpoint.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:  "list_my_team",
		Title: "List my team",
		Description: "List the authenticated user's direct reports: the members of the orgs they manage " +
			"(empty if they manage none). Use the returned OIDs with get_user_assignments to review a report's " +
			"access, or request_role to request access for them. The result names the identity it answered for.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in limitInput) (*mcp.CallToolResult, teamOutput, error) {
		res, err := client.ListMyTeam(ctx, in.Limit)
		if err != nil {
			return nil, teamOutput{}, err
		}
		return text(teamMessage(res, "manages", "direct report", "manages no orgs")), teamResult(res), nil
	})
}

func registerListMyManagers(server *mcp.Server, client *midpoint.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_my_managers",
		Title:       "List my managers",
		Description: "List who the authenticated user reports to: the managers of the orgs they are a member of.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in limitInput) (*mcp.CallToolResult, teamOutput, error) {
		res, err := client.ListMyManagers(ctx, in.Limit)
		if err != nil {
			return nil, teamOutput{}, err
		}
		return text(teamMessage(res, "reports to", "manager", "belongs to no orgs")), teamResult(res), nil
	})
}

func registerListMyTeammates(server *mcp.Server, client *midpoint.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:  "list_my_teammates",
		Title: "List my teammates",
		Description: "List the authenticated user's peers: the other members of the orgs they belong to " +
			"(the caller is excluded). Which orgs count as 'my team' is a deployment setting — a user who belongs " +
			"to several orgs would otherwise return everyone in all of them — see the team.orgOids / team.orgNames " +
			"selector in the config file.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in limitInput) (*mcp.CallToolResult, teamOutput, error) {
		res, err := client.ListMyTeammates(ctx, in.Limit)
		if err != nil {
			return nil, teamOutput{}, err
		}
		return text(teamMessage(res, "has", "teammate", "belongs to no orgs")), teamResult(res), nil
	})
}

func teamResult(res midpoint.TeamResult) teamOutput {
	return teamOutput{Subject: res.Subject, Orgs: res.Orgs, Users: res.Users, Count: len(res.Users)}
}

// teamMessage names the subject and, when the answer is empty, says which of the
// two reasons it was: no qualifying org links at all, or org links whose other
// members midPoint did not return (typically an authorization gap).
func teamMessage(res midpoint.TeamResult, verb, noun, noOrgs string) string {
	n := len(res.Users)
	if n > 0 {
		return fmt.Sprintf("%s %s %d %s(s), via org(s): %s.",
			res.Subject.Name, verb, n, noun, midpoint.OrgNames(res.Orgs))
	}
	if len(res.Orgs) == 0 {
		return fmt.Sprintf("%s %s, so has no %ss.%s",
			res.Subject.Name, noOrgs, noun, subjectHint(res.Subject, true))
	}
	return fmt.Sprintf("%s %s no %ss: org(s) %s returned no matching members"+
		" (midPoint returns only what this identity is authorized to see).%s",
		res.Subject.Name, verb, noun, midpoint.OrgNames(res.Orgs), subjectHint(res.Subject, true))
}
