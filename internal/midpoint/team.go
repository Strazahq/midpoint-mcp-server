package midpoint

import (
	"context"
	"encoding/json"
	"fmt"
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

// isManagerOf reports whether the ref links the user as a manager of the org,
// against the deployment's configured manager relation.
func (r orgRef) isManagerOf(managerRelation string) bool {
	return relationLocal(r.Relation) == managerRelation
}

// OrgLink is one org the caller is linked to and the relation that links them.
// Team answers carry the links they were derived from, so an empty result can
// be told apart from a caller who simply has no orgs.
type OrgLink struct {
	OID      string `json:"oid"`
	Name     string `json:"name,omitempty"`
	Relation string `json:"relation,omitempty" jsonschema:"local part of the relation QName: manager, default, …"`
	Manager  bool   `json:"manager"`
	Source   string `json:"source,omitempty" jsonschema:"where the link was found: parentOrgRef or assignment"`
	Selected bool   `json:"selected" jsonschema:"false when the configured team org selector excludes this org, so team tools ignore it"`
}

// TeamResult is a team answer plus the context needed to explain an empty one:
// the identity it answered for and the org links it was derived from.
type TeamResult struct {
	Subject Subject       `json:"subject"`
	Orgs    []OrgLink     `json:"orgs"`
	Users   []UserSummary `json:"users"`
}

// orgLinks renders org refs as OrgLinks, classifying each against the
// deployment's configured manager relation and org selector.
func orgLinks(refs []orgRef, team TeamConfig) []OrgLink {
	managerRel := team.managerRelation()
	out := make([]OrgLink, 0, len(refs))
	for _, r := range refs {
		out = append(out, OrgLink{
			OID:      r.OID,
			Name:     r.TargetName.value(),
			Relation: relationLocal(r.Relation),
			Manager:  r.isManagerOf(managerRel),
			Source:   r.source,
			Selected: team.selects(r),
		})
	}
	return out
}

// callerOrgs collects the caller's org links from the configured source,
// de-duplicated by org and relation. The org selector is deliberately NOT
// applied here: whoami reports every link with its Selected flag, so a
// misconfigured selector is visible rather than silently subtractive.
func callerOrgs(self userJSON, team TeamConfig) []orgRef {
	var refs []orgRef
	switch team.orgSource() {
	case OrgSourceAssignment:
		refs = self.assignedOrgs()
	case OrgSourceBoth:
		refs = append(self.parentOrgs(), self.assignedOrgs()...)
	case OrgSourceFallback:
		if refs = self.parentOrgs(); len(refs) == 0 {
			refs = self.assignedOrgs()
		}
	default:
		refs = self.parentOrgs()
	}

	seen := make(map[string]bool, len(refs))
	out := make([]orgRef, 0, len(refs))
	for _, r := range refs {
		key := r.OID + "|" + relationLocal(r.Relation)
		if r.OID == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out
}

// queryableOrgs keeps the selected links the caller holds as manager
// (wantManager) or as anything else — plain membership. Membership is "not
// manager" rather than an exact relation match because midPoint omits the
// relation entirely for default membership.
func queryableOrgs(links []OrgLink, wantManager bool) []OrgLink {
	out := make([]OrgLink, 0, len(links))
	for _, l := range links {
		if l.Selected && l.Manager == wantManager {
			out = append(out, l)
		}
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
	return c.teamQuery(ctx, true, c.teamConfig().memberRelation(), limit)
}

// ListMyManagers returns the managers of the orgs the caller is a member of —
// who the caller reports to.
func (c *Client) ListMyManagers(ctx context.Context, limit int) (TeamResult, error) {
	return c.teamQuery(ctx, false, c.teamConfig().managerRelation(), limit)
}

// ListMyTeammates returns the caller's peers: the other members of the orgs the
// caller is a member of. Which orgs count is a deployment question — a user in
// several orgs would otherwise pull in everyone from all of them — so the
// team.orgOids / team.orgNames selector narrows it.
func (c *Client) ListMyTeammates(ctx context.Context, limit int) (TeamResult, error) {
	return c.teamQuery(ctx, false, c.teamConfig().memberRelation(), limit)
}

// teamConfig returns the deployment's team settings (zero value = defaults).
func (c *Client) teamConfig() TeamConfig { return c.cfg.File.Team }

// teamQuery is the shared shape of the team lookups: take the caller's org
// links, keep the selected ones they hold as manager (viaManaged) or as member,
// and return the users linked to those orgs with wantRelation. The links are
// returned either way so an empty answer says which case it was.
func (c *Client) teamQuery(ctx context.Context, viaManaged bool, wantRelation string, limit int) (TeamResult, error) {
	if err := c.requireCallerIdentity(ctx); err != nil {
		return TeamResult{}, err
	}
	self, err := c.selfUser(ctx)
	if err != nil {
		return TeamResult{}, err
	}
	team := c.teamConfig()
	res := TeamResult{
		Subject: Subject{OID: self.OID, Name: self.Name.value(), Mode: c.Mode(ctx)},
		Orgs:    queryableOrgs(orgLinks(callerOrgs(self, team), team), viaManaged),
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
// parentOrgRef that Self() omits.
//
// midPoint does NOT honour options=resolveNames on /self (verified against
// 4.10.3: the org refs come back without targetName), so when the caller has org
// links and any is unnamed the object is re-fetched by OID, where resolveNames
// does work. Names are what make an empty team answer legible and what the
// team.orgNames selector matches on, so the extra read earns itself — but it is
// best-effort: the identity is already established, so a failure there degrades
// to OIDs rather than failing the call.
func (c *Client) selfUser(ctx context.Context) (userJSON, error) {
	body, err := c.get(ctx, "/self", nil)
	if err != nil {
		return userJSON{}, err
	}
	var resp struct {
		User userJSON `json:"user"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return userJSON{}, fmt.Errorf("decoding /self: %w", err)
	}
	if resp.User.OID == "" || !hasUnnamedOrg(resp.User) {
		return resp.User, nil
	}
	var named userJSON
	if err := c.getObject(ctx, collUsers, resp.User.OID, true, &named); err != nil {
		return resp.User, nil
	}
	return named, nil
}

// hasUnnamedOrg reports whether any of the user's org links lacks a resolved
// name — the signal that a resolveNames re-read is worth making.
func hasUnnamedOrg(u userJSON) bool {
	for _, r := range append(u.parentOrgs(), u.assignedOrgs()...) {
		if r.TargetName.value() == "" {
			return true
		}
	}
	return false
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
