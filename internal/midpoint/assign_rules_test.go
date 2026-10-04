package midpoint

import (
	"encoding/json"
	"reflect"
	"testing"
)

// endUserAuthorizations is End user's request rule and a read rule, as a
// person read them over REST on midPoint 4.10.3 (descriptions cut).
const endUserAuthorizations = `[
 {"@id": 7, "name": "read-requestable-roles",
  "action": "http://midpoint.evolveum.com/xml/ns/public/security/authorization-model-3#read",
  "object": {"@id": 21, "type": "#RoleType", "filter": {"text": "requestable = true"}}},
 {"@id": 9, "name": "assign-requestable-roles",
  "action": "http://midpoint.evolveum.com/xml/ns/public/security/authorization-model-3#assign",
  "phase": "request", "object": {"@id": 22, "special": "self"},
  "target": {"@id": 23, "type": "#RoleType", "filter": {"text": "requestable = true"}},
  "relation": "org:default"}]`

func rulesOf(t *testing.T, authorizations string) []AssignRule {
	t.Helper()
	var raws flexSlice
	if err := json.Unmarshal([]byte(authorizations), &raws); err != nil {
		t.Fatal(err)
	}
	return parseRules(ObjectRef{OID: "role-1", Name: "end-user", DisplayName: "End user"}, raws)
}

// Only request rules are kept, in midPoint's words.
func TestParseRulesEndUser(t *testing.T) {
	rules := rulesOf(t, endUserAuthorizations)
	want := []AssignRule{{
		Role: ObjectRef{OID: "role-1", Name: "end-user", DisplayName: "End user"}, Name: "assign-requestable-roles", Kind: ruleAssign,
		Object: []Selector{{Self: true}}, Target: []Selector{{Type: "RoleType", FilterText: "requestable = true"}},
		Relations: []string{"default"}, Paths: newItemPaths(nil, nil),
	}}
	if !reflect.DeepEqual(rules, want) {
		t.Fatalf("rules\n got %+v\nwant %+v", rules, want)
	}
	if got := rules[0].Label(); got != "End user › assign-requestable-roles" {
		t.Errorf("label %q", got)
	}
}

// Deny, field lists, lists of actions, #all, #modify on assignments, the
// execution phase, orgRelation and the clauses the preview can't evaluate.
func TestParseRulesShapes(t *testing.T) {
	rules := rulesOf(t, `[
	 {"name": "manager", "action": ["http://midpoint.evolveum.com/xml/ns/public/security/authorization-model-3#read", "http://midpoint.evolveum.com/xml/ns/public/security/authorization-model-3#assign"],
	  "object": {"type": "UserType", "orgRelation": {"subjectRelation": "org:manager", "scope": "directDescendants"}},
	  "target": [{"type": "c:RoleType", "archetypeRef": [{"oid": "arch-1"}, {"oid": "arch-2"}]}, {"type": "#OrgType"}],
	  "relation": ["org:default", "http://midpoint.evolveum.com/xml/ns/public/common/org-3#approver"],
	  "exceptItem": ["c:assignment/c:activation"]},
	 {"name": "no-desc", "decision": "deny", "action": "http://midpoint.evolveum.com/xml/ns/public/security/authorization-model-3#assign",
	  "item": "assignment/description"},
	 {"name": "super", "action": "http://midpoint.evolveum.com/xml/ns/public/security/authorization-3#all"},
	 {"name": "modify-assignments", "action": "http://midpoint.evolveum.com/xml/ns/public/security/authorization-model-3#modify", "item": ["assignment"]},
	 {"name": "modify-names", "action": "http://midpoint.evolveum.com/xml/ns/public/security/authorization-model-3#modify", "item": ["name"]},
	 {"name": "execution", "action": "http://midpoint.evolveum.com/xml/ns/public/security/authorization-model-3#assign", "phase": "execution"},
	 {"name": "odd", "action": "http://midpoint.evolveum.com/xml/ns/public/security/authorization-model-3#assign", "orderConstraints": {"order": 1},
	  "object": {"roleRelation": {"subjectRelation": "org:owner"}}, "target": {"requester": {"special": "self"}}}]`)
	names := map[string]AssignRule{}
	for _, r := range rules {
		names[r.Name] = r
	}
	if len(rules) != 5 || names["modify-names"].Name != "" || names["execution"].Name != "" {
		t.Fatalf("rules %+v", rules)
	}
	m := names["manager"]
	if m.Kind != ruleAssign || !reflect.DeepEqual(m.Relations, []string{"default", "approver"}) ||
		!reflect.DeepEqual(m.Object, []Selector{{Type: "UserType", OrgRelation: &OrgRelation{SubjectRelations: []string{"manager"}, Scope: "directDescendants"}}}) ||
		!reflect.DeepEqual(m.Target, []Selector{{Type: "RoleType", Archetypes: []string{"arch-1", "arch-2"}}, {Type: "OrgType"}}) {
		t.Errorf("manager %+v", m)
	}
	if m.Paths.includes("assignment/activation/validTo") || !m.Paths.includes("assignment/targetRef") {
		t.Errorf("manager paths %+v", m.Paths)
	}
	if d := names["no-desc"]; !d.Deny || !d.Paths.includes("assignment/description") || d.Paths.includes("assignment/targetRef") {
		t.Errorf("deny %+v", d)
	}
	if names["super"].Kind != ruleAll || names["modify-assignments"].Kind != ruleModifyAssignments {
		t.Errorf("kinds %+v %+v", names["super"], names["modify-assignments"])
	}
	odd := names["odd"]
	if !odd.OrderConstraints || !reflect.DeepEqual(odd.Object[0].Unsupported, []string{"roleRelation"}) || !odd.Target[0].Never {
		t.Errorf("odd %+v", odd)
	}
}

// Item lists cover their paths and everything under them; except lists cover
// everything not above, at or under them; no list covers all.
func TestItemPaths(t *testing.T) {
	for _, tc := range []struct {
		items, except []string
		path          string
		want          bool
	}{
		{nil, nil, "assignment/targetRef", true},
		{[]string{"assignment"}, nil, "assignment/extension/costCenter", true},
		{[]string{"assignment/targetRef"}, nil, "assignment/activation/validTo", false},
		{[]string{"assignment/targetRef"}, nil, "assignment", false},
		{nil, []string{"assignment/activation"}, "assignment/activation/validTo", false},
		{nil, []string{"assignment/activation"}, "assignment", false},
		{nil, []string{"assignment/activation"}, "assignment/targetRef", true},
		{[]string{"c:assignment/ext:costCenter"}, nil, "assignment/costCenter", true},
		{[]string{"assignment/target"}, nil, "assignment/targetRef", false},
	} {
		if got := newItemPaths(tc.items, tc.except).includes(tc.path); got != tc.want {
			t.Errorf("items %v except %v: includes(%q) = %v", tc.items, tc.except, tc.path, got)
		}
	}
}
