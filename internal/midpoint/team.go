package midpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// Org relation local parts. midPoint models management through org structure: a
// user assigned to an org with the manager relation manages it; members hold the
// default relation. These local parts are used both to classify the caller's own
// parentOrgRef and (as trusted constants) in the membership query filter.
const (
	relationManager = "manager"
	relationDefault = "default"
)

// isManager reports whether the ref links the user as a manager of the org.
func (r orgRef) isManager() bool { return relationLocal(r.Relation) == relationManager }

// OrgLink is one org the caller is linked to and the relation that links them.
// Team answers carry the links they were derived from, so an empty result can
// be told apart from a caller who simply has no orgs.
type OrgLink struct {
	OID      string `json:"oid"`
	Name     string `json:"name,omitempty"`
	Relation string `json:"relation,omitempty" jsonschema:"local part of the relation QName: manager, default, …"`
	Manager  bool   `json:"manager"`
}

// TeamResult is a team answer plus the context needed to explain an empty one:
// the identity it answered for and the org links it was derived from.
type TeamResult struct {
	Subject Subject       `json:"subject"`
	Orgs    []OrgLink     `json:"orgs"`
	Users   []UserSummary `json:"users"`
}

// orgLinks renders parentOrgRef entries as OrgLinks.
func orgLinks(refs []orgRef) []OrgLink {
	out := make([]OrgLink, 0, len(refs))
	for _, r := range refs {
		out = append(out, OrgLink{
			OID:      r.OID,
			Name:     r.TargetName.value(),
			Relation: relationLocal(r.Relation),
			Manager:  r.isManager(),
		})
	}
	return out
}

// OrgNames renders links for a human-readable message, falling back to OIDs for
// orgs whose name midPoint did not resolve.
func OrgNames(links []OrgLink) string {
	parts := make([]string, 0, len(links))
	for _, l := range links {
		if l.Name != "" {
			parts = append(parts, l.Name)
			continue
		}
		parts = append(parts, l.OID)
	}
	return strings.Join(parts, ", ")
}

// relationLocal returns the local part of a relation QName ("org:manager" →
// "manager"); an empty relation (the default membership) returns "".
func relationLocal(rel string) string {
	if i := strings.LastIndexAny(rel, ":#/"); i >= 0 {
		return rel[i+1:]
	}
	return rel
}

// ListMyTeam returns the caller's direct reports: the members of the orgs the
// caller manages. It is empty when the caller manages no org. Read-only, and in
// resource-server mode it runs as the caller so midPoint scopes it to what that
// manager may see.
func (c *Client) ListMyTeam(ctx context.Context, limit int) (TeamResult, error) {
	return c.teamQuery(ctx, true, relationDefault, limit)
}

// ListMyManagers returns the managers of the orgs the caller is a member of —
// who the caller reports to.
func (c *Client) ListMyManagers(ctx context.Context, limit int) (TeamResult, error) {
	return c.teamQuery(ctx, false, relationManager, limit)
}

// teamQuery is the shared shape of the team lookups: take the caller's org
// links, keep the ones they hold as manager (viaManaged) or as member, and
// return the users linked to those orgs with wantRelation. The links are
// returned either way so an empty answer says which case it was.
func (c *Client) teamQuery(ctx context.Context, viaManaged bool, wantRelation string, limit int) (TeamResult, error) {
	self, err := c.selfUser(ctx)
	if err != nil {
		return TeamResult{}, err
	}
	res := TeamResult{
		Subject: Subject{OID: self.OID, Name: self.Name.value(), Mode: c.Mode(ctx)},
		Orgs:    orgLinks(matchingOrgs(self.parentOrgs(), viaManaged)),
		Users:   []UserSummary{},
	}
	if len(res.Orgs) == 0 {
		return res, nil
	}
	users, err := c.orgUsers(ctx, orgOIDs(res.Orgs), wantRelation, self.OID, limit)
	if err != nil {
		return TeamResult{}, err
	}
	res.Users = users
	return res, nil
}

// selfUser fetches the caller's full user object (GET /self), which carries the
// parentOrgRef that Self() omits. Names are resolved so org links can be
// reported by name and not just OID.
func (c *Client) selfUser(ctx context.Context) (userJSON, error) {
	body, err := c.get(ctx, "/self", url.Values{"options": {"resolveNames"}})
	if err != nil {
		return userJSON{}, err
	}
	var resp struct {
		User userJSON `json:"user"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return userJSON{}, fmt.Errorf("decoding /self: %w", err)
	}
	return resp.User, nil
}

// matchingOrgs selects the refs the caller holds as manager (wantManager) or as
// non-manager member (!wantManager).
func matchingOrgs(refs []orgRef, wantManager bool) []orgRef {
	var out []orgRef
	for _, r := range refs {
		if r.OID != "" && r.isManager() == wantManager {
			out = append(out, r)
		}
	}
	return out
}

// orgOIDs projects links to their OIDs, for the membership query.
func orgOIDs(links []OrgLink) []string {
	out := make([]string, 0, len(links))
	for _, l := range links {
		out = append(out, l.OID)
	}
	return out
}

// orgUsers searches for users linked to any of orgOIDs with the given relation,
// excluding excludeOID (the caller) and de-duplicating across orgs.
func (c *Client) orgUsers(ctx context.Context, orgOIDs []string, relation, excludeOID string, limit int) ([]UserSummary, error) {
	raws, err := c.searchRaw(ctx, collUsers, orgMembersFilter(orgOIDs, relation), limit)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	if excludeOID != "" {
		seen[excludeOID] = true
	}
	out := make([]UserSummary, 0, len(raws))
	for _, raw := range raws {
		var u userJSON
		if err := json.Unmarshal(raw, &u); err != nil {
			return nil, fmt.Errorf("decoding org member: %w", err)
		}
		if u.OID == "" || seen[u.OID] {
			continue
		}
		seen[u.OID] = true
		out = append(out, u.summary())
	}
	return out, nil
}

// orgMembersFilter builds a query matching users linked to any of orgOIDs with
// the given relation. The relation is a trusted constant (manager/default); OIDs
// are quoted.
func orgMembersFilter(orgOIDs []string, relation string) string {
	parts := make([]string, 0, len(orgOIDs))
	for _, oid := range orgOIDs {
		parts = append(parts, fmt.Sprintf("parentOrgRef matches (oid = %s and relation = %s)", quoteQueryString(oid), relation))
	}
	return strings.Join(parts, " or ")
}
