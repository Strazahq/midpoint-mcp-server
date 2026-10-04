package midpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Request access preview (D44): what one person may request, according to
// the request rules of their own roles, worked out over REST.
//
// The steps, each one call or a few:
//  1. Read the requester's record, as the requester: their memberships.
//  2. Read the rules of those roles, as the server's own account (one search
//     per 250 roles; midPoint lets nobody else read authorizations reliably).
//  3. For a requestee: keep the rules whose "who" selectors match them.
//  4. Ask midPoint, as the requester, which roles each rule's "which"
//     selectors name, and combine: offered when a rule allows and none denies.
//
// Nothing is cached between requests. A selector the preview can't evaluate
// exactly makes it "unsure": it then counts as matching for an allow and is
// ignored for a deny, so the preview shows more, never less, and midPoint
// decides on submit.

// ErrPreviewUnavailable means the preview can't be worked out; callers fall
// back to the plain catalog of requestable roles.
var ErrPreviewUnavailable = errors.New("request access preview unavailable")

// Preview is one requester's request rules.
type Preview struct {
	c      *Client
	Me     person
	Rules  []AssignRule
	Unsure []string // why the preview may offer more than midPoint accepts
}

// person is what the preview reads of a user: the requester or a requestee.
type person struct {
	OID, Name, FullName string
	Archetypes          []string
	Subtypes            []string
	Tenant              string
	Orgs                []refJSON // parentOrgRef, with relations
	Memberships         []refJSON // roleMembershipRef, with relations
	Delegated           []refJSON // delegatedRef
	raw                 map[string]json.RawMessage
}

func (p person) ref() ObjectRef {
	return ObjectRef{OID: p.OID, Type: "User", Name: p.Name, DisplayName: p.FullName}
}

