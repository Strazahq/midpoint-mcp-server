//go:build integration

// Live check of the request access preview (D44) against a real midPoint:
// for people whose request rules the test sets up, each prediction of the
// preview must be midPoint's own answer to the same request. midPoint is the
// only enforcer; this test only shows that the preview agrees with it.
// Compiled only under -tags=integration; it skips unless the environment
// below is set.
//
// It WRITES. As the administrator it creates an archetype, two orgs, roles
// and people, all named it-preview-… with oids e5e5e5e5-0000-4000-8000-0000000…,
// and gives the service account, for the run only, a role that lets it read
// request rules (docs/authorization.md). Each person then sends real
// requests, an assignment added to a user, through the service account with
// Switch-To-Principal, as the server sends them. The requested roles carry an
// approval policy, so an accepted request opens a case and changes nothing
// else; the test deletes those cases at once and never decides them. Cleanup
// removes everything, also when the test fails, and checks that nothing named
// it-preview- is left.
//
// The scenarios. Each requester holds End user's authorizations without End
// user's own #assign, plus:
//
//	S1 custom       #assign self: requestable roles as member, except the
//	                description; roles of a test archetype as approver
//	S2 manager      #assign people in the orgs they manage: requestable roles
//	                as member, except the dates (and #read on those people)
//	S3 cost centre  #assign people with their cost centre: an expression
//	                filter, stored structured (and #read on those people)
//	S4 deny         #assign self: requestable roles as member; deny one role
//	S5 two rules    #assign self: requestable roles as member, except the
//	                dates; roles of the test archetype as approver (a role
//	                that is both is granted by two rules with different items)
//
// Every requester asks for one requestable role for every test person (that
// also checks the "who for" list), then for the cases of their own scenario.
// A sure prediction must be midPoint's answer; an unsure one may offer more
// than midPoint accepts, never less. Where they disagree today, the test
// asserts the current behaviour and marks it FINDING (see lpFindings).
//
// Environment:
//
//	MIDPOINT_URL, MIDPOINT_USERNAME, MIDPOINT_PASSWORD      # the #proxy service account
//	MIDPOINT_IT_ADMIN_USERNAME, MIDPOINT_IT_ADMIN_PASSWORD  # an administrator, for setup and cleanup only
//	MIDPOINT_IT_SERVICE_OID          # the service account's user oid
//	MIDPOINT_IT_PROXY_ARCHETYPE_OID  # an archetype inside the service account's #proxy
//	                                 # scope; every test person gets it
//
// Example:
//
//	MIDPOINT_URL=http://localhost:8080/midpoint MIDPOINT_USERNAME=svc-mcp MIDPOINT_PASSWORD=… \
//	MIDPOINT_IT_ADMIN_USERNAME=administrator MIDPOINT_IT_ADMIN_PASSWORD=… \
//	MIDPOINT_IT_SERVICE_OID=… MIDPOINT_IT_PROXY_ARCHETYPE_OID=… \
//	go test -tags=integration ./internal/midpoint -run LivePreview -v -count=1
package midpoint

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"text/tabwriter"
	"time"
)

const (
	lpEndUserOID  = "00000000-0000-0000-0000-000000000008"
	lpAssignURI   = "http://midpoint.evolveum.com/xml/ns/public/security/authorization-model-3#assign"
	lpReadURI     = "http://midpoint.evolveum.com/xml/ns/public/security/authorization-model-3#read"
	lpPrefix      = "it-preview-"
	lpCostCentre1 = "it-preview-cc-1"
	lpCostCentre2 = "it-preview-cc-2"
)

// lpRef is a test object: its oid and name.
type lpRef struct{ OID, Name string }

func lpOID(n int) string { return fmt.Sprintf("e5e5e5e5-0000-4000-8000-0000000%05x", n) }

// short is the name without the test prefix, for the evidence table.
func (r lpRef) short() string { return strings.TrimPrefix(r.Name, lpPrefix) }

var (
	lpGrant     = lpRef{lpOID(0xa0001), "it-preview-grant"}
	lpArchetype = lpRef{lpOID(0xa0002), "it-preview-app-archetype"}
	lpOrgTop    = lpRef{lpOID(0xa0101), "it-preview-org"}
	lpOrgSub    = lpRef{lpOID(0xa0102), "it-preview-suborg"}

	lpRulesCustom     = lpRef{lpOID(0xa0201), "it-preview-rules-custom"}
	lpRulesManager    = lpRef{lpOID(0xa0202), "it-preview-rules-manager"}
	lpRulesCostCentre = lpRef{lpOID(0xa0203), "it-preview-rules-cost-centre"}
	lpRulesDeny       = lpRef{lpOID(0xa0204), "it-preview-rules-deny"}
	lpRulesTwo        = lpRef{lpOID(0xa0205), "it-preview-rules-two-relations"}

	lpRoleA      = lpRef{lpOID(0xa0301), "it-preview-req-a"}           // requestable
	lpRoleB      = lpRef{lpOID(0xa0302), "it-preview-req-b"}           // requestable, denied in S4
	lpRoleHeld   = lpRef{lpOID(0xa0303), "it-preview-req-held"}        // requestable, held by custom and member
	lpRoleApp    = lpRef{lpOID(0xa0304), "it-preview-app-role"}        // test archetype, not requestable
	lpRoleAppReq = lpRef{lpOID(0xa0305), "it-preview-app-requestable"} // test archetype and requestable
	lpRoleNone   = lpRef{lpOID(0xa0306), "it-preview-unrequestable"}   // nobody may request it

	lpCustom     = lpRef{lpOID(0xa0401), "it-preview-custom"}
	lpManager    = lpRef{lpOID(0xa0402), "it-preview-manager"}
	lpMember     = lpRef{lpOID(0xa0403), "it-preview-member"}   // in the org; cost centre 1; holds req-held
	lpDeep       = lpRef{lpOID(0xa0404), "it-preview-deep"}     // in the sub-org
	lpOutsider   = lpRef{lpOID(0xa0405), "it-preview-outsider"} // in no org; cost centre 2
	lpCostCentre = lpRef{lpOID(0xa0406), "it-preview-cost-centre"}
	lpDenied     = lpRef{lpOID(0xa0407), "it-preview-deny"}
	lpTwo        = lpRef{lpOID(0xa0408), "it-preview-two-relations"}

	lpPeople = []lpRef{lpCustom, lpManager, lpMember, lpDeep, lpOutsider, lpCostCentre, lpDenied, lpTwo}
)

