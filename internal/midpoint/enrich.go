package midpoint

import (
	"context"
	"encoding/json"
)

// Enrichment of approval data (docs/ui-contract.md 7.1, 7.3): the people and
// roles a case names, read as the caller, so a view can name them and an
// approver can decide. Rules (7.1): computed for every caller; a failed read
// leaves its field out (or marks the object readable: false) and never fails
// the answer; every object is read at most once per call; list_work_items
// reads for at most its first inboxEnrichLimit items.
//
// Every read is a plain GET by OID as the caller: midPoint's stock End user
// role gets every user and role (its assignment-target-get authorization),
// which is what its GUI shows an approver too. Live on 4.10.3: a person
// holding only End user and Approver reads the requestee's activation,
// parentOrgRef and roleMembershipRef with its value metadata.

// inboxEnrichLimit is how many work items of one list_work_items answer may
// read from midPoint for their context [default]. Later items use only what
// earlier ones already read.
const inboxEnrichLimit = 50

// abstractCollections are the REST collections of the object types read with
// roleJSON's shape (name, displayName, description, riskLevel), by short type.
var abstractCollections = map[string]string{
	"Role":      collRoles,
	"Org":       "orgs",
	"Service":   "services",
	"Archetype": "archetypes",
}

// governanceRelations put a user in charge of a role (approving requests for
// it, owning it) without giving them the role's access.
var governanceRelations = map[string]bool{"approver": true, "owner": true}

// readState is the outcome of a read the reader was asked for.
type readState int

const (
	notRead    readState = iota // the reader was closed and had no result
	readOK                      // the object was read as the caller
	readFailed                  // midPoint refused the read, or it failed
)

type readResult[T any] struct {
	v  T
	ok bool
}

// refReader reads, as the caller, the users and roles an answer names, each
// at most once; a failed read is remembered like a successful one. Once
// closed it starts no new read and answers from what it has.
type refReader struct {
	c      *Client
	users  map[string]readResult[userJSON]
	roles  map[string]readResult[roleJSON] // by collection and OID
	closed bool
}

func newRefReader(c *Client) *refReader {
	return &refReader{
		c:     c,
		users: map[string]readResult[userJSON]{},
		roles: map[string]readResult[roleJSON]{},
	}
}

// user reads a user, with reference names resolved.
func (r *refReader) user(ctx context.Context, oid string) (userJSON, readState) {
	if res, done := r.users[oid]; done {
		return res.v, stateOf(res.ok)
	}
	if r.closed || oid == "" {
		return userJSON{}, notRead
	}
	var u userJSON
	err := r.c.getObject(ctx, collUsers, oid, true, &u)
	r.users[oid] = readResult[userJSON]{v: u, ok: err == nil}
	return u, stateOf(err == nil)
}

// role reads a role, org, service or archetype from its collection.
func (r *refReader) role(ctx context.Context, collection, oid string) (roleJSON, readState) {
	key := collection + "/" + oid
	if res, done := r.roles[key]; done {
		return res.v, stateOf(res.ok)
	}
	if r.closed || oid == "" {
		return roleJSON{}, notRead
	}
	var ro roleJSON
	err := r.c.getObject(ctx, collection, oid, false, &ro)
	r.roles[key] = readResult[roleJSON]{v: ro, ok: err == nil}
	return ro, stateOf(err == nil)
}

// cachedRole returns a role this reader already read, without reading.
func (r *refReader) cachedRole(typ, oid string) (roleJSON, bool) {
	res, done := r.roles[abstractCollections[typ]+"/"+oid]
	return res.v, done && res.ok
}

func stateOf(ok bool) readState {
	if ok {
		return readOK
	}
	return readFailed
}

// unreadable marks a reference the caller could not read (D16).
func unreadable(o *ObjectRef) {
	f := false
	o.Readable = &f
}

