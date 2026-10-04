package midpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// RequestCatalog is a requestable-role snapshot, with the target and paging hint.
type RequestCatalog struct {
	Roles        []RoleSummary
	ForUserRef   *ObjectRef
	LimitReached bool
	Query        string
}

// RequestableCatalog searches the caller-visible catalog, then excludes the
// explicit target's existing roles, preserving the existing self-list semantics.
func (c *Client) RequestableCatalog(ctx context.Context, target, query string, limit int) (RequestCatalog, error) {
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) > 100 {
		return RequestCatalog{}, &CodedError{Code: CodeInvalidInput, Err: fmt.Errorf("query must have at most 100 characters")}
	}
	filter := "requestable = true"
	if query != "" {
		v := quoteQueryString(query)
		// Not description: midPoint 4.10's repository can't search it (live on
		// 4.10.3: HTTP 500, "Missing item mapping for 'description'").
		filter += fmt.Sprintf(" and (name contains[origIgnoreCase] %[1]s or displayName contains[origIgnoreCase] %[1]s)", v)
	}
	roles, err := c.listRoles(ctx, filter, limit)
	if err != nil {
		return RequestCatalog{}, err
	}
	out := RequestCatalog{Roles: roles, LimitReached: len(roles) == clampLimit(limit), Query: query}
	if target == "" {
		return out, nil
	}
	var u userJSON
	err = c.getObject(ctx, collUsers, target, true, &u)
	if err != nil {
		return RequestCatalog{}, err
	}
	out.ForUserRef = &ObjectRef{OID: target, Type: "User", Name: u.Name.value(), DisplayName: u.FullName.value()}
	held := map[string]bool{}
	for _, raw := range u.RoleMembershipRef {
		var ref refJSON
		if json.Unmarshal(raw, &ref) == nil && ref.OID != "" {
			held[ref.OID] = true
		}
	}
	out.Roles = []RoleSummary{}
	for _, r := range roles {
		if !held[r.OID] {
			out.Roles = append(out.Roles, r)
		}
	}
	// Keep the upstream paging hint: filtering held roles cannot prove that the
	// catalog was complete. Otherwise automatic search would hide later roles.
	return out, nil
}

// NamedRef names one object, read as the caller, the way RequestRefs does.
func (c *Client) NamedRef(ctx context.Context, oid, typ string) ObjectRef {
	return newRefReader(c).objectRef(ctx, refJSON{OID: oid, Type: typ}, typ == "UserType")
}

// RequestRefs resolves request labels best-effort as the caller.
func (c *Client) RequestRefs(ctx context.Context, userOID, roleOID string) (ObjectRef, ObjectRef) {
	r := newRefReader(c)
	return r.objectRef(ctx, refJSON{OID: userOID, Type: "UserType"}, true), r.objectRef(ctx, refJSON{OID: roleOID, Type: "RoleType"}, false)
}

// CatalogBasis says how a catalog or a "who for" list was chosen (D44).
type CatalogBasis struct {
	Basis  string   `json:"basis" jsonschema:"rules: from midPoint's request rules for the acting person, read by the server's account (D44); requestable: the roles flagged requestable, because the rules could not be read"`
	Reason string   `json:"reason,omitempty" jsonschema:"why the rules were not used"`
	Unsure []string `json:"unsure,omitempty" jsonschema:"rules the preview could not evaluate exactly: it offers more there, and midPoint decides on submit"`
}

// CatalogResult is the roles the acting person may request for a target.
type CatalogResult struct {
	Basis        CatalogBasis
	Roles        []OfferedRole
	ForUserRef   *ObjectRef
	LimitReached bool
	Query        string
}

// RequestCatalogFor is the catalog of roles the acting person may request for
// target (themselves when ""): from midPoint's request rules when the server
// can read them (D44), else the roles flagged requestable, as before.
func (c *Client) RequestCatalogFor(ctx context.Context, target, query string, limit int) (CatalogResult, error) {
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) > 100 {
		return CatalogResult{}, &CodedError{Code: CodeInvalidInput, Err: fmt.Errorf("query must have at most 100 characters")}
	}
	p, err := c.RequestPreview(ctx)
	if errors.Is(err, ErrPreviewUnavailable) {
		cat, err2 := c.RequestableCatalog(ctx, target, query, limit)
		if err2 != nil {
			return CatalogResult{}, err2
		}
		out := CatalogResult{Basis: CatalogBasis{Basis: "requestable", Reason: previewReason(err)}, ForUserRef: cat.ForUserRef, LimitReached: cat.LimitReached, Query: cat.Query}
		for _, r := range cat.Roles {
			out.Roles = append(out.Roles, OfferedRole{RoleSummary: r})
		}
		return out, nil
	}
	if err != nil {
		return CatalogResult{}, err
	}
	who, err := p.Person(ctx, target)
	if err != nil {
		return CatalogResult{}, err
	}
	offer, err := p.RolesFor(ctx, who, query, limit)
	if err != nil {
		return CatalogResult{}, err
	}
	out := CatalogResult{Basis: CatalogBasis{Basis: "rules", Unsure: dedupe(offer.Unsure)}, Roles: offer.Roles, LimitReached: offer.LimitReached, Query: query}
	if target != "" {
		ref := who.ref()
		out.ForUserRef = &ref
	}
	if out.Roles == nil {
		out.Roles = []OfferedRole{}
	}
	return out, nil
}