// readPerson reads a user as the caller, without the assignment list (on a
// user with 1,000 roles it is most of the answer, measured on 4.10.3).
func (c *Client) readPerson(ctx context.Context, oid string) (person, error) {
	body, err := c.get(ctx, "/"+collUsers+"/"+url.PathEscape(oid), url.Values{"exclude": {"assignment"}})
	if err != nil {
		return person{}, err
	}
	var env struct {
		User json.RawMessage `json:"user"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return person{}, fmt.Errorf("decoding user %s: %w", oid, err)
	}
	var u struct {
		OID          string      `json:"oid"`
		Name         polyString  `json:"name"`
		FullName     polyString  `json:"fullName"`
		ArchetypeRef flexSlice   `json:"archetypeRef"`
		Subtype      flexStrings `json:"subtype"`
		TenantRef    *refJSON    `json:"tenantRef"`
		ParentOrgRef flexSlice   `json:"parentOrgRef"`
		Membership   flexSlice   `json:"roleMembershipRef"`
		DelegatedRef flexSlice   `json:"delegatedRef"`
	}
	if err := json.Unmarshal(env.User, &u); err != nil {
		return person{}, fmt.Errorf("decoding user %s: %w", oid, err)
	}
	p := person{OID: u.OID, Name: u.Name.value(), FullName: u.FullName.value(), Subtypes: u.Subtype,
		Orgs: decodeRefs(u.ParentOrgRef), Memberships: decodeRefs(u.Membership), Delegated: decodeRefs(u.DelegatedRef)}
	for _, a := range decodeRefs(u.ArchetypeRef) {
		p.Archetypes = append(p.Archetypes, a.OID)
	}
	if u.TenantRef != nil {
		p.Tenant = u.TenantRef.OID
	}
	_ = json.Unmarshal(env.User, &p.raw)
	return p, nil
}

// notAtLogin are relations whose roles give no authorizations: midPoint
// leaves them out when it compiles a person's authorizations (relation kinds
// approver, owner, consent and related are not processedOnLogin by default).
var notAtLogin = map[string]bool{"approver": true, "owner": true, "consent": true, "related": true}

// RequestPreview reads the acting person's request rules. It needs a person
// acting through the server's own account (Switch-To-Principal): only then
// can the server's account read the rules (docs/authorization.md).
func (c *Client) RequestPreview(ctx context.Context) (*Preview, error) {
	me := principalFromContext(ctx)
	if me == "" {
		return nil, fmt.Errorf("%w: the server acts as its own account, which can't read people's rules", ErrPreviewUnavailable)
	}
	p := &Preview{c: c}
	var err error
	if p.Me, err = c.readPerson(ctx, me); err != nil {
		return nil, fmt.Errorf("%w: reading your own record: %w", ErrPreviewUnavailable, err)
	}
	direct, delegated := map[string]bool{}, map[string]bool{}
	var oids []string
	for _, m := range p.Me.Memberships {
		if notAtLogin[localName(m.Relation)] || direct[m.OID] {
			continue
		}
		direct[m.OID] = true
		oids = append(oids, m.OID)
	}
	for _, d := range p.Me.Delegated {
		if !direct[d.OID] && !delegated[d.OID] {
			delegated[d.OID] = true
			oids = append(oids, d.OID)
		}
	}
	roles, err := c.readRuleRoles(ctx, oids)
	if err != nil {
		return nil, fmt.Errorf("%w: reading the rules of your roles: %w", ErrPreviewUnavailable, err)
	}
	// A role the server's account can't read could hold an allowing rule, and
	// midPoint answers a search it may not see with an empty list, not an
	// error (4.10.3). So any unread role turns the preview off.
	if missing := len(oids) - len(roles); missing > 0 {
		return nil, fmt.Errorf("%w: the server's account could not read %d of your %d roles (see docs/authorization.md)", ErrPreviewUnavailable, missing, len(oids))
	}
	now := time.Now()
	for _, oid := range oids {
		r, ok := roles[oid]
		if !ok || !r.active(now) {
			continue
		}
		if delegated[oid] && !direct[oid] && !r.Delegable {
			continue
		}
		p.Rules = append(p.Rules, parseRules(r.ref(), r.Authorization)...)
	}
	for _, rule := range p.Rules {
		for _, part := range [][]Selector{rule.Object, rule.Target} {
			for _, s := range part {
				for _, u := range s.Unsupported {
					p.Unsure = append(p.Unsure, rule.Label()+": "+u)
				}
			}
		}
	}
	return p, nil
}

// ruleRole is a role, org, service or archetype as far as the rules need it.
type ruleRole struct {
	OID            string     `json:"oid"`
	Type           string     `json:"@type"`
	Name           polyString `json:"name"`
	DisplayName    polyString `json:"displayName"`
	LifecycleState string     `json:"lifecycleState"`
	Delegable      bool       `json:"delegable"`
	Activation     *struct {
		AdministrativeStatus string `json:"administrativeStatus"`
		ValidFrom            string `json:"validFrom"`
		ValidTo              string `json:"validTo"`
	} `json:"activation"`
	Authorization flexSlice `json:"authorization"`
}

func (r ruleRole) ref() ObjectRef {
	return ObjectRef{OID: r.OID, Type: strings.TrimSuffix(localName(r.Type), "Type"), Name: r.Name.value(), DisplayName: r.DisplayName.value()}
}

// active reports whether midPoint applies the role's authorizations: its
// lifecycle state is active (none, active or deprecated), it isn't disabled,
// and today is within its validity (midPoint's ActivationComputer defaults).
func (r ruleRole) active(now time.Time) bool {
	switch r.LifecycleState {
	case "", "active", "deprecated":
	default:
		return false
	}
	if a := r.Activation; a != nil {
		if a.AdministrativeStatus == "disabled" || a.AdministrativeStatus == "archived" {
			return false
		}
		if t, err := time.Parse(time.RFC3339, a.ValidFrom); err == nil && now.Before(t) {
			return false
		}
		if t, err := time.Parse(time.RFC3339, a.ValidTo); err == nil && now.After(t) {
			return false
		}
	}
	return true
}

// ruleBatch is how many roles one rule read asks for (measured on 4.10.3:
// 1,000 roles in 4 calls, about one second).
const ruleBatch = 250

// readRuleRoles reads roles by OID, as the server's own account.
func (c *Client) readRuleRoles(ctx context.Context, oids []string) (map[string]ruleRole, error) {
	out := map[string]ruleRole{}
	own := context.WithValue(ctx, principalKey{}, "")
	for start := 0; start < len(oids); start += ruleBatch {
		batch := oids[start:min(start+ruleBatch, len(oids))]
		var quoted []string
		for _, o := range batch {
			quoted = append(quoted, quoteQueryString(o))
		}
		raws, err := c.searchUpTo(own, "abstractRoles", ". inOid ("+strings.Join(quoted, ", ")+")", len(batch))
		if err != nil {
			return nil, err
		}
		for _, raw := range raws {
			var r ruleRole
			if json.Unmarshal(raw, &r) == nil && r.OID != "" {
				out[r.OID] = r
			}
		}
	}
	return out, nil
}

// searchUpTo searches a collection with a query-language filter, as the
// caller, returning at most max objects (up to 1,000).
func (c *Client) searchUpTo(ctx context.Context, collection, filter string, max int) ([]json.RawMessage, error) {
	req := searchRequest{}
	req.Query.Paging = &searchPaging{MaxSize: max}
	if filter != "" {
		req.Query.Filter = &searchFilter{Text: filter}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := c.post(ctx, "/"+collection+"/search", nil, body)
	if err != nil {
		return nil, err
	}
	return parseObjectList(resp)
}

// --- who the rules are for ---

// match is a selector's answer: yes or no, and whether the preview is sure.
type match struct{ yes, sure bool }

var (
	matchYes    = match{true, true}
	matchNo     = match{false, true}
	matchUnsure = match{true, false}
)

// userTypes are the selector types a user is an instance of.
var userTypes = map[string]bool{"": true, "UserType": true, "FocusType": true, "AssignmentHolderType": true, "ObjectType": true}

// coversPerson reports whether a rule's "who" selectors name the requestee.
func (p *Preview) coversPerson(ctx context.Context, rule AssignRule, who person) match {
	if len(rule.Object) == 0 {
		return matchYes
	}
	out := matchNo
	for _, s := range rule.Object {
		switch m := p.selectorNamesPerson(ctx, s, who); {
		case m.yes && m.sure:
			return matchYes
		case m.yes:
			out = matchUnsure
		}
	}
	return out
}

// selectorNamesPerson checks every clause of one selector (AND).
func (p *Preview) selectorNamesPerson(ctx context.Context, s Selector, who person) match {
	if s.Never || !userTypes[s.Type] {
		return matchNo
	}
	if len(s.Unsupported) > 0 || s.OwnerSelf || len(s.FilterRaw) > 0 {
		return matchUnsure
	}
	clauses := []func() match{
		func() match {
			return boolMatch(!s.Self || who.OID == p.Me.OID)
		},
		func() match {
			return boolMatch(len(s.Archetypes) == 0 || overlaps(s.Archetypes, who.Archetypes))
		},
		func() match {
			return boolMatch(s.Subtype == "" || contains(who.Subtypes, s.Subtype))
		},
		func() match {
			return boolMatch(!s.SameTenant || (p.Me.Tenant != "" && who.Tenant == p.Me.Tenant))
		},
		func() match {
			if s.Org == "" {
				return matchYes
			}
			return p.inOrg(ctx, who, s.Org, false)
		},
		func() match {
			if s.OrgRelation == nil {
				return matchYes
			}
			return p.inManagedOrgs(ctx, who, *s.OrgRelation)
		},
		func() match {
			if s.FilterText == "" {
				return matchYes
			}
			return p.filterNamesPerson(ctx, s.FilterText, who)
		},
	}
	out := matchYes
	for _, clause := range clauses {
		switch m := clause(); {
		case !m.yes:
			return matchNo
		case !m.sure:
			out = matchUnsure
		}
	}
	return out
}

// managedOrgs are the requester's orgs where they hold one of the relations
// (none listed means the default, member, relation).
func (p *Preview) managedOrgs(or OrgRelation) []string {
	relations := or.SubjectRelations
	if len(relations) == 0 {
		relations = []string{"default"}
	}
	var out []string
	for _, o := range p.Me.Orgs {
		rel := localName(o.Relation)
		if rel == "" {
			rel = "default"
		}
		if contains(relations, rel) {
			out = append(out, o.OID)
		}
	}
	return out
}

// inManagedOrgs is midPoint's orgRelation clause for a user.
func (p *Preview) inManagedOrgs(ctx context.Context, who person, or OrgRelation) match {
	orgs := p.managedOrgs(or)
	switch or.Scope {
	case "", "allDescendants":
		out := matchNo
		for _, o := range orgs {
			switch m := p.inOrg(ctx, who, o, false); {
			case m.yes && m.sure:
				return matchYes
			case m.yes:
				out = matchUnsure
			}
		}
		return out
	case "directDescendants":
		for _, o := range orgs {
			if p.inOrg(ctx, who, o, true).yes {
				return matchYes
			}
		}
		return matchNo
	default: // allAncestors: a user is nobody's ancestor org
		return matchUnsure
	}
}

// inOrg reports whether a user is in an org (one level) or its subtree,
// asking midPoint as the requester. When the requester can't search that
// user at all, the answer is unsure.
func (p *Preview) inOrg(ctx context.Context, who person, org string, oneLevel bool) match {
	for _, o := range who.Orgs {
		if o.OID == org {
			return matchYes
		}
	}
	if oneLevel {
		return matchNo
	}
	byOID := ". inOid (" + quoteQueryString(who.OID) + ")"
	return p.c.searchConfirms(ctx, collUsers, ". inOrg "+quoteQueryString(org)+" and "+byOID, byOID)
}

// filterNamesPerson asks midPoint whether a rule's filter names the user.
func (p *Preview) filterNamesPerson(ctx context.Context, filter string, who person) match {
	text, ok := p.substituteSubject(filter)
	if !ok {
		return matchUnsure
	}
	byOID := ". inOid (" + quoteQueryString(who.OID) + ")"
	return p.c.searchConfirms(ctx, collUsers, "("+text+") and "+byOID, byOID)
}

// searchConfirms runs a search as the caller and says whether it found
// anything. An empty answer is a sure "no" only when the control search finds
// the object, so the caller may search for it at all.
func (c *Client) searchConfirms(ctx context.Context, collection, filter, control string) match {
	if raws, err := c.searchUpTo(ctx, collection, filter, 1); err == nil && len(raws) > 0 {
		return matchYes
	}
	if raws, err := c.searchUpTo(ctx, collection, control, 1); err == nil && len(raws) > 0 {
		return matchNo
	}
	return matchUnsure
}

// subjectPath finds $subject/… in a filter: midPoint fills it with the
// requester's own values when it checks the rule, and a REST search would
// silently leave it empty (live on 4.10.3), so the preview fills it in.
var subjectPath = regexp.MustCompile(`\$subject/([A-Za-z_][\w.:#-]*(?:/[A-Za-z_][\w.:#-]*)*)`)

// substituteSubject fills $subject paths with the requester's values. It
// fails, and the rule is unsure, for a path that isn't a single plain value
// or any other expression.
func (p *Preview) substituteSubject(filter string) (string, bool) {
	ok := true
	out := subjectPath.ReplaceAllStringFunc(filter, func(m string) string {
		v, found := lookupPath(p.Me.raw, strings.Split(subjectPath.FindStringSubmatch(m)[1], "/"))
		if !found {
			ok = false
			return m
		}
		return v
	})
	if strings.Contains(out, "$") {
		ok = false
	}
	return out, ok
}

// lookupPath reads one plain value from a record by item path, written as a
// query-language literal.
func lookupPath(obj map[string]json.RawMessage, segs []string) (string, bool) {
	if len(segs) == 0 || obj == nil {
		return "", false
	}
	var raw json.RawMessage
	want := localName(segs[0])
	for k, v := range obj {
		if localName(k) == want {
			raw = v
			break
		}
	}
	if raw == nil {
		return "", false
	}
	if len(segs) > 1 {
		var next map[string]json.RawMessage
		if json.Unmarshal(raw, &next) != nil {
			return "", false
		}
		return lookupPath(next, segs[1:])
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return quoteQueryString(s), true
	}
	var ps struct {
		Orig string `json:"orig"`
		OID  string `json:"oid"`
	}
	if json.Unmarshal(raw, &ps) == nil {
		switch {
		case ps.Orig != "":
			return quoteQueryString(ps.Orig), true
		case ps.OID != "":
			return quoteQueryString(ps.OID), true
		}
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String(), true
	}
	if b, err := strconv.ParseBool(string(raw)); err == nil {
		return strconv.FormatBool(b), true
	}
	return "", false
}

// --- which roles ---

// OfferedRole is a role the requester may request for the requestee, with
// one offer per relation it may be requested with.
type OfferedRole struct {
	RoleSummary
	// Offers are its relations, "default" (member) first; others only where
	// a rule names them (D45). midPoint checks a request against the rules
	// for its relation only, so each relation has its own fields and dates.
	Offers []RelationOffer `json:"offers,omitempty"`
}

// RelationOffer is what may be requested with one relation.
type RelationOffer struct {
	Relation string `json:"relation"`
	// AllFields is true when every request field may be filled; otherwise
	// Fields names those that may.
	AllFields bool     `json:"allFields,omitempty"`
	Fields    []string `json:"fields,omitempty"`
	// Validity is true when start and end dates may be set.
	Validity bool `json:"validity,omitempty"`
	// Because names the rules that allow it.
	Because []string `json:"because"`
}

// Relations lists the relations of the offers.
func (o OfferedRole) Relations() []string {
	var out []string
	for _, r := range o.Offers {
		out = append(out, r.Relation)
	}
	return out
}

// Offer returns the offer for a relation.
func (o OfferedRole) Offer(relation string) (RelationOffer, bool) {
	for _, r := range o.Offers {
		if r.Relation == relation {
			return r, true
		}
	}
	return RelationOffer{}, false
}

// RoleOffer is the catalog for one requestee.
type RoleOffer struct {
	Roles        []OfferedRole
	LimitReached bool
	Unsure       []string
}

// roleTypes are the selector types a role is an instance of.
var roleTypes = map[string]bool{"": true, "RoleType": true, "AbstractRoleType": true, "FocusType": true, "AssignmentHolderType": true, "ObjectType": true}

// catalogMax bounds each search behind the catalog.
const catalogMax = 200

// RolesFor is what the requester may request for the requestee, matching an
// optional name query, at most limit roles.
func (p *Preview) RolesFor(ctx context.Context, who person, query string, limit int) (RoleOffer, error) {
	return p.rolesFor(ctx, who, "", query, limit)
}

// RoleFor is the offer of one role for the requestee; ok is false when the
// rules don't offer it.
func (p *Preview) RoleFor(ctx context.Context, who person, roleOID string) (OfferedRole, []string, bool, error) {
	offer, err := p.rolesFor(ctx, who, ". inOid ("+quoteQueryString(roleOID)+")", "", 1)
	if err != nil || len(offer.Roles) == 0 {
		return OfferedRole{}, offer.Unsure, false, err
	}
	return offer.Roles[0], offer.Unsure, true, nil
}

// Person reads a requestee as the requester; the requester is already read.
func (p *Preview) Person(ctx context.Context, oid string) (person, error) {
	if oid == "" || oid == p.Me.OID {
		return p.Me, nil
	}
	return p.c.readPerson(ctx, oid)
}

func (p *Preview) rolesFor(ctx context.Context, who person, only, query string, limit int) (RoleOffer, error) {
	offer := RoleOffer{Unsure: append([]string(nil), p.Unsure...)}
	type found struct {
		role  RoleSummary
		rules []AssignRule
	}
	roles := map[string]*found{}
	denied := map[string][]AssignRule{}

	for _, rule := range p.Rules {
		m := p.coversPerson(ctx, rule, who)
		if !m.yes || (rule.Deny && !m.sure) {
			continue
		}
		if !m.sure {
			offer.Unsure = append(offer.Unsure, rule.Label()+": who it is for")
		}
		set, sure, truncated, err := p.ruleTargets(ctx, rule, only, query)
		if err != nil {
			return RoleOffer{}, err
		}
		offer.LimitReached = offer.LimitReached || truncated
		if rule.Deny {
			if sure {
				for oid := range set {
					denied[oid] = append(denied[oid], rule)
				}
			}
			continue
		}
		if !sure {
			offer.Unsure = append(offer.Unsure, rule.Label()+": which roles")
		}
		for oid, r := range set {
			if roles[oid] == nil {
				roles[oid] = &found{role: r}
			}
			roles[oid].rules = append(roles[oid].rules, rule)
		}
	}
	held := map[string]map[string]bool{}
	for _, m := range who.Memberships {
		if held[m.OID] == nil {
			held[m.OID] = map[string]bool{}
		}
		rel := localName(m.Relation)
		if rel == "" {
			rel = "default"
		}
		held[m.OID][rel] = true
	}
	form := p.c.RequestForm()
	for oid, f := range roles {
		o := offerFor(f.role, f.rules, denied[oid], held[oid], form)
		if len(o.Offers) > 0 {
			offer.Roles = append(offer.Roles, o)
		}
	}
	sort.Slice(offer.Roles, func(i, j int) bool {
		return strings.ToLower(roleTitle(offer.Roles[i].RoleSummary)) < strings.ToLower(roleTitle(offer.Roles[j].RoleSummary))
	})
	if limit = clampLimit(limit); len(offer.Roles) > limit {
		offer.Roles, offer.LimitReached = offer.Roles[:limit], true
	}
	return offer, nil
}

func roleTitle(r RoleSummary) string {
	if r.DisplayName != "" {
		return r.DisplayName
	}
	return r.Name
}

// offerFor combines the rules about one role, as midPoint's server does. The
// relations offered are member and those a rule names (D45). For each, the
// rules midPoint applies are those for that relation (naming it, or naming
// none); the relation is offered when they cover the role reference, no deny
// takes it away and the requestee doesn't hold it already, and its fields and
// dates are what those rules cover.
func offerFor(role RoleSummary, allows, denies []AssignRule, held map[string]bool, form *RequestForm) OfferedRole {
	o := OfferedRole{RoleSummary: role}
	var relations []string
	for _, r := range allows {
		for _, rel := range offeredRelations(r) {
			if !contains(relations, rel) {
				relations = append(relations, rel)
			}
		}
	}
	sort.SliceStable(relations, func(i, j int) bool { return relations[i] == "default" && relations[j] != "default" })
	for _, rel := range relations {
		var rules []AssignRule
		for _, r := range allows {
			if relationApplies(r, rel) {
				rules = append(rules, r)
			}
		}
		allowed := func(path string) bool {
			for _, d := range denies {
				if relationApplies(d, rel) && d.Paths.includes(path) {
					return false
				}
			}
			for _, r := range rules {
				if r.Paths.includes(path) {
					return true
				}
			}
			return false
		}
		if held[rel] || !allowed("assignment/targetRef") {
			continue
		}
		offer := RelationOffer{Relation: rel}
		offer.Validity = allowed("assignment/activation/validFrom") && allowed("assignment/activation/validTo")
		offer.AllFields = allowed("assignment/extension")
		if !offer.AllFields && form != nil {
			for _, it := range form.Items {
				if allowed("assignment/extension/" + it.Name) {
					offer.Fields = append(offer.Fields, it.Name)
				}
			}
		}
		for _, r := range rules {
			if !contains(offer.Because, r.Label()) {
				offer.Because = append(offer.Because, r.Label())
			}
		}
		o.Offers = append(o.Offers, offer)
	}
	return o
}

// offeredRelations are the relations a rule offers in a view: the ones it
// names, or member when it names none (D45; midPoint's server would accept
// any, but deployments hide the others in its GUI).
func offeredRelations(r AssignRule) []string {
	if r.Kind != ruleAssign || len(r.Relations) == 0 {
		return []string{"default"}
	}
	return r.Relations
}

// relationApplies reports whether midPoint applies a rule to a relation: one
// it names, or any when it names none (#all and #modify name none).
func relationApplies(r AssignRule, rel string) bool {
	return len(r.Relations) == 0 || contains(r.Relations, rel)
}

// mayHide is the relations a role the requester can't see might be requested
// with: those of every rule that allows something for the requestee, since
// the preview can't tell which roles those rules name (a rule's roles are
// searched as the requester, as midPoint's own catalog does).
func (p *Preview) mayHide(ctx context.Context, who person) []string {
	var out []string
	for _, rule := range p.Rules {
		if rule.Deny || !p.coversPerson(ctx, rule, who).yes {
			continue
		}
		for _, rel := range offeredRelations(rule) {
			if !contains(out, rel) {
				out = append(out, rel)
			}
		}
	}
	return out
}

// canSee reports whether the requester can search for a role at all.
func (p *Preview) canSee(ctx context.Context, roleOID string) bool {
	raws, err := p.c.searchUpTo(ctx, collRoles, ". inOid ("+quoteQueryString(roleOID)+")", 1)
	return err == nil && len(raws) > 0
}

// ruleTargets asks midPoint which roles a rule's "which" selectors name.
// #all and #modify on assignments name every role the requester can see.
func (p *Preview) ruleTargets(ctx context.Context, rule AssignRule, only, query string) (map[string]RoleSummary, bool, bool, error) {
	if rule.Kind != ruleAssign || len(rule.Target) == 0 {
		return p.searchRoles(ctx, only, query)
	}
	out := map[string]RoleSummary{}
	sure, truncated := true, false
	for _, s := range rule.Target {
		filter, ok, none := p.roleFilter(s)
		if none {
			continue
		}
		if !ok {
			filter, sure = "", false
		}
		if only != "" {
			filter = strings.TrimPrefix(filter+" and "+only, " and ")
		}
		set, _, t, err := p.searchRoles(ctx, filter, query)
		if err != nil {
			return nil, false, false, err
		}
		truncated = truncated || t
		for k, v := range set {
			out[k] = v
		}
	}
	return out, sure, truncated, nil
}

// roleFilter writes one "which" selector as a query on roles. none means it
// names no role (another type, the requester, a case clause); !ok means the
// preview can't write it, so every role the requester can see counts.
func (p *Preview) roleFilter(s Selector) (filter string, ok, none bool) {
	if s.Never || s.Self || !roleTypes[s.Type] {
		return "", true, true
	}
	if len(s.Unsupported) > 0 || len(s.FilterRaw) > 0 || s.OrgRelation != nil {
		return "", false, false
	}
	var clauses []string
	if s.FilterText != "" {
		text, ok := p.substituteSubject(s.FilterText)
		if !ok {
			return "", false, false
		}
		clauses = append(clauses, "("+text+")")
	}
	if len(s.Archetypes) > 0 {
		var any []string
		for _, a := range s.Archetypes {
			any = append(any, "archetypeRef matches (oid = "+quoteQueryString(a)+")")
		}
		clauses = append(clauses, "("+strings.Join(any, " or ")+")")
	}
	if s.Org != "" {
		clauses = append(clauses, ". inOrg "+quoteQueryString(s.Org))
	}
	if s.Subtype != "" {
		clauses = append(clauses, "subtype = "+quoteQueryString(s.Subtype))
	}
	if s.SameTenant {
		if p.Me.Tenant == "" {
			return "", true, true
		}
		clauses = append(clauses, "tenantRef matches (oid = "+quoteQueryString(p.Me.Tenant)+")")
	}
	if s.OwnerSelf {
		var owned []string
		for _, m := range p.Me.Memberships {
			if localName(m.Relation) == "owner" {
				owned = append(owned, quoteQueryString(m.OID))
			}
		}
		if len(owned) == 0 {
			return "", true, true
		}
		clauses = append(clauses, ". inOid ("+strings.Join(owned, ", ")+")")
	}
	return strings.Join(clauses, " and "), true, false
}

// searchRoles searches roles as the requester: what midPoint lets them see,
// as its own catalog does.
func (p *Preview) searchRoles(ctx context.Context, filter, query string) (map[string]RoleSummary, bool, bool, error) {
	clauses := []string{}
	if filter != "" {
		clauses = append(clauses, filter)
	}
	if query = strings.TrimSpace(query); query != "" {
		q := quoteQueryString(query)
		clauses = append(clauses, "(name contains[origIgnoreCase] "+q+" or displayName contains[origIgnoreCase] "+q+")")
	}
	raws, err := p.c.searchUpTo(ctx, collRoles, strings.Join(clauses, " and "), catalogMax)
	if err != nil {
		return nil, false, false, err
	}
	out := map[string]RoleSummary{}
	for _, raw := range raws {
		var r roleJSON
		if json.Unmarshal(raw, &r) == nil && r.OID != "" {
			out[r.OID] = r.summary()
		}
	}
	return out, true, len(raws) == catalogMax, nil
}

// --- who for ---

// OfferedPerson is someone the requester may request for.
type OfferedPerson struct {
	ObjectRef
	Because []string `json:"because"`
}

// PeopleOffer is the "who for" list.
type PeopleOffer struct {
	People       []OfferedPerson
	LimitReached bool
	Unsure       []string
}

// People lists whom the requester may request for, matching an optional name
// query: the requester themselves first, when a rule allows it. It follows
// midPoint's person search for Request access: #assign and #all rules
// without orderConstraints; a rule's target and relations don't matter here.
func (p *Preview) People(ctx context.Context, query string, limit int) (PeopleOffer, error) {
	offer := PeopleOffer{Unsure: append([]string(nil), p.Unsure...)}
	because := map[string][]string{}
	people := map[string]ObjectRef{}
	deniedSet := map[string]bool{}
	for _, rule := range p.Rules {
		if rule.Kind == ruleModifyAssignments || (rule.OrderConstraints && rule.Kind != ruleAll) {
			continue
		}
		set, sure, truncated, err := p.ruleObjects(ctx, rule, query)
		if err != nil {
			return PeopleOffer{}, err
		}
		offer.LimitReached = offer.LimitReached || truncated
		if rule.Deny {
			if sure && len(rule.Target) == 0 && len(rule.Relations) == 0 && rule.Paths.all() {
				for oid := range set {
					deniedSet[oid] = true
				}
			}
			continue
		}
		if !sure {
			offer.Unsure = append(offer.Unsure, rule.Label()+": who it is for")
		}
		for oid, ref := range set {
			people[oid] = ref
			because[oid] = append(because[oid], rule.Label())
		}
	}
	for oid, ref := range people {
		if !deniedSet[oid] {
			offer.People = append(offer.People, OfferedPerson{ObjectRef: ref, Because: dedupe(because[oid])})
		}
	}
	sort.Slice(offer.People, func(i, j int) bool {
		a, b := offer.People[i], offer.People[j]
		if (a.OID == p.Me.OID) != (b.OID == p.Me.OID) {
			return a.OID == p.Me.OID
		}
		return strings.ToLower(a.DisplayName+a.Name) < strings.ToLower(b.DisplayName+b.Name)
	})
	if limit = clampLimit(limit); len(offer.People) > limit {
		offer.People, offer.LimitReached = offer.People[:limit], true
	}
	return offer, nil
}

// ruleObjects asks midPoint which people a rule's "who" selectors name.
func (p *Preview) ruleObjects(ctx context.Context, rule AssignRule, query string) (map[string]ObjectRef, bool, bool, error) {
	if len(rule.Object) == 0 {
		return p.searchPeople(ctx, "", query)
	}
	out := map[string]ObjectRef{}
	sure, truncated := true, false
	add := func(set map[string]ObjectRef, t bool) {
		truncated = truncated || t
		for k, v := range set {
			out[k] = v
		}
	}
	for _, s := range rule.Object {
		filters, ok, none := p.personFilters(s)
		if none {
			continue
		}
		if !ok {
			sure = false
			filters = []string{""}
		}
		for _, f := range filters {
			set, _, t, err := p.searchPeople(ctx, f, query)
			if err != nil {
				return nil, false, false, err
			}
			add(set, t)
		}
	}
	return out, sure, truncated, nil
}

// personFilters writes one "who" selector as queries on users (several for an
// orgRelation: one per org the requester holds).
func (p *Preview) personFilters(s Selector) (filters []string, ok, none bool) {
	if s.Never || !userTypes[s.Type] {
		return nil, true, true
	}
	if len(s.Unsupported) > 0 || len(s.FilterRaw) > 0 || s.OwnerSelf {
		return nil, false, false
	}
	var clauses []string
	if s.Self {
		clauses = append(clauses, ". inOid ("+quoteQueryString(p.Me.OID)+")")
	}
	if s.FilterText != "" {
		text, ok := p.substituteSubject(s.FilterText)
		if !ok {
			return nil, false, false
		}
		clauses = append(clauses, "("+text+")")
	}
	if len(s.Archetypes) > 0 {
		var any []string
		for _, a := range s.Archetypes {
			any = append(any, "archetypeRef matches (oid = "+quoteQueryString(a)+")")
		}
		clauses = append(clauses, "("+strings.Join(any, " or ")+")")
	}
	if s.Org != "" {
		clauses = append(clauses, ". inOrg "+quoteQueryString(s.Org))
	}
	if s.Subtype != "" {
		clauses = append(clauses, "subtype = "+quoteQueryString(s.Subtype))
	}
	if s.SameTenant {
		if p.Me.Tenant == "" {
			return nil, true, true
		}
		clauses = append(clauses, "tenantRef matches (oid = "+quoteQueryString(p.Me.Tenant)+")")
	}
	base := strings.Join(clauses, " and ")
	if s.OrgRelation == nil {
		return []string{base}, true, false
	}
	scope := ". inOrg "
	switch s.OrgRelation.Scope {
	case "", "allDescendants":
	case "directDescendants":
		scope = ". inOrg[ONE_LEVEL] "
	default:
		return nil, false, false
	}
	orgs := p.managedOrgs(*s.OrgRelation)
	if len(orgs) == 0 {
		return nil, true, true
	}
	for _, o := range orgs {
		f := scope + quoteQueryString(o)
		if base != "" {
			f += " and " + base
		}
		filters = append(filters, f)
	}
	return filters, true, false
}

// searchPeople searches users as the requester.
func (p *Preview) searchPeople(ctx context.Context, filter, query string) (map[string]ObjectRef, bool, bool, error) {
	clauses := []string{}
	if filter != "" {
		clauses = append(clauses, filter)
	}
	if query = strings.TrimSpace(query); query != "" {
		q := quoteQueryString(query)
		clauses = append(clauses, "(name contains[origIgnoreCase] "+q+" or fullName contains[origIgnoreCase] "+q+")")
	}
	raws, err := p.c.searchUpTo(ctx, collUsers, strings.Join(clauses, " and "), catalogMax)
	if err != nil {
		return nil, false, false, err
	}
	out := map[string]ObjectRef{}
	for _, raw := range raws {
		var u userJSON
		if json.Unmarshal(raw, &u) == nil && u.OID != "" {
			out[u.OID] = ObjectRef{OID: u.OID, Type: "User", Name: u.Name.value(), DisplayName: u.FullName.value()}
		}
	}
	return out, true, len(raws) == catalogMax, nil
}

// --- small helpers ---

func boolMatch(b bool) match {
	if b {
		return matchYes
	}
	return matchNo
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func overlaps(a, b []string) bool {
	for _, x := range a {
		if contains(b, x) {
			return true
		}
	}
	return false
}

func dedupe(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range list {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