// objectRef names a reference for display, reading the object as the caller:
// a user's fullName, a role's displayName. A reference without a type is
// taken for a user when person is set.
func (r *refReader) objectRef(ctx context.Context, ref refJSON, person bool) ObjectRef {
	out := ObjectRef{OID: ref.OID, Type: cleanType(ref.Type), Name: ref.TargetName.value()}
	if out.Type == "" && person {
		out.Type = "User"
	}
	switch {
	case out.Type == "User":
		u, st := r.user(ctx, ref.OID)
		applyUser(&out, u, st)
	case abstractCollections[out.Type] != "":
		ro, st := r.role(ctx, abstractCollections[out.Type], ref.OID)
		applyRole(&out, ro, st)
	}
	return out
}

func applyUser(o *ObjectRef, u userJSON, st readState) {
	switch st {
	case readOK:
		if n := u.Name.value(); n != "" {
			o.Name = n
		}
		o.DisplayName = u.FullName.value()
	case readFailed:
		unreadable(o)
	}
}

func applyRole(o *ObjectRef, ro roleJSON, st readState) {
	switch st {
	case readOK:
		if n := ro.Name.value(); n != "" {
			o.Name = n
		}
		o.DisplayName = ro.DisplayName.value()
	case readFailed:
		unreadable(o)
	}
}

// objectRefs names references, skipping the OIDs in skip and repeats.
func (r *refReader) objectRefs(ctx context.Context, refs []refJSON, skip map[string]bool) []ObjectRef {
	out := []ObjectRef{}
	seen := map[string]bool{}
	for _, ref := range refs {
		if ref.OID == "" || skip[ref.OID] || seen[ref.OID] {
			continue
		}
		seen[ref.OID] = true
		out = append(out, r.objectRef(ctx, ref, true))
	}
	return out
}

// person names an optional reference to a person.
func (r *refReader) person(ctx context.Context, ref *refJSON) *ObjectRef {
	if ref == nil || ref.OID == "" {
		return nil
	}
	o := r.objectRef(ctx, *ref, true)
	return &o
}

// requestee names whose access a case changes and returns their user object
// when it could be read.
func (r *refReader) requestee(ctx context.Context, ref *refJSON) (PersonRef, userJSON, readState) {
	if ref == nil || ref.OID == "" {
		return PersonRef{}, userJSON{}, notRead
	}
	out := PersonRef{ObjectRef: ObjectRef{OID: ref.OID, Type: cleanType(ref.Type), Name: ref.TargetName.value()}}
	if out.Type == "" {
		out.Type = "User"
	}
	if out.Type != "User" {
		out.ObjectRef = r.objectRef(ctx, *ref, false)
		return out, userJSON{}, notRead
	}
	u, st := r.user(ctx, ref.OID)
	applyUser(&out.ObjectRef, u, st)
	if st == readOK {
		out.Status = u.Activation.status()
	}
	return out, u, st
}

// target names what a case requests, with the role's description and risk
// level.
func (r *refReader) target(ctx context.Context, ref *refJSON) TargetRef {
	if ref == nil || ref.OID == "" {
		return TargetRef{}
	}
	out := TargetRef{ObjectRef: ObjectRef{OID: ref.OID, Type: cleanType(ref.Type), Name: ref.TargetName.value()}}
	coll := abstractCollections[out.Type]
	if coll == "" {
		out.ObjectRef = r.objectRef(ctx, *ref, false)
		return out
	}
	ro, st := r.role(ctx, coll, ref.OID)
	applyRole(&out.ObjectRef, ro, st)
	if st == readOK {
		out.Description = ro.Description
		out.RiskLevel = ro.RiskLevel
	}
	return out
}

// requesteeAccess lists the requestee's roles in effect now. midPoint keeps
// only active memberships in roleMembershipRef: live on 4.10.3, a disabled
// assignment and one whose validFrom is still ahead add none. A user whose
// read carries neither memberships nor assignments counts as not visible:
// midPoint drops the items a reader may not see, so an empty answer cannot be
// told from a hidden one.
func (r *refReader) requesteeAccess(ctx context.Context, u userJSON, st readState) RequesteeAccess {
	acc := RequesteeAccess{Roles: []RoleMembership{}}
	if st != readOK || (len(u.RoleMembershipRef) == 0 && len(u.Assignment) == 0) {
		return acc
	}
	acc.Visible = true
	acc.Roles = inEffectRoles(u)
	for i := range acc.Roles {
		m := &acc.Roles[i]
		if coll := abstractCollections[m.Type]; coll != "" {
			ro, st := r.role(ctx, coll, m.OID)
			if st == readOK {
				m.DisplayName = ro.DisplayName.value()
			}
		}
	}
	return acc
}

