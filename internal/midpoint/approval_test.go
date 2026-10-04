package midpoint

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The fixtures below were recorded from midPoint 4.10.3 and made neutral:
//
//   - cases_search_approver.json: list_work_items' search as an approver
//     holding only the stock End user and Approver roles. A request for
//     db-admin with a validity and a justification, two steps (two named
//     approvers who must both agree, then the role's approvers); the reader
//     sees only its own work item.
//   - cases_search_delegate.json: the same search as a manager. The two-step
//     case's other item, delegated to the manager (two assignees), and a
//     request decided by the requestee's managers (one step, firstDecides,
//     approvers computed by an expression).
//   - cases_search_removal.json: a request to remove that assignment.
//   - case_get_closed.json: the db-admin case read by its requester once
//     approved: every work item, with performers and comments.
//   - case_get_after_decision.json: the case read back by the first approver
//     right after deciding.

// justificationItem is the fixtures' justification item.
var justificationItem = QName{Namespace: "http://example.com/xml/ns/access-request", Local: "justification"}

// fixtureCases decodes the cases of a search fixture.
func fixtureCases(t *testing.T, name string) []caseJSON {
	t.Helper()
	raws, err := parseObjectList(fixture(t, name))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	out := make([]caseJSON, 0, len(raws))
	for _, raw := range raws {
		var cj caseJSON
		if err := json.Unmarshal(raw, &cj); err != nil {
			t.Fatalf("%s: decoding case: %v", name, err)
		}
		out = append(out, cj)
	}
	return out
}

// fixtureCase decodes a single-object GET fixture.
func fixtureCase(t *testing.T, name string) caseJSON {
	t.Helper()
	obj, err := unwrapObject(fixture(t, name))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var cj caseJSON
	if err := json.Unmarshal(obj, &cj); err != nil {
		t.Fatalf("%s: decoding case: %v", name, err)
	}
	return cj
}

var twoSteps = []StageInfo{
	{Number: 1, Count: 2, Name: "Team leads", Strategy: StrategyAllMustAgree},
	{Number: 2, Count: 2, Name: "Role approvers", Strategy: StrategyFirstDecides},
}

// details are a request's fields as a server without a request form lists
// them: by name, in midPoint's order.
func details(a approval) []RequestDetail { return (&Client{}).requestDetailsOf(a) }

var wantJustification = []RequestDetail{{Name: "justification", Values: []string{"Needed for the quarter-end close."}}}

func stageInfos(a approval) []StageInfo {
	out := []StageInfo{}
	for _, s := range a.stages {
		out = append(out, s.StageInfo)
	}
	return out
}

// A search result carries approvalContext: the parked assignment with its
// validity and extension item, the steps, and the creation time.
func TestApprovalFromSearch(t *testing.T) {
	cj := fixtureCases(t, "cases_search_approver.json")[0]
	a := cj.approval()
	if a.change != ChangeAdd {
		t.Errorf("change = %q, want add", a.change)
	}
	if got := details(a); !reflect.DeepEqual(got, wantJustification) {
		t.Errorf("request details = %+v, want %+v", got, wantJustification)
	}
	want := &Validity{ValidFrom: "2026-10-02T00:00:00+02:00", ValidTo: "2026-10-31T23:59:59+01:00"}
	if got := a.validity(); !reflect.DeepEqual(got, want) {
		t.Errorf("validity = %+v, want %+v", got, want)
	}
	if got := stageInfos(a); !reflect.DeepEqual(got, twoSteps) {
		t.Errorf("stages = %+v, want %+v", got, twoSteps)
	}
	s1, _ := a.stage(1)
	if len(s1.approvers) != 2 || s1.approvers[1].TargetName.value() != "mkovac" {
		t.Errorf("stage 1 approvers = %+v, want dlee and mkovac", s1.approvers)
	}
	if got := cj.createdAt(); got != "2026-10-01T10:53:58.855Z" {
		t.Errorf("createdAt = %q", got)
	}
	if cj.StageNumber != 1 {
		t.Errorf("stageNumber = %d, want 1", cj.StageNumber)
	}
}

// A single-object GET leaves the extension item's namespace out; the item
// still matches on its local name.
func TestApprovalFromGet(t *testing.T) {
	cj := fixtureCase(t, "case_get_closed.json")
	a := cj.approval()
	if a.change != ChangeAdd || !reflect.DeepEqual(details(a), wantJustification) {
		t.Errorf("change = %q, request details = %+v", a.change, details(a))
	}
	if got := stageInfos(a); !reflect.DeepEqual(got, twoSteps) {
		t.Errorf("stages = %+v", got)
	}
	if cj.CloseTimestamp != "2026-10-01T10:58:03.417Z" {
		t.Errorf("closeTimestamp = %q", cj.CloseTimestamp)
	}
}