// lpObject is one object the test creates, in creation order.
type lpObject struct {
	coll string
	ref  lpRef
}

var lpObjects = []lpObject{
	{"archetypes", lpArchetype}, {"roles", lpGrant}, {"orgs", lpOrgTop}, {"orgs", lpOrgSub},
	{"roles", lpRoleA}, {"roles", lpRoleB}, {"roles", lpRoleHeld}, {"roles", lpRoleApp}, {"roles", lpRoleAppReq}, {"roles", lpRoleNone},
	{"roles", lpRulesCustom}, {"roles", lpRulesManager}, {"roles", lpRulesCostCentre}, {"roles", lpRulesDeny}, {"roles", lpRulesTwo},
	{"users", lpCustom}, {"users", lpManager}, {"users", lpMember}, {"users", lpDeep}, {"users", lpOutsider}, {"users", lpCostCentre},
	{"users", lpDenied}, {"users", lpTwo},
}

// lpCase is one request: requestee, role, relation (local name) and the
// field filled in, if any ("description" or "activation").
type lpCase struct {
	requestee, role lpRef
	relation, field string
}

type lpScenario struct {
	id, title string
	requester lpRef
	cases     []lpCase // beyond the baseline: role A as member for every test person
}

var lpScenarios = []lpScenario{
	{id: "S1", title: "custom rules: self, requestable as member except description; archetype as approver", requester: lpCustom, cases: []lpCase{
		{lpCustom, lpRoleA, "default", "description"},
		{lpCustom, lpRoleA, "default", "activation"},
		{lpCustom, lpRoleA, "approver", ""},
		{lpCustom, lpRoleApp, "approver", ""},
		{lpCustom, lpRoleApp, "approver", "description"},
		{lpCustom, lpRoleApp, "default", ""},
		{lpCustom, lpRoleAppReq, "default", ""},
		{lpCustom, lpRoleAppReq, "default", "description"},
		{lpCustom, lpRoleAppReq, "approver", ""},
		{lpCustom, lpRoleAppReq, "approver", "description"},
		{lpCustom, lpRoleNone, "default", ""},
		{lpCustom, lpRoleHeld, "default", ""},
		{lpCustom, lpRoleHeld, "approver", ""},
	}},
	{id: "S2", title: "manager: people in the managed org and its sub-org, requestable as member except dates", requester: lpManager, cases: []lpCase{
		{lpMember, lpRoleA, "default", "activation"},
		{lpMember, lpRoleA, "approver", ""},
		{lpMember, lpRoleAppReq, "default", ""},
		{lpMember, lpRoleApp, "default", ""},
		{lpMember, lpRoleNone, "default", ""},
		{lpMember, lpRoleHeld, "default", ""},
		{lpDeep, lpRoleA, "default", "activation"},
		{lpManager, lpRoleA, "default", "activation"},
	}},
	{id: "S3", title: "cost centre: people with the requester's cost centre (expression filter)", requester: lpCostCentre, cases: []lpCase{
		{lpCostCentre, lpRoleA, "default", "activation"},
		{lpMember, lpRoleA, "default", "activation"},
	}},
	{id: "S4", title: "deny: self, requestable as member, one role denied by name", requester: lpDenied, cases: []lpCase{
		{lpDenied, lpRoleB, "default", ""},
		{lpDenied, lpRoleB, "approver", ""},
		{lpDenied, lpRoleB, "default", "activation"},
		{lpDenied, lpRoleA, "default", "activation"},
		{lpDenied, lpRoleAppReq, "default", ""},
	}},
	{id: "S5", title: "two rules: self, requestable as member except dates; archetype as approver", requester: lpTwo, cases: []lpCase{
		{lpTwo, lpRoleA, "default", "activation"},
		{lpTwo, lpRoleAppReq, "default", ""},
		{lpTwo, lpRoleAppReq, "default", "activation"},
		{lpTwo, lpRoleAppReq, "approver", ""},
		{lpTwo, lpRoleAppReq, "approver", "activation"},
	}},
}

// lpFinding is a case where the preview and midPoint disagree today: what the
// preview predicts and whether midPoint accepts. The test asserts exactly
// that, so the entry fails once either side changes.
type lpFinding struct {
	offered, accepted bool
	note              string
}

// lpFindings is empty: the two findings of the first run are fixed. A role
// the requester can't search for is no longer "sure not offered"
// (request_role lets midPoint decide), and each relation gets the items of
// the rules midPoint applies to it, not of every rule that grants the role.
var lpFindings = map[string]lpFinding{}

// lpUnsure is when the preview should say it is unsure, per scenario and
// requestee, and the reason it should give.
func lpUnsure(scenario string, who lpRef) (bool, string) {
	switch scenario {
	case "S2":
		// End user may #get any user but search only itself: for someone
		// outside the managed orgs, the preview's org search finds nothing and
		// can't tell "not in the org" from "can't see them".
		if who != lpManager && who != lpMember && who != lpDeep {
			return true, lpRulesManager.Name + " › assign-to-my-org: who it is for"
		}
	case "S3":
		return true, lpRulesCostCentre.Name + " › assign-same-cost-centre: who it is for"
	}
	return false, ""
}

