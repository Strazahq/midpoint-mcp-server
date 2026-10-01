package midpoint

import (
	"context"
	"encoding/json"
	"fmt"
)

// assignmentSubjectRelation classifies the subject using selected manager links
// and the subject's effective parentOrgRef membership, without an extra team search.
func (c *Client) assignmentSubjectRelation(ctx context.Context, u userJSON) string {
	self, err := c.selfUser(ctx)
	if err != nil {
		return "other"
	}
	if u.OID != "" && u.OID == self.OID {
		return "self"
	}
	team := c.teamConfig()
	managed := map[string]bool{}
	for _, l := range orgLinks(callerOrgs(self, team), team) {
		if l.Manager && l.Selected {
			managed[l.OID] = true
		}
	}
	for _, org := range u.parentOrgs() {
		rel := relationLocal(org.Relation)
		if rel == "" {
			rel = relationDefault
		}
		if managed[org.OID] && rel == team.memberRelation() {
			return "direct-report"
		}
	}
	return "other"
}

// Revocation reports the observed result of deleting a direct assignment.
type Revocation struct {
	Role    ObjectRef `json:"role"`
	User    ObjectRef `json:"user"`
	Outcome string    `json:"outcome" jsonschema:"removed, pending-approval, still-assigned or preview"`
	CaseOID string    `json:"caseOid,omitempty"`
}

// ReadRevocation reads the user after a removal, or names a preview. A write's
// HTTP success is not proof that the assignment disappeared. If the user cannot
// be read, the error is returned instead of claiming an outcome.
func (c *Client) ReadRevocation(ctx context.Context, userOID, roleOID string, applied bool) (Revocation, error) {
	r := newRefReader(c)
	var u userJSON
	if err := c.getObject(ctx, collUsers, userOID, true, &u); err != nil {
		return Revocation{}, fmt.Errorf("reading removal outcome: %w", err)
	}
	out := Revocation{
		User:    ObjectRef{OID: userOID, Type: "User", Name: u.Name.value(), DisplayName: u.FullName.value()},
		Role:    r.objectRef(ctx, refJSON{OID: roleOID, Type: "RoleType"}, false),
		Outcome: "preview",
	}
	if !applied {
		return out, nil
	}
	assigned := false
	for _, raw := range u.Assignment {
		var a assignmentJSON
		if err := json.Unmarshal(raw, &a); err != nil {
			return Revocation{}, fmt.Errorf("decoding removal outcome: %w", err)
		}
		if a.TargetRef != nil && a.TargetRef.OID == roleOID {
			assigned = true
		}
	}
	out.Outcome = "removed"
	if !assigned {
		return out, nil
	}
	out.Outcome = "still-assigned"
	// Case discovery is best-effort, as for request_role: a caller may be able
	// to read a report but not its cases. Never infer pending from an add case.
	filter := fmt.Sprintf(`objectRef matches (oid = %s) and targetRef matches (oid = %s) and (state = "open" or state = "created")`, quoteQueryString(userOID), quoteQueryString(roleOID))
	raws, err := c.searchRaw(ctx, collCases, filter, maxLimit)
	if err != nil {
		return out, nil
	}
	for _, raw := range raws {
		var cj caseJSON
		if json.Unmarshal(raw, &cj) != nil {
			continue
		}
		if cj.OID != "" && refOID(cj.ObjectRef) == userOID && refOID(cj.TargetRef) == roleOID && (cj.State == "open" || cj.State == "created") && cj.approval().change == ChangeDelete {
			out.Outcome, out.CaseOID = "pending-approval", cj.OID
			break
		}
	}
	return out, nil
}
