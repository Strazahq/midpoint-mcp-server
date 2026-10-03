package midpoint

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

// ExpiringAssignment is one assignment that ends soon.
type ExpiringAssignment struct {
	User     ObjectRef `json:"user" jsonschema:"whose access ends"`
	Target   ObjectRef `json:"target" jsonschema:"what ends: the assigned role, org or service"`
	ValidTo  string    `json:"validTo" jsonschema:"when it ends (RFC 3339)"`
	DaysLeft int       `json:"daysLeft" jsonschema:"whole days until it ends; 0 when it ends within 24 hours"`
	Status   string    `json:"status,omitempty" jsonschema:"the assignment's activation status"`
}

// ExpiringAccess lists the direct assignments of the given users that end
// between now and now+days, read as the caller, soonest first. Users that
// can't be read are returned by OID instead of failing the whole answer.
func (c *Client) ExpiringAccess(ctx context.Context, userOIDs []string, days int, now time.Time) ([]ExpiringAssignment, []string) {
	until := now.Add(time.Duration(days) * 24 * time.Hour)
	r := newRefReader(c)
	out := []ExpiringAssignment{}
	var unreadable []string
	for _, oid := range userOIDs {
		var u userJSON
		if err := c.getObject(ctx, collUsers, oid, true, &u); err != nil {
			unreadable = append(unreadable, oid)
			continue
		}
		user := ObjectRef{OID: oid, Type: "User", Name: u.Name.value(), DisplayName: u.FullName.value()}
		for _, a := range u.assignments() {
			if a.TargetRef == nil || a.Activation == nil || a.Activation.ValidTo == "" {
				continue
			}
			end, err := time.Parse(time.RFC3339, a.Activation.ValidTo)
			if err != nil || end.Before(now) || end.After(until) {
				continue
			}
			out = append(out, ExpiringAssignment{
				User:     user,
				Target:   r.objectRef(ctx, *a.TargetRef, false),
				ValidTo:  a.Activation.ValidTo,
				DaysLeft: int(end.Sub(now).Hours() / 24),
				Status:   a.Activation.status(),
			})
		}
	}
	at := func(s string) time.Time { t, _ := time.Parse(time.RFC3339, s); return t }
	sort.SliceStable(out, func(i, j int) bool { return at(out[i].ValidTo).Before(at(out[j].ValidTo)) })
	return out, unreadable
}

// assignments decodes a user's assignment values, skipping any it can't read.
func (u userJSON) assignments() []assignmentJSON {
	out := make([]assignmentJSON, 0, len(u.Assignment))
	for _, raw := range u.Assignment {
		var a assignmentJSON
		if json.Unmarshal(raw, &a) == nil {
			out = append(out, a)
		}
	}
	return out
}