func TestLivePreviewMatchesMidpoint(t *testing.T) {
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Skipf("skipping live preview test: %v", err)
	}
	env := map[string]string{}
	for _, k := range []string{"MIDPOINT_IT_ADMIN_USERNAME", "MIDPOINT_IT_ADMIN_PASSWORD", "MIDPOINT_IT_SERVICE_OID", "MIDPOINT_IT_PROXY_ARCHETYPE_OID"} {
		v := os.Getenv(k)
		if k != "MIDPOINT_IT_ADMIN_PASSWORD" {
			v = strings.TrimSpace(v)
		}
		if v == "" {
			t.Skipf("skipping live preview test: %s is not set", k)
		}
		env[k] = v
	}
	started := time.Now()
	hc := &http.Client{Timeout: 60 * time.Second}
	if cfg.InsecureTLS {
		hc.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // same opt-in as the client
	}
	w := &lpWorld{
		t:     t,
		admin: &lpAdmin{base: cfg.BaseURL, user: env["MIDPOINT_IT_ADMIN_USERNAME"], pass: env["MIDPOINT_IT_ADMIN_PASSWORD"], hc: hc},
		svc:   NewClient(cfg), serviceOID: env["MIDPOINT_IT_SERVICE_OID"], proxyArchetype: env["MIDPOINT_IT_PROXY_ARCHETYPE_OID"],
		baseline: map[string]map[int64]bool{},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// What an interrupted run left behind, by this test's own oids only.
	w.clean(ctx)
	if left := w.leftovers(ctx); len(left) > 0 {
		t.Fatalf("midPoint holds objects named %s that are not this test's: %v", lpPrefix, left)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer ccancel()
		w.clean(cctx)
		if left := w.leftovers(cctx); len(left) > 0 {
			t.Errorf("left on midPoint after cleanup: %v", left)
		} else {
			t.Logf("cleanup: nothing named %s is left, and the service account no longer holds the grant", lpPrefix)
		}
		t.Logf("run took %s", time.Since(started).Round(100*time.Millisecond))
	})
	w.setup(ctx)
	t.Logf("setup done in %s", time.Since(started).Round(100*time.Millisecond))

	var rows []lpRow
	var peopleRows []lpPeopleRow
	for _, sc := range lpScenarios {
		r, pr := w.run(ctx, sc)
		rows = append(rows, r...)
		peopleRows = append(peopleRows, pr...)
	}
	w.report(rows, peopleRows)
}

// --- the world: setup, requests, cleanup ---

type lpWorld struct {
	t                          *testing.T
	admin                      *lpAdmin
	svc                        *Client
	serviceOID, proxyArchetype string
	baseline                   map[string]map[int64]bool // assignment ids per person after setup
}

var (
	lpRootTag   = regexp.MustCompile(`<role\b[^>]*>`)
	lpXMLNS     = regexp.MustCompile(`xmlns(?::[\w.-]+)?="[^"]*"`)
	lpAuthzElem = regexp.MustCompile(`(?s)<authorization\b.*?</authorization>`)
	lpIDAttr    = regexp.MustCompile(`\s+id="\d+"`)
)

// endUser reads End user as XML: the namespace declarations of its root and
// its authorizations, without its own #assign (each scenario brings its own).
func (w *lpWorld) endUser(ctx context.Context) (ns, authorizations string) {
	st, body, err := w.admin.call(ctx, http.MethodGet, "/roles/"+lpEndUserOID, "", "application/xml", nil)
	if err != nil || st != http.StatusOK {
		w.t.Fatalf("reading End user as XML: %d %v", st, err)
	}
	x := string(body)
	ns = strings.Join(lpXMLNS.FindAllString(lpRootTag.FindString(x), -1), " ")
	for _, want := range []string{`xmlns="` + commonNS + `"`, `xmlns:q=`, `xmlns:org=`} {
		if !strings.Contains(ns, want) {
			w.t.Fatalf("End user's XML root lacks %s: %s", want, ns)
		}
	}
	var kept, dropped []string
	for _, a := range lpAuthzElem.FindAllString(x, -1) {
		if strings.Contains(a, "#assign</action>") {
			dropped = append(dropped, a)
			continue
		}
		kept = append(kept, lpIDAttr.ReplaceAllString(a, ""))
	}
	if len(kept) == 0 || len(dropped) == 0 {
		w.t.Fatalf("End user: %d authorizations kept, %d #assign dropped; expected some of each", len(kept), len(dropped))
	}
	w.t.Logf("End user: copying %d authorizations, leaving out its %d #assign", len(kept), len(dropped))
	return ns, strings.Join(kept, "\n")
}