// inEffectRoles lists a user's memberships as RoleMembership, leaving out
// archetypes (a classification, not access) and governance relations. A
// membership is direct when one of its assignment paths has a single segment;
// otherwise via names the first segment's target, the directly assigned role
// or org it comes with (contract 4.5, D32). Without path metadata a
// membership is direct when the user holds an assignment to it.
func inEffectRoles(u userJSON) []RoleMembership {
	assigned := map[string]bool{}
	for _, raw := range u.Assignment {
		var a assignmentJSON
		if json.Unmarshal(raw, &a) == nil && a.TargetRef != nil {
			assigned[a.TargetRef.OID] = true
		}
	}
	names := map[string]string{}
	var refs []refJSON
	var paths [][][]refJSON
	for _, raw := range u.RoleMembershipRef {
		var ref refJSON
		if json.Unmarshal(raw, &ref) != nil || ref.OID == "" {
			continue
		}
		if n := ref.TargetName.value(); n != "" {
			names[ref.OID] = n
		}
		refs = append(refs, ref)
		paths = append(paths, assignmentPaths(raw))
	}

	out := []RoleMembership{}
	seen := map[string]bool{}
	for i, ref := range refs {
		typ := cleanType(ref.Type)
		if typ == "Archetype" || governanceRelations[relationLocal(ref.Relation)] || seen[ref.OID] {
			continue
		}
		seen[ref.OID] = true
		m := RoleMembership{OID: ref.OID, Name: ref.TargetName.value(), Type: typ}
		if len(paths[i]) == 0 {
			m.Direct = assigned[ref.OID]
		}
		for _, p := range paths[i] {
			if len(p) == 1 {
				m.Direct = true
			}
		}
		if !m.Direct {
			for _, p := range paths[i] {
				if len(p) < 2 {
					continue
				}
				via := ObjectRef{OID: p[0].OID, Type: cleanType(p[0].Type), Name: p[0].TargetName.value()}
				if via.Name == "" {
					via.Name = names[via.OID]
				}
				m.Via = &via
				break
			}
		}
		out = append(out, m)
	}
	return out
}

