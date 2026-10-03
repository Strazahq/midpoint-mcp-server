package main

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

// registerMyAccessTools installs the read-only tools about the caller's own
// access and their team's access that ends soon. Both run as the caller.
func registerMyAccessTools(server *mcp.Server, client *midpoint.Client) {
	registerGetMyAccess(server, client)
	registerListExpiringAccess(server, client)
}

// --- get_my_access ---

type getMyAccessOutput struct {
	midpoint.UserAssignments
}

func registerGetMyAccess(server *mcp.Server, client *midpoint.Client) {
	addTool(server, &mcp.Tool{
		Name:  "get_my_access",
		Title: "Get my access",
		Description: "List the authenticated user's own access: direct assignments with their dates and where each came from " +
			"(who requested and approved it), and effective role membership (each flagged direct or inherited). " +
			"It reads the caller's own user, so it needs nothing beyond what midPoint's self-service pages need. " + untrustedTextNote,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, getMyAccessOutput, error) {
		self, err := client.Self(ctx)
		if err != nil {
			return nil, getMyAccessOutput{}, fmt.Errorf("resolving self: %w", err)
		}
		res, err := client.GetUserAssignments(ctx, self.OID)
		if err != nil {
			return nil, getMyAccessOutput{}, err
		}
		return text(accessReviewText(fmt.Sprintf("You (%s) have %d direct assignment(s), %d effective membership(s).",
			res.User.Name, len(res.Assignments), len(res.Effective)), res)), getMyAccessOutput{UserAssignments: res}, nil
	})
}

// --- list_expiring_access ---

type listExpiringAccessInput struct {
	Days        int   `json:"days,omitempty" jsonschema:"how many days ahead to look, default 30, max 365"`
	IncludeSelf *bool `json:"includeSelf,omitempty" jsonschema:"also list the caller's own access, default true"`
}

type listExpiringAccessOutput struct {
	Subject    midpoint.Subject              `json:"subject" jsonschema:"the identity this answered for"`
	Days       int                           `json:"days"`
	Until      string                        `json:"until" jsonschema:"the end of the window (RFC 3339)"`
	People     int                           `json:"people" jsonschema:"how many people were checked: the caller (unless excluded) and their direct reports"`
	Items      []midpoint.ExpiringAssignment `json:"items" jsonschema:"assignments that end in the window, soonest first"`
	Unreadable []string                      `json:"unreadable,omitempty" jsonschema:"OIDs of people whose access midPoint didn't let the caller read"`
}

// maxExpiringPeople caps one answer: one read per person.
const maxExpiringPeople = 100

func registerListExpiringAccess(server *mcp.Server, client *midpoint.Client) {
	addTool(server, &mcp.Tool{
		Name:  "list_expiring_access",
		Title: "List access that ends soon",
		Description: "List the assignments that end within the next days (default 30) for the authenticated user and their " +
			"direct reports (the members of the orgs they manage, as list_my_team), soonest first: whose access, which role, " +
			"when it ends. It reads each person as the caller, so midPoint decides who is visible; people it hides are listed by OID.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listExpiringAccessInput) (*mcp.CallToolResult, listExpiringAccessOutput, error) {
		days := in.Days
		if days <= 0 {
			days = 30
		}
		if days > 365 {
			return nil, listExpiringAccessOutput{}, &midpoint.CodedError{Code: midpoint.CodeInvalidInput,
				Err: fmt.Errorf("days must be between 1 and 365, got %d", in.Days)}
		}
		team, err := client.ListMyTeam(ctx, maxExpiringPeople)
		if err != nil {
			return nil, listExpiringAccessOutput{}, err
		}
		var people []string
		if in.IncludeSelf == nil || *in.IncludeSelf {
			people = append(people, team.Subject.OID)
		}
		for _, u := range team.Users {
			if u.OID != team.Subject.OID {
				people = append(people, u.OID)
			}
		}
		now := time.Now()
		items, unreadable := client.ExpiringAccess(ctx, people, days, now)
		out := listExpiringAccessOutput{Subject: team.Subject, Days: days, Until: now.Add(time.Duration(days) * 24 * time.Hour).UTC().Format(time.RFC3339),
			People: len(people), Items: items, Unreadable: unreadable}
		return text(expiringText(out)), out, nil
	})
}

// expiringText names each person and role before their OIDs.
func expiringText(out listExpiringAccessOutput) string {
	who := fmt.Sprintf("%d people", out.People)
	if out.People == 1 {
		who = "1 person"
	}
	t := newListText(fmt.Sprintf("%d assignment(s) end within %d days, checked for %s as %s.", len(out.Items), out.Days, who, out.Subject.Name))
	for _, it := range out.Items {
		t.item(fmt.Sprintf("%s: %s", refNames([]midpoint.ObjectRef{displayRef(it.User)}), refNames([]midpoint.ObjectRef{displayRef(it.Target)})),
			textField{"validTo", it.ValidTo}, textField{"daysLeft", fmt.Sprint(it.DaysLeft)}, textField{"status", it.Status},
			textField{"user", it.User.Name}, textField{"userOid", it.User.OID}, textField{"targetOid", it.Target.OID})
	}
	if len(out.Unreadable) > 0 {
		t.group("Not readable as you:")
		for _, oid := range out.Unreadable {
			t.item(oid)
		}
	}
	return t.String()
}

// displayRef puts a reference's display name in front for text, keeping its
// midPoint name when there is no display name.
func displayRef(r midpoint.ObjectRef) midpoint.ObjectRef {
	if r.DisplayName != "" {
		r.Name = r.DisplayName
	}
	return r
}
