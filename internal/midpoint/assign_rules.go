package midpoint

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Request rules (D44): the midPoint authorizations that decide what a person
// may request, read as midPoint wrote them.
//
// midPoint enforces every request; nothing in this file grants anything. The
// request access preview only uses these rules to decide what a view offers.
// The semantics follow midPoint 4.10's own server-side check
// (ClockworkRequestAuthorizer and SecurityEnforcerImpl), and the preview never
// offers more than midPoint's Request access page would (D45: a relation other
// than member only where a rule names it).

const (
	authzModelNS = "http://midpoint.evolveum.com/xml/ns/public/security/authorization-model-3"
	actionAll    = "http://midpoint.evolveum.com/xml/ns/public/security/authorization-3#all"
	actionAssign = authzModelNS + "#assign"
	actionModify = authzModelNS + "#modify"
)

// ruleKind is what a request rule lets a person do with assignments.
type ruleKind int

const (
	// ruleAssign is #assign: assign the targets its target selectors name.
	ruleAssign ruleKind = iota + 1
	// ruleModifyAssignments is #modify that covers the assignment item:
	// change any assignment, which midPoint treats as assigning anything.
	ruleModifyAssignments
	// ruleAll is #all, the superuser action.
	ruleAll
)

// AssignRule is one request rule: an authorization of a role (or org,
// service, archetype) that holds #assign, #modify on assignments or #all, for
// the request phase. Its parts keep midPoint's own words.
type AssignRule struct {
	Role ObjectRef // the role that carries the authorization
	Name string    // the authorization's name, "" when it has none
	Deny bool      // decision deny
	Kind ruleKind
	// Object selectors say who the assignment may be for; none means anyone.
	Object []Selector
	// Target selectors say what may be assigned; none means anything.
	Target []Selector
	// Relations are the relation local names the rule lists; none listed
	// means midPoint accepts any relation.
	Relations []string
	// Paths are the assignment items the rule covers (item / exceptItem).
	Paths itemPaths
	// OrderConstraints marks a rule with orderConstraints, which midPoint's
	// person search leaves out.
	OrderConstraints bool
}

// Label names the rule for "because" lines: "End user › assign-requestable-roles".
func (r AssignRule) Label() string {
	role := r.Role.DisplayName
	if role == "" {
		role = r.Role.Name
	}
	if role == "" {
		role = r.Role.OID
	}
	name := r.Name
	if name == "" {
		name = "unnamed rule"
	}
	return role + " › " + name
}

// Selector is one object or target selector of a rule. Its clauses must all
// hold (AND); a rule's several selectors are alternatives (OR).
type Selector struct {
	Type        string   // "RoleType", "UserType", … ("" = any type)
	Self        bool     // special self: the requester
	FilterText  string   // a filter in midPoint's query language
	FilterRaw   []byte   // a structured (non-text) filter
	Archetypes  []string // archetypeRef OIDs, any of them
	Org         string   // orgRef: in this org's subtree
	OrgRelation *OrgRelation
	Subtype     string
	SameTenant  bool // tenant sameAsSubject
	OwnerSelf   bool // owner {special self}
	// Never marks a clause that never matches a user or a role (requester,
	// assignee, candidateAssignee, relatedObject, parent: case and task
	// clauses).
	Never bool
	// Unsupported names clauses the preview can't evaluate. A rule with one
	// is "unsure": treated as matching for allows and ignored for denies.
	Unsupported []string
}

// OrgRelation is midPoint's orgRelation clause: objects in the orgs where the
// requester holds one of SubjectRelations, at Scope.
type OrgRelation struct {
	SubjectRelations []string // relation local names; empty = default
	Scope            string   // allDescendants (default), directDescendants, allAncestors
}

// authorizationJSON is AuthorizationType as midPoint's REST API writes it.
type authorizationJSON struct {
	Name             string          `json:"name"`
	Decision         string          `json:"decision"`
	Action           flexStrings     `json:"action"`
	Phase            string          `json:"phase"`
	Object           flexSlice       `json:"object"`
	Target           flexSlice       `json:"target"`
	Relation         flexStrings     `json:"relation"`
	Item             flexStrings     `json:"item"`
	ExceptItem       flexStrings     `json:"exceptItem"`
	OrderConstraints json.RawMessage `json:"orderConstraints"`
}