// assignmentPaths reads the assignment paths in a roleMembershipRef value's
// metadata: @metadata (one value per path) / provenance / assignmentPath /
// segment[] / targetRef. Each path is its segments' targets, the directly
// assigned one first.
func assignmentPaths(raw json.RawMessage) [][]refJSON {
	var v struct {
		Metadata flexSlice `json:"@metadata"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	var out [][]refJSON
	for _, md := range v.Metadata {
		var m struct {
			Provenance *struct {
				AssignmentPath flexSlice `json:"assignmentPath"`
			} `json:"provenance"`
		}
		if json.Unmarshal(md, &m) != nil || m.Provenance == nil {
			continue
		}
		for _, ap := range m.Provenance.AssignmentPath {
			var p struct {
				Segment flexSlice `json:"segment"`
			}
			if json.Unmarshal(ap, &p) != nil {
				continue
			}
			var path []refJSON
			for _, seg := range p.Segment {
				var s struct {
					TargetRef *refJSON `json:"targetRef"`
				}
				if json.Unmarshal(seg, &s) == nil && s.TargetRef != nil && s.TargetRef.OID != "" {
					path = append(path, *s.TargetRef)
				}
			}
			if len(path) > 0 {
				out = append(out, path)
			}
		}
	}
	return out
}

// enricher builds the context of the work items in one answer.
type enricher struct {
	reader  *refReader
	self    userJSON
	team    TeamConfig
	item    QName
	hasItem bool
}

func (c *Client) newEnricher(self userJSON) *enricher {
	item, ok := c.cfg.File.Requests.Justification()
	return &enricher{reader: newRefReader(c), self: self, team: c.teamConfig(), item: item, hasItem: ok}
}

// workItemContext is what an approver needs to decide wi (contract 7.1).
func (e *enricher) workItemContext(ctx context.Context, cj caseJSON, a approval, items []workItemJSON, wi workItemJSON) WorkItemContext {
	wc := newWorkItemContext()
	wc.Change = a.change
	if p := e.reader.person(ctx, cj.RequestorRef); p != nil {
		wc.Requester = *p
	}
	requestee, u, st := e.reader.requestee(ctx, cj.ObjectRef)
	wc.Requestee = requestee
	wc.Target = e.reader.target(ctx, cj.TargetRef)
	wc.Justification = a.justification(e.item, e.hasItem)
	wc.Validity = a.validity()
	wc.RequestedAt = cj.createdAt()
	wc.CreatedAt = wi.CreateTimestamp
	wc.Deadline = wi.Deadline
	wc.Stage = a.stageInfo(wi.StageNumber)
	wc.Reason = reason(e.self, e.team, u, st == readOK, refOID(cj.TargetRef))

	co := otherAssignees(wi, e.self.OID)
	skip := map[string]bool{e.self.OID: true}
	wc.CoAssignees = e.reader.objectRefs(ctx, co, skip)
	for _, r := range co {
		skip[r.OID] = true
	}
	wc.StageApprovers = e.reader.objectRefs(ctx, stageApprovers(items, a, wi, e.self.OID), skip)
	wc.RequesteeAccess = e.reader.requesteeAccess(ctx, u, st)
	return wc
}

// reason says why a work item is in the caller's inbox, in the order of D29:
// the caller manages (a selected manager link) an org the requestee is a
// member of; else the caller's own roleMembershipRef holds the target as
// approver, then as owner; else midPoint simply assigned it.
func reason(self userJSON, team TeamConfig, requestee userJSON, requesteeRead bool, targetOID string) string {
	if requesteeRead {
		managed := map[string]bool{}
		for _, l := range orgLinks(callerOrgs(self, team), team) {
			if l.Selected && l.Manager {
				managed[l.OID] = true
			}
		}
		member := team.memberRelation()
		for _, o := range callerOrgs(requestee, team) {
			rel := relationLocal(o.Relation)
			if rel == "" {
				rel = relationDefault
			}
			if managed[o.OID] && rel == member {
				return ReasonManager
			}
		}
	}
	if targetOID == "" {
		return ReasonAssigned
	}
	held := map[string]bool{}
	for _, raw := range self.RoleMembershipRef {
		var ref refJSON
		if json.Unmarshal(raw, &ref) == nil && ref.OID == targetOID {
			held[relationLocal(ref.Relation)] = true
		}
	}
	switch {
	case held["approver"]:
		return ReasonRoleApprover
	case held["owner"]:
		return ReasonRoleOwner
	}
	return ReasonAssigned
}

// otherAssignees are wi's assignees other than selfOID: whoever else may
// complete the same item.
func otherAssignees(wi workItemJSON, selfOID string) []refJSON {
	var out []refJSON
	for _, r := range wi.assignees() {
		if r.OID != selfOID {
			out = append(out, r)
		}
	}
	return out
}

// othersHidden reports whether every work item of the case the caller can see
// is assigned to the caller. midPoint's stock Approver role reads only the
// reader's own work items (live on 4.10.3: another approver's item is not
// in the case object at all), so the case then says nothing about other
// approvers' items, open or closed.
func othersHidden(items []workItemJSON, selfOID string) bool {
	if selfOID == "" {
		return false
	}
	for _, wi := range items {
		if wi.assignedTo(selfOID) == nil {
			return false
		}
	}
	return true
}

// stageApprovers are the assignees of the case's other open work items in
// wi's stage. When the caller sees only its own items, they come from the
// stage's approverRef in the approval schema instead: the approvers midPoint
// resolved when the case started. That is a best guess (an approver who
// already decided an all-must-agree stage is still listed), and stages whose
// approvers an expression computes, such as a requestee's managers, have none.
func stageApprovers(items []workItemJSON, a approval, wi workItemJSON, selfOID string) []refJSON {
	var out []refJSON
	for _, o := range items {
		if o.ID.s == wi.ID.s || !o.open() || o.StageNumber != wi.StageNumber {
			continue
		}
		out = append(out, o.assignees()...)
	}
	if othersHidden(items, selfOID) {
		if s, ok := a.stage(wi.StageNumber); ok {
			out = append(out, s.approvers...)
		}
	}
	return out
}

// nextApprovers are who still has to decide an open case: the assignees of
// its open work items and, when the caller sees only its own items, the
// approvers the schema names for the current stage (as in stageApprovers),
// apart from the caller.
func nextApprovers(cj caseJSON, a approval, items []workItemJSON, selfOID string) []refJSON {
	if cj.State != "open" && cj.State != "created" {
		return nil
	}
	var out []refJSON
	for _, wi := range items {
		if wi.open() {
			out = append(out, wi.assignees()...)
		}
	}
	if othersHidden(items, selfOID) {
		if s, ok := a.stage(cj.StageNumber); ok {
			for _, r := range s.approvers {
				if r.OID != selfOID {
					out = append(out, r)
				}
			}
		}
	}
	return out
}

// caseDetail is get_case's answer for a case read as the caller (contract
// 7.3): the case's references named, the request, the steps and every work
// item with its people.
func (c *Client) caseDetail(ctx context.Context, cj caseJSON) CaseDetail {
	return c.caseDetailWithReader(ctx, cj, newRefReader(c))
}

func (c *Client) caseDetailWithReader(ctx context.Context, cj caseJSON, r *refReader) CaseDetail {
	a := cj.approval()
	items := cj.items()
	item, hasItem := c.cfg.File.Requests.Justification()

	d := CaseDetail{
		CaseSummary:   cj.summary(),
		ObjectRef:     r.person(ctx, cj.ObjectRef),
		RequestorRef:  r.person(ctx, cj.RequestorRef),
		Change:        a.change,
		RequestedAt:   cj.createdAt(),
		ClosedAt:      cj.CloseTimestamp,
		Justification: a.justification(item, hasItem),
		Validity:      a.validity(),
		Stages:        []StageInfo{},
		WorkItems:     []CaseWorkItem{},
		NextApprovers: []ObjectRef{},
	}
	if cj.TargetRef != nil && cj.TargetRef.OID != "" {
		t := r.objectRef(ctx, *cj.TargetRef, false)
		d.TargetRef = &t
	}
	for _, s := range a.stages {
		d.Stages = append(d.Stages, s.StageInfo)
	}
	if (cj.State == "open" || cj.State == "created") && cj.StageNumber > 0 {
		s := a.stageInfo(cj.StageNumber)
		d.Stage = &s
	}
	for _, wi := range items {
		item := CaseWorkItem{
			WorkItem: WorkItem{
				CaseOID:  cj.OID,
				ID:       wi.ID.s,
				Assignee: wi.assigneeNames(),
				Stage:    wi.StageNumber,
			},
			Assignees: r.objectRefs(ctx, wi.assignees(), nil),
			CreatedAt: wi.CreateTimestamp,
			ClosedAt:  wi.CloseTimestamp,
			Deadline:  wi.Deadline,
			Performer: r.person(ctx, wi.PerformerRef),
		}
		if wi.Output != nil {
			item.Outcome = shortURI(wi.Output.Outcome)
			item.Comment = wi.Output.Comment
		}
		d.WorkItems = append(d.WorkItems, item)
	}
	// The caller's OID only decides whether others' items are hidden; without
	// it, nothing is guessed from the schema.
	var selfOID string
	if self, err := c.selfUser(ctx); err == nil {
		selfOID = self.OID
	}
	d.NextApprovers = r.objectRefs(ctx, nextApprovers(cj, a, items, selfOID), nil)
	return d
}
