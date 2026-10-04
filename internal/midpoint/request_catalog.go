package midpoint

import (
	"context"
	"encoding/json"
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