// parseRules returns the request rules among a role's authorizations.
// Authorizations for other actions, or for the execution phase only, are not
// request rules and are left out.
func parseRules(role ObjectRef, authorizations flexSlice) []AssignRule {
	var out []AssignRule
	for _, raw := range authorizations {
		var a authorizationJSON
		if json.Unmarshal(raw, &a) != nil {
			continue
		}
		if a.Phase != "" && a.Phase != "request" {
			continue
		}
		paths := newItemPaths(a.Item, a.ExceptItem)
		kind := ruleKindOf(a.Action, paths)
		if kind == 0 {
			continue
		}
		rule := AssignRule{
			Role:             role,
			Name:             strings.TrimSpace(a.Name),
			Deny:             a.Decision == "deny",
			Kind:             kind,
			Paths:            paths,
			OrderConstraints: len(bytes.TrimSpace(a.OrderConstraints)) > 0 && string(bytes.TrimSpace(a.OrderConstraints)) != "null",
		}
		for _, r := range a.Relation {
			rule.Relations = append(rule.Relations, localName(r))
		}
		rule.Object = parseSelectors(a.Object)
		rule.Target = parseSelectors(a.Target)
		out = append(out, rule)
	}
	return out
}

// ruleKindOf picks the request action of an authorization: #all, #assign, or
// #modify where its items cover the assignment item. 0 means none.
func ruleKindOf(actions []string, paths itemPaths) ruleKind {
	kind := ruleKind(0)
	for _, a := range actions {
		switch a {
		case actionAll:
			return ruleAll
		case actionAssign:
			kind = ruleAssign
		case actionModify:
			if kind == 0 && paths.includes("assignment") {
				kind = ruleModifyAssignments
			}
		}
	}
	return kind
}

// selectorJSON is OwnedObjectSelectorType as midPoint's REST API writes it.
type selectorJSON struct {
	Type         string          `json:"type"`
	Special      flexStrings     `json:"special"`
	Filter       json.RawMessage `json:"filter"`
	ArchetypeRef flexSlice       `json:"archetypeRef"`
	OrgRef       json.RawMessage `json:"orgRef"`
	OrgRelation  *struct {
		SubjectRelation flexStrings `json:"subjectRelation"`
		Scope           string      `json:"scope"`
	} `json:"orgRelation"`
	Subtype string `json:"subtype"`
	Tenant  *struct {
		SameAsSubject bool `json:"sameAsSubject"`
	} `json:"tenant"`
	Owner             json.RawMessage `json:"owner"`
	Delegator         json.RawMessage `json:"delegator"`
	RoleRelation      json.RawMessage `json:"roleRelation"`
	Requester         json.RawMessage `json:"requester"`
	Assignee          json.RawMessage `json:"assignee"`
	CandidateAssignee json.RawMessage `json:"candidateAssignee"`
	RelatedObject     json.RawMessage `json:"relatedObject"`
	Parent            json.RawMessage `json:"parent"`
}