func (w *lpWorld) setup(ctx context.Context) {
	t := w.t
	adminOID := w.admin.selfOID(ctx, t)
	ns, endUser := w.endUser(ctx)
	xml := func(kind string, r lpRef, body string) string {
		return fmt.Sprintf(`<%s %s oid="%s"><name>%s</name>%s</%s>`, kind, ns, r.OID, r.Name, body, kind)
	}
	assign := func(oid, typ, relation string) string {
		rel := ""
		if relation != "" {
			rel = ` relation="` + relation + `"`
		}
		return fmt.Sprintf(`<assignment><targetRef oid="%s" type="%s"%s/></assignment>`, oid, typ, rel)
	}
	// Any assignment of these roles, at any relation, waits for the
	// administrator's approval: an accepted request opens a case and changes
	// nothing else.
	approval := `<assignment><policyRule><name>it-preview-approval</name><policyConstraints><assignment>` +
		`<operation>add</operation><relation>q:any</relation></assignment></policyConstraints>` +
		`<policyActions><approval><approverRef oid="` + adminOID + `" type="UserType"/></approval></policyActions></policyRule></assignment>`
	requestable := `<requestable>true</requestable>`
	app := assign(lpArchetype.OID, "ArchetypeType", "")
	sameCostCentre := `<filter><q:equal><q:path>costCenter</q:path><expression><path>$subject/costCenter</path></expression></q:equal></filter>`
	myOrg := `<orgRelation><subjectRelation>org:manager</subjectRelation></orgRelation>`
	reqTarget := `<target><type>RoleType</type><filter><q:text>requestable = true</q:text></filter></target>`

	rules := map[lpRef]string{
		lpRulesCustom: `
<authorization><name>assign-requestable-as-member</name><action>` + lpAssignURI + `</action><phase>request</phase>
 <object><special>self</special></object>` + reqTarget + `
 <relation>org:default</relation><exceptItem>assignment/description</exceptItem></authorization>
<authorization><name>assign-app-roles-as-approver</name><action>` + lpAssignURI + `</action><phase>request</phase>
 <object><special>self</special></object>
 <target><type>RoleType</type><archetypeRef oid="` + lpArchetype.OID + `"/></target>
 <relation>org:approver</relation></authorization>`,
		lpRulesManager: `
<authorization><name>assign-to-my-org</name><action>` + lpAssignURI + `</action><phase>request</phase>
 <object><type>UserType</type>` + myOrg + `</object>` + reqTarget + `
 <relation>org:default</relation><exceptItem>assignment/activation</exceptItem></authorization>
<authorization><name>read-my-org</name><action>` + lpReadURI + `</action>
 <object><type>UserType</type>` + myOrg + `</object></authorization>`,
		lpRulesCostCentre: `
<authorization><name>assign-same-cost-centre</name><action>` + lpAssignURI + `</action><phase>request</phase>
 <object><type>UserType</type>` + sameCostCentre + `</object>` + reqTarget + `
 <relation>org:default</relation></authorization>
<authorization><name>read-same-cost-centre</name><action>` + lpReadURI + `</action>
 <object><type>UserType</type>` + sameCostCentre + `</object></authorization>`,
		lpRulesDeny: `
<authorization><name>assign-requestable-to-self</name><action>` + lpAssignURI + `</action><phase>request</phase>
 <object><special>self</special></object>` + reqTarget + `
 <relation>org:default</relation></authorization>
<authorization><name>deny-req-b</name><decision>deny</decision><action>` + lpAssignURI + `</action>
 <object><special>self</special></object>
 <target><type>RoleType</type><filter><q:text>name = "` + lpRoleB.Name + `"</q:text></filter></target></authorization>`,
		lpRulesTwo: `
<authorization><name>assign-requestable-without-dates</name><action>` + lpAssignURI + `</action><phase>request</phase>
 <object><special>self</special></object>` + reqTarget + `
 <relation>org:default</relation><exceptItem>assignment/activation</exceptItem></authorization>
<authorization><name>assign-app-roles-as-approver</name><action>` + lpAssignURI + `</action><phase>request</phase>
 <object><special>self</special></object>
 <target><type>RoleType</type><archetypeRef oid="` + lpArchetype.OID + `"/></target>
 <relation>org:approver</relation></authorization>`,
	}
	person := func(r lpRef, costCentre string, extra ...string) string {
		body := ""
		if costCentre != "" {
			body = "<costCenter>" + costCentre + "</costCenter>"
		}
		body += assign(w.proxyArchetype, "ArchetypeType", "") + strings.Join(extra, "")
		return xml("user", r, body)
	}
	bodies := map[lpRef]string{
		lpArchetype: xml("archetype", lpArchetype, ""),
		lpGrant: xml("role", lpGrant, `<authorization><name>read-request-rules</name><action>`+lpReadURI+`</action>`+
			`<object><type>AbstractRoleType</type></object><item>name</item><item>displayName</item><item>authorization</item>`+
			`<item>lifecycleState</item><item>activation</item><item>delegable</item></authorization>`),
		lpOrgTop:     xml("org", lpOrgTop, ""),
		lpOrgSub:     xml("org", lpOrgSub, assign(lpOrgTop.OID, "OrgType", "")),
		lpRoleA:      xml("role", lpRoleA, requestable+approval),
		lpRoleB:      xml("role", lpRoleB, requestable+approval),
		lpRoleHeld:   xml("role", lpRoleHeld, requestable+approval),
		lpRoleApp:    xml("role", lpRoleApp, app+approval),
		lpRoleAppReq: xml("role", lpRoleAppReq, requestable+app+approval),
		lpRoleNone:   xml("role", lpRoleNone, approval),
		lpCustom:     person(lpCustom, "", assign(lpRulesCustom.OID, "RoleType", "")),
		lpManager:    person(lpManager, "", assign(lpRulesManager.OID, "RoleType", ""), assign(lpOrgTop.OID, "OrgType", "org:manager")),
		lpMember:     person(lpMember, lpCostCentre1, assign(lpOrgTop.OID, "OrgType", "")),
		lpDeep:       person(lpDeep, "", assign(lpOrgSub.OID, "OrgType", "")),
		lpOutsider:   person(lpOutsider, lpCostCentre2),
		lpCostCentre: person(lpCostCentre, lpCostCentre1, assign(lpRulesCostCentre.OID, "RoleType", "")),
		lpDenied:     person(lpDenied, "", assign(lpRulesDeny.OID, "RoleType", "")),
		lpTwo:        person(lpTwo, "", assign(lpRulesTwo.OID, "RoleType", "")),
	}
	for r, authz := range rules {
		bodies[r] = xml("role", r, endUser+authz)
	}
	for _, o := range lpObjects {
		st, body, err := w.admin.call(ctx, http.MethodPost, "/"+o.coll, "application/xml", "application/json", []byte(bodies[o.ref]))
		if err != nil || st/100 != 2 {
			t.Fatalf("creating %s: %d %v %s", o.ref.Name, st, err, lpSnippet(body))
		}
		if st, _, err := w.admin.json(ctx, http.MethodGet, "/"+o.coll+"/"+o.ref.OID, nil); err != nil || st != http.StatusOK {
			t.Fatalf("%s was not created (an approval?): %d %v", o.ref.Name, st, err)
		}
	}

	// The service account may read request rules for the run.
	if st, body, err := w.admin.json(ctx, http.MethodPatch, "/users/"+w.serviceOID, modifyBody(itemDelta{
		ModificationType: "add", Path: "assignment", Value: map[string]any{"targetRef": map[string]any{"oid": lpGrant.OID, "type": "RoleType"}},
	})); err != nil || st/100 != 2 {
		t.Fatalf("giving the service account the grant: %d %v %s", st, err, lpSnippet(body))
	}

	// The held role, without its approval: added raw, then recomputed so
	// midPoint lists it among the person's memberships.
	for _, who := range []lpRef{lpCustom, lpMember} {
		st, body, err := w.admin.json(ctx, http.MethodPatch, "/users/"+who.OID+"?options=raw", modifyBody(itemDelta{
			ModificationType: "add", Path: "assignment", Value: map[string]any{"targetRef": map[string]any{"oid": lpRoleHeld.OID, "type": "RoleType"}},
		}))
		if err != nil || st/100 != 2 {
			t.Fatalf("giving %s the held role: %d %v %s", who.Name, st, err, lpSnippet(body))
		}
		if st, body, err := w.admin.json(ctx, http.MethodPatch, "/users/"+who.OID+"?options=reconcile", map[string]any{"objectModification": map[string]any{}}); err != nil || st/100 != 2 {
			t.Fatalf("recomputing %s: %d %v %s", who.Name, st, err, lpSnippet(body))
		}
		if n := w.dropCases(ctx, who.OID); n > 0 {
			t.Fatalf("giving %s the held role opened %d cases", who.Name, n)
		}
	}
	for _, p := range lpPeople {
		w.baseline[p.OID] = w.admin.assignmentIDs(ctx, t, p.OID)
	}
}

