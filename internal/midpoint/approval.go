package midpoint

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

// What an approval case says about its request, read from the REST case
// object (docs/ui-contract.md 7.1, 7.3). list_work_items and get_case share
// it. Shapes as midPoint 4.10.3 writes them, fired live:
//
//   - The change waiting for approval is approvalContext/deltasToApprove/
//     focusPrimaryDelta: changeType, then itemDelta[] with modificationType,
//     path ("c:assignment") and value[]. An added assignment value carries the
//     requested activation/validFrom, activation/validTo and extension items.
//     The case object of a search carries approvalContext too.
//   - The steps are approvalContext/approvalSchema/stage[] (a single stage is
//     a bare object), each with number, name, evaluationStrategy
//     (allMustApprove or firstDecides) and, where midPoint resolved them when
//     the case started, approverRef[].
//   - The case's creation time is its value metadata,
//     @metadata/storage/createTimestamp.
//
// Everything here is best-effort: a part that does not decode is treated as
// absent, so a case with an unexpected shape never fails a list.

// Approval stage strategies as the contract names them. midPoint calls the
// first one allMustApprove.
const (
	StrategyAllMustAgree = "allMustAgree"
	StrategyFirstDecides = "firstDecides"
)

// stageStrategy maps midPoint's evaluationStrategy to the contract's word, or
// "" when it is unknown.
func stageStrategy(s string) string {
	switch s {
	case "allMustApprove":
		return StrategyAllMustAgree
	case "firstDecides":
		return StrategyFirstDecides
	}
	return ""
}

type approvalContextJSON struct {
	DeltasToApprove *struct {
		FocusPrimaryDelta *objectDeltaJSON `json:"focusPrimaryDelta"`
	} `json:"deltasToApprove"`
	ApprovalSchema *struct {
		Stage flexSlice `json:"stage"`
	} `json:"approvalSchema"`
}

type objectDeltaJSON struct {
	ChangeType string    `json:"changeType"`
	ItemDelta  flexSlice `json:"itemDelta"`
}

type itemDeltaJSON struct {
	ModificationType string    `json:"modificationType"`
	Path             string    `json:"path"`
	Value            flexSlice `json:"value"`
}

type stageDefJSON struct {
	Number             int        `json:"number"`
	Name               polyString `json:"name"`
	EvaluationStrategy string     `json:"evaluationStrategy"`
	ApproverRef        flexSlice  `json:"approverRef"`
}

// stageDef is one approval step of a case.
type stageDef struct {
	StageInfo
	// approvers are the stage's approverRef values: the approvers midPoint
	// resolved when the case started (named approvers, and approvers by
	// relation to the target). Approvers an expression computes, such as a
	// requestee's managers, are not among them.
	approvers []refJSON
}

// approval is what a case's approvalContext says about the request.
type approval struct {
	change string
	// value is the requested assignment value when change is add; nil
	// otherwise. Its keys are kept as midPoint wrote them, prefixed or not.
	value  map[string]json.RawMessage
	stages []stageDef // in step order
}

// approval parses the case's approvalContext.
func (cj caseJSON) approval() approval {
	a := approval{change: ChangeUnknown}
	if len(cj.ApprovalContext) == 0 {
		return a
	}
	var ac approvalContextJSON
	if json.Unmarshal(cj.ApprovalContext, &ac) != nil {
		return a
	}
	if ac.DeltasToApprove != nil && ac.DeltasToApprove.FocusPrimaryDelta != nil {
		a.change, a.value = ac.DeltasToApprove.FocusPrimaryDelta.parked(refOID(cj.TargetRef))
	}
	if ac.ApprovalSchema != nil {
		a.stages = parseStages(ac.ApprovalSchema.Stage)
	}
	return a
}