// A one-step schema is a bare stage object, and expression-computed
// approvers leave approverRef empty.
func TestApprovalSingleStage(t *testing.T) {
	cases := fixtureCases(t, "cases_search_delegate.json")
	if len(cases) != 2 {
		t.Fatalf("cases = %d, want 2", len(cases))
	}
	a := cases[1].approval()
	want := []StageInfo{{Number: 1, Count: 1, Name: "Requestee's manager", Strategy: StrategyFirstDecides}}
	if got := stageInfos(a); !reflect.DeepEqual(got, want) {
		t.Errorf("stages = %+v, want %+v", got, want)
	}
	if s, _ := a.stage(1); len(s.approvers) != 0 {
		t.Errorf("approvers = %+v, want none", s.approvers)
	}
	if a.change != ChangeAdd || a.validity() != nil || details(a) != nil {
		t.Errorf("change=%q validity=%+v details=%+v, want add without either",
			a.change, a.validity(), details(a))
	}
}

// A removal parks the whole assignment value, with prefixed keys. It is a
// delete, and the value's validity and fields belong to the grant
// being removed, not to the request.
func TestApprovalRemoval(t *testing.T) {
	a := fixtureCases(t, "cases_search_removal.json")[0].approval()
	if a.change != ChangeDelete {
		t.Errorf("change = %q, want delete", a.change)
	}
	if a.validity() != nil || details(a) != nil {
		t.Errorf("a removal shows validity %+v and details %+v", a.validity(), details(a))
	}
	if got := stageInfos(a); len(got) != 1 || got[0].Name != "Removal check" {
		t.Errorf("stages = %+v", got)
	}
}

func TestParkedChangeKinds(t *testing.T) {
	assign := func(mod string) string {
		return `{"modificationType":"` + mod + `","path":"c:assignment","value":{"targetRef":{"oid":"r1"}}}`
	}
	for _, tc := range []struct {
		name, delta, want string
	}{
		{"add", `{"changeType":"modify","itemDelta":[` + assign("add") + `]}`, ChangeAdd},
		{"delete", `{"changeType":"modify","itemDelta":` + assign("delete") + `}`, ChangeDelete},
		{"replace", `{"changeType":"modify","itemDelta":[` + assign("replace") + `]}`, ChangeModify},
		{"other item", `{"changeType":"modify","itemDelta":[{"modificationType":"replace","path":"c:fullName","value":"x"}]}`, ChangeModify},
		{"inside an assignment", `{"changeType":"modify","itemDelta":[{"modificationType":"replace","path":"assignment/activation","value":{}}]}`, ChangeModify},
		{"mixed", `{"changeType":"modify","itemDelta":[` + assign("add") + `,` + assign("delete") + `]}`, ChangeModify},
		{"two adds", `{"changeType":"modify","itemDelta":[` + assign("add") + `,` + assign("add") + `]}`, ChangeAdd},
		{"object added", `{"changeType":"add","objectToAdd":{}}`, ChangeModify},
		{"no deltas", `{"changeType":"modify"}`, ChangeUnknown},
		{"no change type", `{"itemDelta":[` + assign("add") + `]}`, ChangeUnknown},
		{"undecodable item", `{"changeType":"modify","itemDelta":[{"path":7}]}`, ChangeUnknown},
	} {
		cj := caseJSON{ApprovalContext: json.RawMessage(`{"deltasToApprove":{"focusPrimaryDelta":` + tc.delta + `}}`)}
		if got := cj.approval().change; got != tc.want {
			t.Errorf("%s: change = %q, want %q", tc.name, got, tc.want)
		}
	}
	for name, ac := range map[string]string{
		"absent":      ``,
		"not decoded": `{"deltasToApprove":"x"}`,
		"no delta":    `{"deltasToApprove":{}}`,
	} {
		if got := (caseJSON{ApprovalContext: json.RawMessage(ac)}).approval(); got.change != ChangeUnknown || got.stages != nil {
			t.Errorf("%s: approval = %+v, want unknown and no stages", name, got)
		}
	}
}