// request sends one request as the requester, the way the server sends it,
// then undoes what it did: deletes the cases it opened and removes an
// assignment it added.
func (w *lpWorld) request(ctx context.Context, requester lpRef, c lpCase) lpAnswer {
	ref := map[string]any{"oid": c.role.OID, "type": "RoleType"}
	if c.relation != "default" {
		ref["relation"] = "org:" + c.relation
	}
	value := map[string]any{"targetRef": ref}
	switch c.field {
	case "description":
		value["description"] = "it-preview"
	case "activation":
		now := time.Now().UTC().Truncate(time.Second)
		value["activation"] = map[string]string{"validFrom": now.Add(24 * time.Hour).Format(time.RFC3339), "validTo": now.Add(30 * 24 * time.Hour).Format(time.RFC3339)}
	}
	body, err := json.Marshal(modifyBody(itemDelta{ModificationType: "add", Path: "assignment", Value: value}))
	if err != nil {
		w.t.Fatal(err)
	}
	resp, err := w.svc.doFull(WithPrincipal(ctx, requester.OID), http.MethodPatch, userPath(c.requestee.OID), nil, body)
	a := lpAnswer{status: resp.StatusCode}
	if err != nil {
		var se *StatusError
		if !errors.As(err, &se) {
			w.t.Fatalf("request %s → %s %s: %v", requester.Name, c.requestee.Name, c.role.Name, err)
		}
		a.status, a.reason = se.StatusCode, se.Reason
		if a.reason == "" {
			a.reason = se.Message
		}
	}
	cases := w.dropCases(ctx, c.requestee.OID)
	added := w.dropNewAssignments(ctx, c.requestee.OID)
	switch {
	case a.status/100 != 2:
	case cases > 0:
		a.effect = "case"
	case added > 0:
		a.effect = "assigned"
	default:
		a.effect = "no change"
	}
	return a
}

// dropCases deletes the cases about a person and returns how many there were.
func (w *lpWorld) dropCases(ctx context.Context, personOID string) int {
	raws, err := w.admin.search(ctx, "cases", `objectRef matches (oid = `+quoteQueryString(personOID)+`)`)
	if err != nil {
		w.t.Errorf("searching the cases about %s: %v", personOID, err)
		return 0
	}
	for _, raw := range raws {
		w.admin.delete(ctx, w.t, "cases", lpOIDOf(raw))
	}
	return len(raws)
}

// dropNewAssignments removes the assignments a person got after setup.
func (w *lpWorld) dropNewAssignments(ctx context.Context, personOID string) int {
	n := 0
	for id := range w.admin.assignmentIDs(ctx, w.t, personOID) {
		if !w.baseline[personOID][id] {
			w.admin.removeAssignment(ctx, w.t, personOID, id)
			n++
		}
	}
	return n
}

// clean removes what this test creates, by its own oids, and the cases and
// tasks about its people. It is safe to run when nothing is there.
func (w *lpWorld) clean(ctx context.Context) {
	for _, p := range lpPeople {
		w.dropCases(ctx, p.OID)
	}
	for _, coll := range []string{"cases", "tasks"} {
		raws, err := w.admin.search(ctx, coll, `name contains[origIgnoreCase] "`+lpPrefix+`"`)
		if err != nil {
			w.t.Errorf("searching %s: %v", coll, err)
		}
		for _, raw := range raws {
			w.admin.delete(ctx, w.t, coll, lpOIDOf(raw))
		}
	}
	for id, target := range w.admin.assignments(ctx, w.t, w.serviceOID) {
		if target == lpGrant.OID {
			w.admin.removeAssignment(ctx, w.t, w.serviceOID, id)
		}
	}
	for i := len(lpObjects) - 1; i >= 0; i-- {
		w.admin.delete(ctx, w.t, lpObjects[i].coll, lpObjects[i].ref.OID)
	}
}

// leftovers names what is still on midPoint: objects named it-preview- and
// the grant on the service account.
func (w *lpWorld) leftovers(ctx context.Context) []string {
	var out []string
	for _, coll := range []string{"users", "roles", "orgs", "archetypes", "cases", "tasks"} {
		raws, err := w.admin.search(ctx, coll, `name contains[origIgnoreCase] "`+lpPrefix+`"`)
		if err != nil {
			out = append(out, fmt.Sprintf("%s: %v", coll, err))
		}
		for _, raw := range raws {
			var o struct {
				Name polyString `json:"name"`
			}
			_ = json.Unmarshal(raw, &o)
			out = append(out, coll+"/"+o.Name.value())
		}
	}
	for _, target := range w.admin.assignments(ctx, w.t, w.serviceOID) {
		if target == lpGrant.OID {
			out = append(out, "the grant on the service account")
		}
	}
	return out
}

// --- one scenario ---

// lpOffer is the preview's catalog for one requestee, or why there is none.
type lpOffer struct {
	who   person
	offer RoleOffer
	err   error
}

type lpPrediction struct {
	offered, sure, held bool
	why                 string
	because             []string
}

type lpAnswer struct {
	status int
	effect string // for a 2xx: "case", "assigned" or "no change"
	reason string // what midPoint said when it refused
}

// accepted is midPoint taking the request on: it opened a case or assigned.
func (a lpAnswer) accepted() bool { return a.status/100 == 2 && a.effect != "no change" }

func (a lpAnswer) String() string {
	if a.effect != "" {
		return fmt.Sprintf("%d %s", a.status, a.effect)
	}
	return fmt.Sprint(a.status)
}

type lpRow struct {
	key     string
	scen    string
	c       lpCase
	from    lpRef
	pred    lpPrediction
	ans     lpAnswer
	verdict string
}

type lpPeopleRow struct {
	scen, person     string
	listed, accepted bool
	sure             bool
	verdict          string
}