// parked derives the kind of change from the delta and returns the added
// assignment value: the one targeting targetOID, else the first. A request
// for access adds an assignment and a request to remove access deletes one;
// anything else (other items, mixed deltas, an object added or deleted under
// approval) is a modify.
func (d objectDeltaJSON) parked(targetOID string) (string, map[string]json.RawMessage) {
	switch d.ChangeType {
	case "":
		return ChangeUnknown, nil
	case "modify":
	default:
		return ChangeModify, nil
	}
	kinds := map[string]bool{}
	var added []map[string]json.RawMessage
	for _, raw := range d.ItemDelta {
		var id itemDeltaJSON
		if json.Unmarshal(raw, &id) != nil {
			kinds[ChangeUnknown] = true
			continue
		}
		kind := ChangeModify
		if localPath(id.Path) == "assignment" {
			switch id.ModificationType {
			case "add":
				kind = ChangeAdd
			case "delete":
				kind = ChangeDelete
			}
		}
		kinds[kind] = true
		if kind != ChangeAdd {
			continue
		}
		for _, v := range id.Value {
			var m map[string]json.RawMessage
			if json.Unmarshal(v, &m) == nil {
				added = append(added, m)
			}
		}
	}
	change := ChangeUnknown
	switch len(kinds) {
	case 0:
	case 1:
		for k := range kinds {
			change = k
		}
	default:
		change = ChangeModify
	}
	if change != ChangeAdd || len(added) == 0 {
		return change, nil
	}
	for _, v := range added {
		if targetOID != "" && assignmentTargetOID(v) == targetOID {
			return change, v
		}
	}
	return change, added[0]
}

// localPath drops the namespace prefix of every segment of an item path
// ("c:assignment" → "assignment").
func localPath(p string) string {
	segs := strings.Split(strings.TrimSpace(p), "/")
	for i, s := range segs {
		if j := strings.LastIndex(s, ":"); j >= 0 {
			segs[i] = s[j+1:]
		}
	}
	return strings.Join(segs, "/")
}

// assignmentTargetOID returns the OID an assignment value targets.
func assignmentTargetOID(v map[string]json.RawMessage) string {
	raw, ok := refKey(v, "targetRef")
	if !ok {
		return ""
	}
	var r refJSON
	if json.Unmarshal(raw, &r) != nil {
		return ""
	}
	return r.OID
}

// parkedValidity reads activation/validFrom and activation/validTo of an
// assignment value; nil when neither is set.
func parkedValidity(v map[string]json.RawMessage) *Validity {
	raw, ok := refKey(v, "activation")
	if !ok {
		return nil
	}
	var act map[string]json.RawMessage
	if json.Unmarshal(raw, &act) != nil {
		return nil
	}
	var val Validity
	for local, dst := range map[string]*string{"validFrom": &val.ValidFrom, "validTo": &val.ValidTo} {
		if raw, ok := refKey(act, local); ok {
			*dst = scalarText(raw)
		}
	}
	if val.ValidFrom == "" && val.ValidTo == "" {
		return nil
	}
	return &val
}

// extensionValue returns the value of an assignment value's extension item.
// midPoint does not write the item's namespace the same way every time: a
// search result declares it as the extension's default namespace ("@ns"), a
// single-object GET leaves it out. So the item matches on its local name, and
// a namespace is compared only where the JSON states one.
func extensionValue(v map[string]json.RawMessage, item QName) string {
	raw, ok := refKey(v, "extension")
	if !ok {
		return ""
	}
	var ext map[string]json.RawMessage
	if json.Unmarshal(raw, &ext) != nil {
		return ""
	}
	var defaultNS string
	if ns, ok := ext["@ns"]; ok {
		_ = json.Unmarshal(ns, &defaultNS)
	}
	for k, val := range ext {
		if strings.HasPrefix(k, "@") {
			continue
		}
		switch {
		case k == item.Local:
			if defaultNS != "" && defaultNS != item.Namespace {
				continue
			}
		case strings.Contains(k, "#"): // namespace#local
			if k != item.Namespace+"#"+item.Local {
				continue
			}
		case strings.HasSuffix(k, ":"+item.Local): // prefix:local
		default:
			continue
		}
		return scalarText(val)
	}
	return ""
}