func parseSelectors(raws flexSlice) []Selector {
	var out []Selector
	for _, raw := range raws {
		var j selectorJSON
		if json.Unmarshal(raw, &j) != nil {
			out = append(out, Selector{Unsupported: []string{"unreadable selector"}})
			continue
		}
		s := Selector{Type: localName(strings.TrimPrefix(j.Type, "#")), Subtype: j.Subtype}
		for _, sp := range j.Special {
			if sp == "self" {
				s.Self = true
			} else {
				s.Unsupported = append(s.Unsupported, "special "+sp)
			}
		}
		if f := bytes.TrimSpace(j.Filter); len(f) > 0 && string(f) != "null" {
			var text struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(f, &text) == nil && text.Text != "" {
				s.FilterText = text.Text
			} else {
				s.FilterRaw = f
			}
		}
		for _, r := range j.ArchetypeRef {
			var ref refJSON
			if json.Unmarshal(r, &ref) == nil && ref.OID != "" {
				s.Archetypes = append(s.Archetypes, ref.OID)
			}
		}
		if len(bytes.TrimSpace(j.OrgRef)) > 0 {
			var ref refJSON
			if json.Unmarshal(j.OrgRef, &ref) == nil && ref.OID != "" {
				s.Org = ref.OID
			} else {
				s.Unsupported = append(s.Unsupported, "orgRef")
			}
		}
		if j.OrgRelation != nil {
			or := &OrgRelation{Scope: j.OrgRelation.Scope}
			for _, r := range j.OrgRelation.SubjectRelation {
				or.SubjectRelations = append(or.SubjectRelations, localName(r))
			}
			s.OrgRelation = or
		}
		if j.Tenant != nil {
			if j.Tenant.SameAsSubject {
				s.SameTenant = true
			} else {
				s.Unsupported = append(s.Unsupported, "tenant")
			}
		}
		if len(j.Owner) > 0 {
			var owner struct {
				Special flexStrings `json:"special"`
			}
			if json.Unmarshal(j.Owner, &owner) == nil && len(owner.Special) == 1 && owner.Special[0] == "self" {
				s.OwnerSelf = true
			} else {
				s.Unsupported = append(s.Unsupported, "owner")
			}
		}
		if len(j.Delegator) > 0 {
			s.Unsupported = append(s.Unsupported, "delegator")
		}
		if len(j.RoleRelation) > 0 {
			s.Unsupported = append(s.Unsupported, "roleRelation")
		}
		if len(j.Requester) > 0 || len(j.Assignee) > 0 || len(j.CandidateAssignee) > 0 || len(j.RelatedObject) > 0 || len(j.Parent) > 0 {
			s.Never = true
		}
		out = append(out, s)
	}
	return out
}

// localName drops a namespace prefix ("org:approver", "c:RoleType") or URI
// ("http://…#approver") from a QName.
func localName(q string) string {
	q = strings.TrimSpace(q)
	if i := strings.LastIndexAny(q, "#:"); i >= 0 {
		return q[i+1:]
	}
	return q
}

// itemPaths is a rule's item / exceptItem list, as midPoint's
// PositiveNegativeItemPaths reads it: no list means every item, an item list
// covers those paths and everything under them, an exceptItem list covers
// every path that is not one of them, above them or under them.
type itemPaths struct {
	items  []string
	except []string
}

func newItemPaths(items, except []string) itemPaths {
	return itemPaths{items: cleanPaths(items), except: cleanPaths(except)}
}

// all reports whether the rule covers every item.
func (p itemPaths) all() bool { return len(p.items) == 0 && len(p.except) == 0 }

// includes reports whether the rule covers path ("assignment/targetRef").
func (p itemPaths) includes(path string) bool {
	switch {
	case p.all():
		return true
	case len(p.items) > 0:
		for _, i := range p.items {
			if pathPrefix(i, path) {
				return true
			}
		}
		return false
	default:
		for _, e := range p.except {
			if pathPrefix(e, path) || pathPrefix(path, e) {
				return false
			}
		}
		return true
	}
}

// pathPrefix reports whether prefix equals path or is one of its ancestors.
func pathPrefix(prefix, path string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// cleanPaths writes item paths without namespace prefixes:
// "c:assignment/ext:costCenter" becomes "assignment/costCenter".
func cleanPaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		var segs []string
		for _, s := range strings.Split(strings.TrimSpace(p), "/") {
			if s = localName(s); s != "" {
				segs = append(segs, s)
			}
		}
		if len(segs) > 0 {
			out = append(out, strings.Join(segs, "/"))
		}
	}
	return out
}

// flexStrings decodes a value midPoint writes as one string or a list.
type flexStrings []string

func (f *flexStrings) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '[' {
		var list []string
		if err := json.Unmarshal(data, &list); err != nil {
			return err
		}
		*f = list
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*f = flexStrings{s}
	return nil
}