func (w *lpWorld) run(ctx context.Context, sc lpScenario) ([]lpRow, []lpPeopleRow) {
	t := w.t
	rctx := WithPrincipal(ctx, sc.requester.OID)
	p, err := w.svc.RequestPreview(rctx)
	if err != nil {
		t.Fatalf("%s: RequestPreview as %s: %v", sc.id, sc.requester.Name, err)
	}
	t.Logf("%s %s — %s: %d request rules, unsure %q", sc.id, sc.requester.short(), sc.title, len(p.Rules), p.Unsure)
	for _, r := range p.Rules {
		t.Logf("  rule %s", lpDescribeRule(r))
	}
	w.checkRules(sc, p)

	offers := map[string]lpOffer{}
	for _, who := range lpPeople {
		var o lpOffer
		if o.who, o.err = w.svc.readPerson(rctx, who.OID); o.err == nil {
			o.offer, o.err = p.RolesFor(rctx, o.who, lpPrefix, 200)
			if o.err != nil {
				t.Fatalf("%s: RolesFor %s: %v", sc.id, who.Name, o.err)
			}
		}
		offers[who.OID] = o
		w.checkOffer(sc, who, o)
	}
	people, err := p.People(rctx, lpPrefix, 200)
	if err != nil {
		t.Fatalf("%s: People: %v", sc.id, err)
	}
	w.checkPeople(sc, people)

	var rows []lpRow
	accepted := map[string]bool{}
	cases := []lpCase{}
	for _, who := range lpPeople {
		cases = append(cases, lpCase{who, lpRoleA, "default", ""})
	}
	for i, c := range append(cases, sc.cases...) {
		// For a role the catalog doesn't offer at that relation, request_role
		// has the last word: a role the requester can't search for passes its
		// check unsure, and midPoint decides.
		var check error
		if role, ok := lpOffered(offers[c.requestee.OID], c); !ok || !contains(role.Relations(), c.relation) {
			check = w.svc.CheckRequestOffer(rctx, c.requestee.OID, c.role.OID, c.relation)
		}
		pred := lpPredict(p, offers[c.requestee.OID], c, check)
		ans := w.request(ctx, sc.requester, c)
		if i < len(lpPeople) {
			accepted[c.requestee.OID] = ans.accepted()
		}
		row := lpRow{scen: sc.id, c: c, from: sc.requester, pred: pred, ans: ans}
		row.key = fmt.Sprintf("%s %s→%s %s %s %s", sc.id, sc.requester.short(), c.requestee.short(), c.role.short(), c.relation, lpOr(c.field, "-"))
		row.verdict = lpVerdict(pred, ans)
		if f, known := lpFindings[row.key]; known {
			if pred.offered == f.offered && ans.accepted() == f.accepted {
				row.verdict = "FINDING"
			} else {
				t.Errorf("%s: the finding changed (preview offered=%v, midPoint accepted=%v; recorded %v/%v): %s", row.key, pred.offered, ans.accepted(), f.offered, f.accepted, f.note)
			}
		} else if row.verdict == "MISMATCH" {
			t.Errorf("%s: preview %s (%s), midPoint %s: %s", row.key, lpOfferedWord(pred), pred.why, ans, ans.reason)
		}
		rows = append(rows, row)
	}

	// The "who for" list: exactly the people midPoint accepts a request for,
	// or more of them when the preview is unsure.
	listed := map[string]bool{}
	for _, x := range people.People {
		listed[x.OID] = true
	}
	var prows []lpPeopleRow
	sure := len(people.Unsure) == 0
	for _, who := range lpPeople {
		r := lpPeopleRow{scen: sc.id + " " + sc.requester.short(), person: who.short(), listed: listed[who.OID], accepted: accepted[who.OID], sure: sure}
		key := fmt.Sprintf("P %s %s→%s", sc.id, sc.requester.short(), who.short())
		switch {
		case r.listed == r.accepted:
			r.verdict = "ok"
		case !sure && r.listed:
			r.verdict = "ok (unsure: shows more)"
		default:
			r.verdict = "MISMATCH"
		}
		if f, known := lpFindings[key]; known {
			if r.listed == f.offered && r.accepted == f.accepted {
				r.verdict = "FINDING"
			} else {
				t.Errorf("%s: the finding changed (listed=%v, accepted=%v): %s", key, r.listed, r.accepted, f.note)
			}
		} else if r.verdict == "MISMATCH" {
			t.Errorf("%s: People lists=%v, midPoint accepts=%v (unsure %q)", key, r.listed, r.accepted, people.Unsure)
		}
		prows = append(prows, r)
	}
	return rows, prows
}

// lpOffered is the catalog's offer of the case's role, if any.
func lpOffered(o lpOffer, c lpCase) (OfferedRole, bool) {
	for _, r := range o.offer.Roles {
		if r.OID == c.role.OID {
			return r, true
		}
	}
	return OfferedRole{}, false
}

// lpPredict reads the preview's answer for one request. check is
// request_role's own check, made when the catalog doesn't offer the role at
// that relation.
func lpPredict(p *Preview, o lpOffer, c lpCase, check error) lpPrediction {
	if o.err != nil {
		code, _ := ErrorCode(o.err)
		return lpPrediction{sure: true, why: "requestee not readable (" + code + ")"}
	}
	pr := lpPrediction{sure: len(o.offer.Unsure) == 0}
	for _, m := range o.who.Memberships {
		if m.OID == c.role.OID && lpOr(localName(m.Relation), "default") == c.relation {
			pr.held = true
		}
	}
	role, listed := lpOffered(o, c)
	offer, ok := role.Offer(c.relation)
	switch {
	case pr.held && !ok:
		pr.why = "held already"
		return pr
	case !ok && check == nil:
		pr.offered, pr.sure, pr.why = true, false, "not listed (the requester can't search for it); request_role lets midPoint decide"
		return pr
	case !listed:
		pr.why = "role not offered"
		return pr
	case !ok:
		pr.why = "not offered as " + c.relation
		return pr
	}
	pr.because = offer.Because
	switch c.field {
	case "activation":
		if !offer.Validity {
			pr.why = "dates not allowed"
			return pr
		}
	case "description":
		if !lpFieldAllowed(p, offer, "assignment/description") {
			pr.why = "description not allowed"
			return pr
		}
	}
	pr.offered, pr.why = true, "offered"
	return pr
}

