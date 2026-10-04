package midpoint

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

const requestNS = "{http://example.com/xml/ns/access-request}"

func formTestClient(t *testing.T) *Client {
	t.Helper()
	c, warnings := formTestClientWith(t, Config{})
	if len(warnings) != 0 {
		t.Fatalf("warnings %v", warnings)
	}
	return c
}

// formTestClientWith loads the request form from recorded-style schema
// answers: a database schema, a schema file and a lookup table.
func formTestClientWith(t *testing.T, cfg Config) (*Client, []string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(SwitchToPrincipalHeader) != "" {
			t.Error("schema discovery impersonated a user")
		}
		files := map[string]string{"/ws/rest/schemas": "request_schema_db.xml", "/ws/schema": "request_schema_files.xml", "/ws/schema/request-extension.xsd": "request_schema_file.xsd",
			"/ws/rest/lookupTables/70000000-0000-0000-0000-000000000001": "lookup_table_regions.json"}
		f, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/ws/rest/lookupTables/") {
			if r.URL.Query().Get("include") != "row" {
				t.Error("lookup table read without its rows")
			}
		} else if r.Header.Get("Accept") != "application/xml" {
			t.Error("schema discovery must request XML")
		}
		b, err := os.ReadFile("testdata/" + f)
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	cfg.BaseURL = srv.URL
	c := NewClient(cfg)
	var warnings []string
	if err := c.LoadRequestForm(context.Background(), func(s string) { warnings = append(warnings, s) }); err != nil {
		t.Fatal(err)
	}
	return c, warnings
}

// Every assignment item of midPoint's schema is a field (D43), in display
// order; hidden, operational and read-only items are not, and an item the
// form can't fill is named apart.
func TestRequestFormSchemaSources(t *testing.T) {
	f := formTestClient(t).RequestForm()
	var names []string
	for _, i := range f.Items {
		names = append(names, i.Name)
	}
	want := []string{"justification", "projectCode", "ticket", "acknowledged", "neededOn", "handover", "accessLevel", "region", "environments", "costShare", "costCenter"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("items %v, want %v", names, want)
	}
	by := map[string]FormItem{}
	for _, i := range f.Items {
		by[i.Name] = i
	}
	if i := by["projectCode"]; !i.Required || i.DisplayName != "Project code" || i.Help != "The project this access supports." || i.Type != "string" {
		t.Errorf("projectCode %+v", i)
	}
	if i := by["justification"]; i.Required || i.Help != "Explain why this access is needed." {
		t.Errorf("justification %+v", i)
	}
	if i := by["accessLevel"]; i.Type != "choice" || !reflect.DeepEqual(i.Options, []FormOption{{"read", "Read only"}, {"write", "Read and write"}}) {
		t.Errorf("enumeration %+v", i)
	}
	if i := by["region"]; i.Type != "choice" || !reflect.DeepEqual(i.Options, []FormOption{{"eu", "Europe"}, {"us", "United States"}, {"apac", ""}}) {
		t.Errorf("lookup table %+v", i)
	}
	if i := by["environments"]; i.Type != "string" || !i.Multiple || i.Required {
		t.Errorf("multi-valued %+v", i)
	}
	if by["costShare"].Type != "decimal" || by["ticket"].Type != "int" || by["acknowledged"].Type != "boolean" {
		t.Errorf("types %+v", f.Items)
	}
	if !reflect.DeepEqual(f.Other, []FormOther{{Name: "sponsor", QName: requestNS + "sponsor", DisplayName: "Sponsor"}}) {
		t.Errorf("other %+v", f.Other)
	}
}

// The settings of 0.5 still load, and are named as unused.
func TestRequestFormOldSettings(t *testing.T) {
	cfg := Config{}
	cfg.File.Requests.FormItems = []string{requestNS + "projectCode"}
	c, warnings := formTestClientWith(t, cfg)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "no longer used") || len(c.RequestForm().Items) != 11 {
		t.Errorf("warnings %v, items %d", warnings, len(c.RequestForm().Items))
	}
}

// A schema that can't be read leaves no form, and the server starts.
func TestRequestFormUnreadable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	t.Cleanup(srv.Close)
	c := NewClient(Config{BaseURL: srv.URL})
	var warnings []string
	if err := c.LoadRequestForm(context.Background(), func(s string) { warnings = append(warnings, s) }); err != nil || c.RequestForm() != nil {
		t.Fatalf("err %v form %+v", err, c.RequestForm())
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "could not be read") {
		t.Errorf("warnings %v", warnings)
	}
}

