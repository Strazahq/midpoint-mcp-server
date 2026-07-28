package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

// registerIdentityTools installs whoami — the tool that answers "who does this
// server think I am?", which every self-scoped tool's result depends on.
func registerIdentityTools(server *mcp.Server, client *midpoint.Client) {
	registerWhoami(server, client)
}

// --- whoami ---

type whoamiInput struct{}

func registerWhoami(server *mcp.Server, client *midpoint.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:  "whoami",
		Title: "Who am I",
		Description: "Report the identity midPoint executes as, how it was established (personal = the server's " +
			"configured credentials; resource-server = a validated per-request end user), and the orgs that identity " +
			"is linked to. Call this first when a self-scoped tool (list_my_team, list_work_items, list_my_requests) " +
			"returns an unexpectedly empty result — it distinguishes 'you genuinely have none' from 'this server is " +
			"not acting as you'.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ whoamiInput) (*mcp.CallToolResult, midpoint.Principal, error) {
		p, err := client.Whoami(ctx)
		if err != nil {
			return nil, midpoint.Principal{}, err
		}

		var b strings.Builder
		fmt.Fprintf(&b, "Acting as %q (oid %s), %s mode.", p.Name, p.OID, p.Mode)
		if p.Mode == midpoint.ModePersonal {
			b.WriteString(" There is no per-caller identity: midPoint sees this server's configured credentials," +
				" whoever is driving the MCP client, and every self-scoped tool answers for that account.")
		}
		if len(p.Orgs) == 0 {
			b.WriteString(" This identity is linked to no orgs, so list_my_team / list_my_managers can only be empty.")
		} else {
			fmt.Fprintf(&b, " Org links: %s.", orgLinkSummary(p.Orgs))
		}
		return text(b.String()), p, nil
	})
}

// orgLinkSummary renders org links as "name (manager)" / "name (member)".
func orgLinkSummary(links []midpoint.OrgLink) string {
	parts := make([]string, 0, len(links))
	for _, l := range links {
		role := "member"
		if l.Manager {
			role = "manager"
		}
		name := l.Name
		if name == "" {
			name = l.OID
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", name, role))
	}
	return strings.Join(parts, ", ")
}

// subjectHint explains an empty self-scoped result in personal mode, where the
// answer describes the configured credentials' identity rather than whoever is
// operating the MCP client. It returns "" when the subject really is the caller
// (resource-server mode) or when the result was non-empty — the caveat only
// matters where a zero could otherwise be read as a fact about the human.
func subjectHint(s midpoint.Subject, empty bool) string {
	if !empty || s.Mode != midpoint.ModePersonal {
		return ""
	}
	return fmt.Sprintf(" Note: this server authenticates to midPoint as %q and has no per-caller identity,"+
		" so this answers for that account — not necessarily for you. Call whoami for details.", s.Name)
}