// lpFieldAllowed applies the preview's own field rule (offerFor: an item may
// be filled when a rule midPoint applies to the relation covers it) to the
// description, which the offer has no flag for: the server never fills it.
// No deny rule here names the description, so denies are left out.
func lpFieldAllowed(p *Preview, o RelationOffer, path string) bool {
	for _, r := range p.Rules {
		if !r.Deny && contains(o.Because, r.Label()) && r.Paths.includes(path) {
			return true
		}
	}
	return false
}

// lpVerdict compares a prediction with midPoint's answer. A sure prediction
// must be the answer. An unsure one may offer more, never less. A role held
// already is not offered, and midPoint must not change anything for it: on
// 4.10.3 it answers 204 with no change when someone asks for their own held
// role, and 403 when a manager asks for a member's (the request changes
// nothing, so no approval starts, and midPoint then refuses an operation on
// the member that is not the assignment check).
func lpVerdict(pr lpPrediction, a lpAnswer) string {
	switch {
	case pr.held:
		if a.accepted() {
			return "MISMATCH"
		}
		return "ok (held: nothing to request)"
	case pr.offered && a.accepted(), !pr.offered && a.status == http.StatusForbidden:
		return "ok"
	case pr.offered && !pr.sure && a.status == http.StatusForbidden:
		return "ok (unsure: shows more)"
	}
	return "MISMATCH"
}

// checkRules asserts how the preview read each scenario's rules.
func (w *lpWorld) checkRules(sc lpScenario, p *Preview) {
	t := w.t
	want := map[string][]string{
		"S1": {"assign-requestable-as-member", "assign-app-roles-as-approver"},
		"S2": {"assign-to-my-org"},
		"S3": {"assign-same-cost-centre"},
		"S4": {"assign-requestable-to-self", "deny-req-b"},
		"S5": {"assign-requestable-without-dates", "assign-app-roles-as-approver"},
	}[sc.id]
	var got []string
	for _, r := range p.Rules {
		got = append(got, r.Name)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s: rules %v, want %v", sc.id, got, want)
	}
	if len(p.Unsure) != 0 {
		t.Errorf("%s: preview unsure %q", sc.id, p.Unsure)
	}
	if sc.id != "S3" {
		return
	}
	// midPoint returns the expression filter as it stores it, structured, not
	// as query-language text; the preview can't evaluate that and says so.
	for _, r := range p.Rules {
		if r.Name == "assign-same-cost-centre" {
			s := r.Object[0]
			t.Logf("S3: midPoint returns the cost-centre filter structured: %s", lpSnippet(s.FilterRaw))
			if len(s.FilterRaw) == 0 || s.FilterText != "" {
				t.Errorf("S3: filter read as text %q, raw %s; want raw (structured)", s.FilterText, s.FilterRaw)
			}
		}
	}
}

// checkOffer asserts the unsure flags of one requestee's catalog.
func (w *lpWorld) checkOffer(sc lpScenario, who lpRef, o lpOffer) {
	if o.err != nil {
		w.t.Logf("%s: %s can't read %s: %v", sc.id, sc.requester.short(), who.short(), o.err)
		return
	}
	var roles []string
	for _, r := range o.offer.Roles {
		var rels []string
		for _, o := range r.Offers {
			rels = append(rels, o.Relation+lpIf(o.Validity, "+dates", ""))
		}
		roles = append(roles, strings.TrimPrefix(r.Name, lpPrefix)+"("+strings.Join(rels, "/")+")")
	}
	w.t.Logf("%s: for %s the preview offers %v; unsure %q", sc.id, who.short(), roles, o.offer.Unsure)
	wantUnsure, why := lpUnsure(sc.id, who)
	if gotUnsure := len(o.offer.Unsure) > 0; gotUnsure != wantUnsure || (wantUnsure && !contains(o.offer.Unsure, why)) {
		w.t.Errorf("%s: for %s unsure %q, want unsure=%v %q", sc.id, who.short(), o.offer.Unsure, wantUnsure, why)
	}
}

func (w *lpWorld) checkPeople(sc lpScenario, people PeopleOffer) {
	var names []string
	for _, x := range people.People {
		names = append(names, strings.TrimPrefix(x.Name, lpPrefix))
	}
	w.t.Logf("%s: People lists %v; unsure %q", sc.id, names, people.Unsure)
	// Only S3's rule is one the people search can't write; S2's org search is
	// exact (it lists whom the manager may search, which is whom they manage).
	wantUnsure, why := sc.id == "S3", lpRulesCostCentre.Name+" › assign-same-cost-centre: who it is for"
	if gotUnsure := len(people.Unsure) > 0; gotUnsure != wantUnsure || (wantUnsure && !contains(people.Unsure, why)) {
		w.t.Errorf("%s: People unsure %q, want unsure=%v %q", sc.id, people.Unsure, wantUnsure, why)
	}
}

// report logs the evidence: every request, the preview's prediction and why,
// midPoint's answer, and the rules the preview named.
func (w *lpWorld) report(rows []lpRow, prows []lpPeopleRow) {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tscen\trequester → requestee\trole\trelation\tfield\tpreview\tmidPoint\tverdict\tbecause")
	for i, r := range rows {
		because := strings.ReplaceAll(strings.Join(r.pred.because, "; "), lpPrefix, "")
		fmt.Fprintf(tw, "%d\t%s\t%s → %s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", i+1, r.scen, r.from.short(), r.c.requestee.short(), r.c.role.short(),
			r.c.relation, lpOr(r.c.field, "-"), lpPreviewText(r.pred), r.ans, r.verdict, because)
	}
	_ = tw.Flush()
	w.t.Logf("requests (names without %q; preview \"unsure\" = may offer more than midPoint accepts):\n%s", lpPrefix, b.String())

	b.Reset()
	tw = tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "scenario\tperson\tPeople lists\tmidPoint accepts\tsure\tverdict")
	for _, r := range prows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%v\t%s\n", r.scen, r.person, lpIf(r.listed, "yes", "no"), lpIf(r.accepted, "yes", "no"), r.sure, r.verdict)
	}
	_ = tw.Flush()
	w.t.Logf("who for (People vs. a request for %s as member):\n%s", lpRoleA.short(), b.String())

	for _, r := range rows {
		if r.verdict != "ok" {
			w.t.Logf("%s: %s; preview %s; midPoint %s %s", r.key, r.verdict, lpPreviewText(r.pred), r.ans, r.ans.reason)
		}
	}
}