func TestRequestFieldsValidation(t *testing.T) {
	c := formTestClient(t)
	base := func(extra map[string]any) map[string]any {
		m := map[string]any{"projectCode": "P", "acknowledged": false}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	for _, tc := range []struct {
		name   string
		fields map[string]any
		field  string
	}{
		{"required", map[string]any{}, "projectCode"},
		{"blank", map[string]any{"projectCode": "  "}, "projectCode"},
		{"unknown", map[string]any{"secret": "x"}, "secret"},
		{"hidden item", base(map[string]any{"syncMarker": "x"}), "syncMarker"},
		{"boolean missing", map[string]any{"projectCode": "P"}, "acknowledged"},
		{"boolean wrong", base(map[string]any{"acknowledged": "true"}), "acknowledged"},
		{"int fraction", base(map[string]any{"ticket": 1.5}), "ticket"},
		{"int range", base(map[string]any{"ticket": float64(1 << 32)}), "ticket"},
		{"decimal text", base(map[string]any{"costShare": "half"}), "costShare"},
		{"date invalid", base(map[string]any{"neededOn": "2028-02-30"}), "neededOn"},
		{"datetime no offset", base(map[string]any{"handover": "2028-10-01T12:00:00"}), "handover"},
		{"choice not offered", base(map[string]any{"accessLevel": "admin"}), "accessLevel"},
		{"lookup not offered", base(map[string]any{"region": "Europe"}), "region"},
		{"list for one value", base(map[string]any{"projectCode": []any{"P", "Q"}}), "projectCode"},
		{"list value wrong", base(map[string]any{"environments": []any{"dev", 7.0}}), "environments"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := c.ValidateRequestFields(tc.fields)
			code, field := ErrorCode(err)
			if code != CodeInvalidField || field != tc.field {
				t.Fatalf("error %v code %s field %s", err, code, field)
			}
		})
	}
	good := base(map[string]any{"justification": "  ", "ticket": float64(7), "neededOn": "2028-02-29", "handover": "2028-10-01T12:30:00+02:00",
		"costCenter": "Research", "accessLevel": "write", "region": "eu", "environments": []any{"dev", " ", "test"}, "costShare": 0.25})
	clean, ext, err := c.ValidateRequestFields(good)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := clean["justification"]; ok {
		t.Error("empty optional value not omitted")
	}
	if clean["acknowledged"] != false || len(ext) != 10 {
		t.Errorf("values %v %v", clean, ext)
	}
	if got := ext["http://example.com/xml/ns/access-request#environments"]; !reflect.DeepEqual(got, []any{"dev", "test"}) {
		t.Errorf("several values %v", got)
	}
	if got := ext["http://example.com/xml/ns/access-request#region"]; got != "eu" {
		t.Errorf("one value %v", got)
	}
	if _, _, err := NewClient(Config{}).ValidateRequestFields(map[string]any{"projectCode": "P"}); err == nil {
		t.Error("field accepted without form")
	}
}

func TestRequestValidityValidation(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, from, to string
		ok             bool
	}{
		{"unlimited", "", "", true},
		{"today", "2026-10-01T00:00:00+02:00", "2026-10-01T23:59:59+02:00", true},
		{"start only", "2026-10-02T00:00:00Z", "", true},
		{"zone today", "2026-10-02T00:00:00+14:00", "2026-10-02T23:59:59+14:00", true},
		{"past start", "2026-09-30T23:59:59Z", "2026-10-03T00:00:00Z", false},
		{"past end", "", "2026-09-30T23:59:59Z", false},
		{"reverse", "2026-10-03T00:00:00Z", "2026-10-02T00:00:00Z", false},
		{"equal", "2026-10-03T00:00:00Z", "2026-10-03T00:00:00Z", false},
		{"no offset", "2026-10-02T00:00:00", "", false},
		{"invalid date", "", "2026-02-30T00:00:00Z", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRequestValidity(tc.from, tc.to, now)
			if (err == nil) != tc.ok {
				t.Fatalf("error %v", err)
			}
			if err != nil {
				if code, _ := ErrorCode(err); code != CodeInvalidValidity {
					t.Error(code)
				}
			}
		})
	}
}