// TargetsResult is whom the acting person may request for.
type TargetsResult struct {
	Basis        CatalogBasis
	People       []OfferedPerson
	LimitReached bool
	Query        string
}

// RequestTargets lists whom the acting person may request for (D44). Without
// the rules, it is the person alone.
func (c *Client) RequestTargets(ctx context.Context, query string, limit int) (TargetsResult, error) {
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) > 100 {
		return TargetsResult{}, &CodedError{Code: CodeInvalidInput, Err: fmt.Errorf("query must have at most 100 characters")}
	}
	p, err := c.RequestPreview(ctx)
	if errors.Is(err, ErrPreviewUnavailable) {
		self, err2 := c.Self(ctx)
		if err2 != nil {
			return TargetsResult{}, err2
		}
		return TargetsResult{Basis: CatalogBasis{Basis: "requestable", Reason: previewReason(err)}, Query: query,
			People: []OfferedPerson{{ObjectRef: ObjectRef{OID: self.OID, Type: "User", Name: self.Name, DisplayName: self.FullName}}}}, nil
	}
	if err != nil {
		return TargetsResult{}, err
	}
	offer, err := p.People(ctx, query, limit)
	if err != nil {
		return TargetsResult{}, err
	}
	out := TargetsResult{Basis: CatalogBasis{Basis: "rules", Unsure: dedupe(offer.Unsure)}, People: offer.People, LimitReached: offer.LimitReached, Query: query}
	if out.People == nil {
		out.People = []OfferedPerson{}
	}
	return out, nil
}

// CheckRequestOffer refuses a request the acting person's rules don't offer
// (D44): the role for that target, and a relation other than member only
// where a rule names it (D45). Without the rules, the requestable check
// stands and only member may be requested.
func (c *Client) CheckRequestOffer(ctx context.Context, target, roleOID, relation string) error {
	relation = localName(relation)
	if relation == "" {
		relation = "default"
	}
	p, err := c.RequestPreview(ctx)
	if errors.Is(err, ErrPreviewUnavailable) {
		if relation != "default" {
			return &CodedError{Code: CodeNotRequestable, Err: fmt.Errorf("role %s is not offered as %s: only member can be requested while midPoint's request rules can't be read (%s)", roleOID, relation, previewReason(err))}
		}
		return c.EnsureRequestable(ctx, roleOID)
	}
	if err != nil {
		return err
	}
	if !c.cfg.File.Requests.RequestableRequired() && relation == "default" {
		return nil
	}
	who, err := p.Person(ctx, target)
	if err != nil {
		return err
	}
	offer, _, ok, err := p.RoleFor(ctx, who, roleOID)
	if err != nil {
		return err
	}
	offered := offer.Relations()
	if !ok && !p.canSee(ctx, roleOID) {
		// The requester can't search for the role, so the preview can't tell
		// which rules name it: any relation a rule allows for this person may
		// be right, and midPoint decides (live on 4.10.3: End user searches
		// only requestable roles, yet a rule may let it assign others).
		offered = p.mayHide(ctx, who)
		ok = len(offered) > 0
	}
	if !ok {
		return &CodedError{Code: CodeNotRequestable, Err: fmt.Errorf("role %s is not offered for %s by midPoint's request rules", roleOID, who.Name)}
	}
	if !contains(offered, relation) {
		return &CodedError{Code: CodeNotRequestable, Err: fmt.Errorf("role %s is not offered as %s for %s by midPoint's request rules, which offer only %s", roleOID, relation, who.Name, strings.Join(offered, ", "))}
	}
	return nil
}

// previewReason is why the preview is off, without the sentinel's words.
func previewReason(err error) string {
	return strings.TrimPrefix(err.Error(), ErrPreviewUnavailable.Error()+": ")
}
