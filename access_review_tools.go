package main

import "github.com/strazahq/midpoint-mcp-server/internal/midpoint"

// revocationOutput extends the existing write result with the observed removal.
type revocationOutput struct {
	viewWriteOutput
	Revocation midpoint.Revocation `json:"revocation"`
}

// accessReviewText keeps the existing summary and lists every assignment and
// effective membership, including the dates an agent needs to review access.
func accessReviewText(line1 string, res midpoint.UserAssignments) string {
	t := newListText(line1)
	t.group("Direct assignments:")
	for _, a := range res.Assignments {
		t.item(a.TargetName, textField{"oid", a.TargetOID}, textField{"type", a.TargetType}, textField{"relation", a.Relation}, textField{"status", a.Status}, textField{"validFrom", a.ValidFrom}, textField{"validTo", a.ValidTo})
	}
	t.group("Effective membership:")
	for _, m := range res.Effective {
		source := "inherited"
		if m.Direct {
			source = "direct"
		}
		t.item(m.Name, textField{"oid", m.OID}, textField{"type", m.Type}, textField{"source", source})
	}
	return t.String()
}

// accessReviewTeamText lists the visible reports under the existing summary.
func accessReviewTeamText(res midpoint.TeamResult) string {
	t := newListText(teamMessage(res, "manages", "direct report", "manages no orgs"))
	for _, u := range res.Users {
		t.item(u.Name, textField{"oid", u.OID}, textField{"fullName", u.FullName}, textField{"status", u.Status})
	}
	return t.String()
}
