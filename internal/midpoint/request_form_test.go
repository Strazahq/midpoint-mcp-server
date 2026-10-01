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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(SwitchToPrincipalHeader) != "" {
			t.Error("schema discovery impersonated a user")
		}
		if r.Header.Get("Accept") != "application/xml" {
			t.Error("schema discovery must request XML")
		}
		files := map[string]string{"/ws/rest/schemas": "request_schema_db.xml", "/ws/schema": "request_schema_files.xml", "/ws/schema/request-extension.xsd": "request_schema_file.xsd"}
		f, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		b, err := os.ReadFile("testdata/" + f)
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	cfg := Config{BaseURL: srv.URL}
	cfg.File.Requests.JustificationItem = requestNS + "justification"
	for _, n := range []string{"projectCode", "justification", "ticket", "acknowledged", "neededOn", "handover", "multiple", "decimal", "absent", "userOnly"} {
		cfg.File.Requests.FormItems = append(cfg.File.Requests.FormItems, requestNS+n)
	}
	cfg.File.Requests.FormItems = append(cfg.File.Requests.FormItems, "{http://example.com/xml/ns/request-file}costCenter")
	c := NewClient(cfg)
	var warnings []string
	if err := c.LoadRequestForm(WithPrincipal(context.Background(), "end-user"), func(s string) { warnings = append(warnings, s) }); err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 4 {
		t.Fatalf("warnings %v", warnings)
	}
	return c
}

func TestRequestFormSchemaSources(t *testing.T) {
	c := formTestClient(t)
	f := c.RequestForm()
	if len(f.Items) != 7 {
		t.Fatalf("items %+v", f)
	}
	if f.Items[0].Name != "projectCode" || !f.Items[0].Required || f.Items[0].DisplayName != "Project code" || f.Items[0].Help == "" {
		t.Fatalf("first %+v", f.Items[0])
	}
	if !f.Items[1].Justification || !f.Items[1].Multiline || f.Items[1].Required {
		t.Fatalf("justification %+v", f.Items[1])
	}
	if f.Items[6].Name != "costCenter" {
		t.Fatalf("file item %+v", f.Items[6])
	}
}

func TestRequestFieldsValidation(t *testing.T) {
	c := formTestClient(t)
	for _, tc := range []struct {
		name   string
		fields map[string]any
		field  string
	}{
		{"required", map[string]any{}, "projectCode"},
		{"blank", map[string]any{"projectCode": "  "}, "projectCode"},
		{"unknown", map[string]any{"secret": "x"}, "secret"},
		{"boolean missing", map[string]any{"projectCode": "P"}, "acknowledged"},
		{"boolean wrong", map[string]any{"projectCode": "P", "acknowledged": "true"}, "acknowledged"},
		{"int fraction", map[string]any{"projectCode": "P", "ticket": 1.5, "acknowledged": false}, "ticket"},
		{"int range", map[string]any{"projectCode": "P", "ticket": float64(1 << 32), "acknowledged": false}, "ticket"},
		{"date invalid", map[string]any{"projectCode": "P", "neededOn": "2028-02-30", "acknowledged": true}, "neededOn"},
		{"datetime no offset", map[string]any{"projectCode": "P", "handover": "2028-10-01T12:00:00", "acknowledged": true}, "handover"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := c.ValidateRequestFields(tc.fields)
			code, field := ErrorCode(err)
			if code != CodeInvalidField || field != tc.field {
				t.Fatalf("error %v code %s field %s", err, code, field)
			}
		})
	}
	good := map[string]any{"projectCode": "P-7", "acknowledged": false, "justification": "  ", "ticket": float64(7), "neededOn": "2028-02-29", "handover": "2028-10-01T12:30:00+02:00", "costCenter": "Research"}
	clean, ext, err := c.ValidateRequestFields(good)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := clean["justification"]; ok {
		t.Error("empty optional value not omitted")
	}
	if clean["acknowledged"] != false || len(ext) != 6 {
		t.Errorf("values %v %v", clean, ext)
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

func TestSchemaFailuresAndConfig(t *testing.T) {
	for _, names := range [][]string{{"invalid"}, {requestNS + "same", "{urn:other}same"}, {requestNS + "same", requestNS + "same"}} {
		fc := FileConfig{Requests: RequestsConfig{FormItems: names}}
		if err := fc.validate(); err == nil {
			t.Errorf("accepted %v", names)
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
		c := NewClient(Config{BaseURL: srv.URL, File: FileConfig{Requests: RequestsConfig{FormItems: []string{requestNS + "projectCode"}}}})
		if err := c.LoadRequestForm(context.Background(), nil); err == nil {
			t.Error("schema failure did not fail startup")
		}
		srv.Close()
	}
	if err := NewClient(Config{}).LoadRequestForm(context.Background(), nil); err != nil {
		t.Error("unconfigured form contacted server")
	}
}