func TestRequestPlanOneAssignment(t *testing.T) {
	c := formTestClient(t)
	p, clean, err := c.PlanRequestRoleWithValues("person", "role", "2099-10-01T00:00:00+02:00", "2099-10-31T23:59:59+01:00", map[string]any{"projectCode": "P", "acknowledged": true})
	if err != nil {
		t.Fatal(err)
	}
	if p.Method != "PATCH" || p.Endpoint() != "/ws/rest/users/person" || len(clean) != 2 {
		t.Fatalf("plan %+v", p)
	}
	b, _ := json.Marshal(p.Body)
	var body struct {
		ObjectModification struct {
			ItemDelta []struct {
				ModificationType string
				Path             string
				Value            map[string]any
			}
		}
	}
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	d := body.ObjectModification.ItemDelta
	if len(d) != 1 || d[0].ModificationType != "add" || d[0].Path != "assignment" {
		t.Fatalf("body %s", b)
	}
	if !reflect.DeepEqual(d[0].Value["targetRef"], map[string]any{"oid": "role", "type": "RoleType"}) {
		t.Error(string(b))
	}
	if strings.Contains(string(b), "comment") || d[0].Value["activation"] == nil || d[0].Value["extension"] == nil {
		t.Error(string(b))
	}
}

// The settings of 0.5 load whatever they hold, and a schema that fails to
// read or parse leaves no form without failing startup.
func TestSchemaFailuresAndConfig(t *testing.T) {
	for _, names := range [][]string{{"invalid"}, {requestNS + "same", "{urn:other}same"}, {requestNS + "same", requestNS + "same"}} {
		fc := FileConfig{Requests: RequestsConfig{FormItems: names, JustificationItem: "not a qname"}}
		if err := fc.validate(); err != nil {
			t.Errorf("old settings %v refused: %v", names, err)
		}
	}
	for _, response := range []struct {
		status int
		body   string
	}{{403, ""}, {200, "broken XML"}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(response.status)
			_, _ = io.WriteString(w, response.body)
		}))
		c := NewClient(Config{BaseURL: srv.URL})
		if err := c.LoadRequestForm(context.Background(), nil); err != nil || c.RequestForm() != nil {
			t.Errorf("%d %q: err %v form %+v", response.status, response.body, err, c.RequestForm())
		}
		srv.Close()
	}
}

// An approver sees every field the request carries (D43), labelled from the
// schema and in its order, a choice by its label and a field the form doesn't
// know by its name, last.
func TestRequestDetailsLabelled(t *testing.T) {
	c := formTestClient(t)
	value := map[string]json.RawMessage{"extension": json.RawMessage(`{
		"@ns": "http://example.com/xml/ns/access-request",
		"zzLegacy": "kept",
		"environments": ["dev", "test"],
		"region": "eu",
		"http://example.com/xml/ns/access-request#projectCode": {"@type": "xsd:string", "@value": "OPS-7"},
		"acknowledged": false,
		"justification": {"orig": "Quarter end", "norm": "quarter end"},
		"empty": ""}`)}
	got := c.requestDetails(value)
	want := []RequestDetail{
		{Name: "justification", Label: "Justification", Type: "string", Values: []string{"Quarter end"}},
		{Name: "projectCode", Label: "Project code", Type: "string", Values: []string{"OPS-7"}},
		{Name: "acknowledged", Label: "Policy acknowledged", Type: "boolean", Values: []string{"false"}},
		{Name: "region", Label: "Region", Type: "choice", Values: []string{"eu"}, Labels: []string{"Europe"}},
		{Name: "environments", Label: "Environments", Type: "string", Values: []string{"dev", "test"}},
		{Name: "zzLegacy", Values: []string{"kept"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("details\n got %+v\nwant %+v", got, want)
	}
}

// The comment typed in midPoint's own Request access page is on the case's
// creation event (live on 4.10.3); a case read without its events has none.
func TestRequesterComment(t *testing.T) {
	var cj caseJSON
	if err := json.Unmarshal([]byte(`{"oid":"c","event":{"@type":"c:CaseCreationEventType","@id":4,"timestamp":"2026-10-04T06:40:30.663Z",
		"initiatorRef":{"oid":"00000000-0000-0000-0000-000000000002","type":"c:UserType"},
		"businessContext":{"comment":" Needed for the quarter-end close. "}}}`), &cj); err != nil {
		t.Fatal(err)
	}
	if got := cj.requesterComment(); got != "Needed for the quarter-end close." {
		t.Errorf("comment %q", got)
	}
	if got := (caseJSON{}).requesterComment(); got != "" {
		t.Errorf("comment without events %q", got)
	}
}