func lpOfferedWord(p lpPrediction) string {
	word := "not offered"
	if p.offered {
		word = "offered"
	}
	if !p.sure {
		word += ", unsure"
	}
	return word
}

// lpPreviewText is the prediction for the table: offered, or not and why.
func lpPreviewText(p lpPrediction) string {
	if p.offered {
		return lpOfferedWord(p)
	}
	return lpOfferedWord(p) + ": " + p.why
}

func lpDescribeRule(r AssignRule) string {
	var b strings.Builder
	b.WriteString(r.Label())
	if r.Deny {
		b.WriteString(" DENY")
	}
	fmt.Fprintf(&b, " kind=%d relations=%v items=%v except=%v", r.Kind, r.Relations, r.Paths.items, r.Paths.except)
	for _, s := range r.Object {
		b.WriteString(" who{" + lpDescribeSelector(s) + "}")
	}
	for _, s := range r.Target {
		b.WriteString(" which{" + lpDescribeSelector(s) + "}")
	}
	return strings.ReplaceAll(b.String(), lpPrefix, "")
}

func lpDescribeSelector(s Selector) string {
	var parts []string
	add := func(ok bool, v string) {
		if ok {
			parts = append(parts, v)
		}
	}
	add(s.Type != "", s.Type)
	add(s.Self, "self")
	add(s.FilterText != "", "filter "+s.FilterText)
	add(len(s.FilterRaw) > 0, "structured filter")
	add(len(s.Archetypes) > 0, fmt.Sprintf("archetype %v", s.Archetypes))
	add(s.Org != "", "org "+s.Org)
	if s.OrgRelation != nil {
		parts = append(parts, fmt.Sprintf("orgRelation %v %s", s.OrgRelation.SubjectRelations, s.OrgRelation.Scope))
	}
	add(len(s.Unsupported) > 0, fmt.Sprintf("unsupported %v", s.Unsupported))
	return strings.Join(parts, " ")
}

func lpOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func lpIf(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}

func lpSnippet(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}

func lpOIDOf(raw json.RawMessage) string {
	var o struct {
		OID string `json:"oid"`
	}
	_ = json.Unmarshal(raw, &o)
	return o.OID
}

// --- the administrator's REST calls ---

// lpAdmin calls midPoint's REST API as the administrator, for setup and
// cleanup only; the preview and the requests go through the server's client.
type lpAdmin struct {
	base, user, pass string
	hc               *http.Client
}

func (a *lpAdmin) call(ctx context.Context, method, path, ctype, accept string, body []byte) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.base, "/")+restPrefix+path, rd)
	if err != nil {
		return 0, nil, withoutURL(err)
	}
	req.SetBasicAuth(a.user, a.pass)
	req.Header.Set("Accept", accept)
	if body != nil {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		return 0, nil, withoutURL(err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return resp.StatusCode, out, err
}

func (a *lpAdmin) json(ctx context.Context, method, path string, v any) (int, []byte, error) {
	var body []byte
	if v != nil {
		var err error
		if body, err = json.Marshal(v); err != nil {
			return 0, nil, err
		}
	}
	return a.call(ctx, method, path, "application/json", "application/json", body)
}

func (a *lpAdmin) search(ctx context.Context, coll, filter string) ([]json.RawMessage, error) {
	st, body, err := a.json(ctx, http.MethodPost, "/"+coll+"/search", map[string]any{
		"query": map[string]any{"filter": map[string]any{"text": filter}, "paging": map[string]any{"maxSize": 500}},
	})
	if err != nil {
		return nil, err
	}
	if st != http.StatusOK {
		return nil, fmt.Errorf("search %s [%s]: status %d %s", coll, filter, st, lpSnippet(body))
	}
	return parseObjectList(body)
}

func (a *lpAdmin) delete(ctx context.Context, t *testing.T, coll, oid string) {
	if oid == "" {
		return
	}
	st, body, err := a.json(ctx, http.MethodDelete, "/"+coll+"/"+oid, nil)
	if err != nil || (st/100 != 2 && st != http.StatusNotFound) {
		t.Errorf("deleting %s/%s: %d %v %s", coll, oid, st, err, lpSnippet(body))
	}
}

func (a *lpAdmin) selfOID(ctx context.Context, t *testing.T) string {
	st, body, err := a.json(ctx, http.MethodGet, "/self", nil)
	var s selfResponse
	if err != nil || st != http.StatusOK || json.Unmarshal(body, &s) != nil || s.User.OID == "" {
		t.Fatalf("reading the administrator: %d %v", st, err)
	}
	return s.User.OID
}

// assignments maps a user's assignment ids to their targets; none when the
// user doesn't exist.
func (a *lpAdmin) assignments(ctx context.Context, t *testing.T, oid string) map[int64]string {
	st, body, err := a.json(ctx, http.MethodGet, "/users/"+oid, nil)
	if st == http.StatusNotFound {
		return nil
	}
	var env struct {
		User struct {
			Assignment flexSlice `json:"assignment"`
		} `json:"user"`
	}
	if err != nil || st != http.StatusOK || json.Unmarshal(body, &env) != nil {
		t.Errorf("reading the assignments of %s: %d %v", oid, st, err)
		return nil
	}
	out := map[int64]string{}
	for _, raw := range env.User.Assignment {
		var as struct {
			ID        int64   `json:"@id"`
			TargetRef refJSON `json:"targetRef"`
		}
		if json.Unmarshal(raw, &as) == nil {
			out[as.ID] = as.TargetRef.OID
		}
	}
	return out
}

func (a *lpAdmin) assignmentIDs(ctx context.Context, t *testing.T, oid string) map[int64]bool {
	out := map[int64]bool{}
	for id := range a.assignments(ctx, t, oid) {
		out[id] = true
	}
	return out
}

func (a *lpAdmin) removeAssignment(ctx context.Context, t *testing.T, userOID string, id int64) {
	st, body, err := a.json(ctx, http.MethodPatch, "/users/"+userOID, modifyBody(itemDelta{
		ModificationType: "delete", Path: "assignment", Value: map[string]any{"@id": id},
	}))
	if err != nil || st/100 != 2 {
		t.Errorf("removing assignment %d of %s: %d %v %s", id, userOID, st, err, lpSnippet(body))
	}
}