// scalarText renders a single JSON value as text: a string as itself, a
// number or boolean as written, a typed value ({"@value": …}) and a list
// (its first value) unwrapped. Anything else is "".
func scalarText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	switch raw[0] {
	case '"':
		var s string
		_ = json.Unmarshal(raw, &s)
		return s
	case '[':
		var vals []json.RawMessage
		if json.Unmarshal(raw, &vals) != nil || len(vals) == 0 {
			return ""
		}
		return scalarText(vals[0])
	case '{':
		var typed map[string]json.RawMessage
		if json.Unmarshal(raw, &typed) != nil {
			return ""
		}
		if v, ok := typed["@value"]; ok {
			return scalarText(v)
		}
		return ""
	case 'n': // null
		return ""
	}
	return string(raw)
}

// parseStages reads approvalSchema/stage values in step order. A stage
// without a number takes its position.
func parseStages(raws flexSlice) []stageDef {
	out := make([]stageDef, 0, len(raws))
	for i, raw := range raws {
		var s stageDefJSON
		if json.Unmarshal(raw, &s) != nil {
			continue
		}
		n := s.Number
		if n <= 0 {
			n = i + 1
		}
		out = append(out, stageDef{
			StageInfo: StageInfo{
				Number:   n,
				Name:     s.Name.value(),
				Strategy: stageStrategy(s.EvaluationStrategy),
			},
			approvers: decodeRefs(s.ApproverRef),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	for i := range out {
		out[i].Count = len(out)
	}
	return out
}

// stage returns step n, or false when the schema does not describe it.
func (a approval) stage(n int) (stageDef, bool) {
	for _, s := range a.stages {
		if s.Number == n {
			return s, true
		}
	}
	return stageDef{}, false
}

// stageInfo describes step n with what the schema says about it.
func (a approval) stageInfo(n int) StageInfo {
	if s, ok := a.stage(n); ok {
		return s.StageInfo
	}
	info := StageInfo{Number: n}
	if len(a.stages) > 0 {
		info.Count = len(a.stages)
	}
	return info
}

// requestDetailsOf lists the fields of a request for access (D43): the
// extension items of the requested assignment value. A removal has none.
func (c *Client) requestDetailsOf(a approval) []RequestDetail {
	if a.change != ChangeAdd || a.value == nil {
		return nil
	}
	return c.requestDetails(a.value)
}

// validity is the requested validity, for a request for access only.
func (a approval) validity() *Validity {
	if a.change != ChangeAdd || a.value == nil {
		return nil
	}
	return parkedValidity(a.value)
}

// createdAt returns when the case was created: its storage metadata, which
// midPoint 4.10 writes as value metadata (@metadata/storage/createTimestamp)
// and earlier versions as a metadata container (metadata/createTimestamp).
func (cj caseJSON) createdAt() string {
	if len(cj.ValueMetadata) > 0 {
		var metas flexSlice
		if json.Unmarshal(cj.ValueMetadata, &metas) == nil {
			for _, raw := range metas {
				var m struct {
					Storage *struct {
						CreateTimestamp string `json:"createTimestamp"`
					} `json:"storage"`
				}
				if json.Unmarshal(raw, &m) == nil && m.Storage != nil && m.Storage.CreateTimestamp != "" {
					return m.Storage.CreateTimestamp
				}
			}
		}
	}
	if len(cj.Metadata) > 0 {
		var m struct {
			CreateTimestamp string `json:"createTimestamp"`
		}
		if json.Unmarshal(cj.Metadata, &m) == nil {
			return m.CreateTimestamp
		}
	}
	return ""
}

// decodeRefs decodes reference values, skipping any without an OID.
func decodeRefs(raws flexSlice) []refJSON {
	out := make([]refJSON, 0, len(raws))
	for _, raw := range raws {
		var r refJSON
		if json.Unmarshal(raw, &r) == nil && r.OID != "" {
			out = append(out, r)
		}
	}
	return out
}

// refOID returns a reference's OID, or "" for none.
func refOID(r *refJSON) string {
	if r == nil {
		return ""
	}
	return r.OID
}