// With several assignments added, the requested one is the value targeting
// the case's target.
func TestParkedValueMatchesTarget(t *testing.T) {
	cj := caseJSON{
		TargetRef: &refJSON{OID: "r2"},
		ApprovalContext: json.RawMessage(`{"deltasToApprove":{"focusPrimaryDelta":{"changeType":"modify","itemDelta":{
			"modificationType":"add","path":"assignment","value":[
				{"targetRef":{"oid":"r1"},"activation":{"validTo":"2026-01-01T00:00:00Z"}},
				{"c:targetRef":{"oid":"r2"},"c:activation":{"validTo":"2027-01-01T00:00:00Z"}}]}}}}`),
	}
	if got := cj.approval().validity(); got == nil || got.ValidTo != "2027-01-01T00:00:00Z" {
		t.Errorf("validity = %+v, want r2's", got)
	}
}

func TestExtensionValue(t *testing.T) {
	item := QName{Namespace: "urn:x", Local: "why"}
	for _, tc := range []struct {
		name, ext, want string
	}{
		{"default namespace", `{"@ns":"urn:x","why":"because"}`, "because"},
		{"no namespace stated", `{"why":"because"}`, "because"},
		{"other namespace", `{"@ns":"urn:other","why":"because"}`, ""},
		{"prefixed", `{"x:why":"because"}`, "because"},
		{"full name", `{"urn:x#why":"because"}`, "because"},
		{"full name, other namespace", `{"urn:y#why":"because"}`, ""},
		{"typed value", `{"why":{"@type":"xsd:string","@value":"because"}}`, "because"},
		{"list", `{"why":["because","also"]}`, "because"},
		{"number", `{"why":42}`, "42"},
		{"null", `{"why":null}`, ""},
		{"other item", `{"what":"x"}`, ""},
		{"not an object", `"x"`, ""},
	} {
		v := map[string]json.RawMessage{"extension": json.RawMessage(tc.ext)}
		if got := extensionValue(v, item); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := extensionValue(map[string]json.RawMessage{}, item); got != "" {
		t.Errorf("no extension: %q", got)
	}
}

func TestCaseCreatedAt(t *testing.T) {
	for name, cj := range map[string]caseJSON{
		"value metadata list": {ValueMetadata: json.RawMessage(`[{"process":{}},{"storage":{"createTimestamp":"t2"}}]`)},
		"metadata container":  {Metadata: json.RawMessage(`{"createTimestamp":"t2"}`)},
		"both":                {ValueMetadata: json.RawMessage(`{"storage":{"createTimestamp":"t2"}}`), Metadata: json.RawMessage(`{"createTimestamp":"t1"}`)},
	} {
		if got := cj.createdAt(); got != "t2" {
			t.Errorf("%s: createdAt = %q, want t2", name, got)
		}
	}
	if got := (caseJSON{ValueMetadata: json.RawMessage(`"x"`)}).createdAt(); got != "" {
		t.Errorf("undecodable metadata: %q", got)
	}
}

func TestStageStrategy(t *testing.T) {
	for in, want := range map[string]string{
		"allMustApprove": StrategyAllMustAgree,
		"firstDecides":   StrategyFirstDecides,
		"allMustAgree":   "", // the contract's word, not midPoint's
		"":               "",
	} {
		if got := stageStrategy(in); got != want {
			t.Errorf("stageStrategy(%q) = %q, want %q", in, got, want)
		}
	}
}

// Stages come back in step order whatever order the schema lists them in, and
// a stage without a number takes its position.
func TestParseStagesOrder(t *testing.T) {
	stages := parseStages(flexSlice{
		json.RawMessage(`{"number":2,"name":"b"}`),
		json.RawMessage(`{"number":1,"name":"a","evaluationStrategy":"allMustApprove"}`),
		json.RawMessage(`"not a stage"`),
	})
	want := []StageInfo{{Number: 1, Count: 2, Name: "a", Strategy: StrategyAllMustAgree}, {Number: 2, Count: 2, Name: "b"}}
	if got := stageInfos(approval{stages: stages}); !reflect.DeepEqual(got, want) {
		t.Errorf("stages = %+v, want %+v", got, want)
	}
	if got := parseStages(flexSlice{json.RawMessage(`{"name":"only"}`)}); got[0].Number != 1 {
		t.Errorf("unnumbered stage = %+v, want number 1", got[0])
	}
	if got := (approval{}).stageInfo(3); got != (StageInfo{Number: 3}) {
		t.Errorf("stageInfo without a schema = %+v", got)
	}
}
