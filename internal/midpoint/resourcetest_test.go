package midpoint

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestResourceBriefReadsWithoutFetching(t *testing.T) {
	c, reqs := taskServer(t, map[string]func(http.ResponseWriter, *http.Request){
		"GET /ws/rest/resources/{oid}": answer(200, []byte(`{"resource":{"oid":"r-1","name":"HR CSV",`+
			`"operationalState":{"lastAvailabilityStatus":"broken","message":"Status set to BROKEN"}}}`)),
	})
	b, err := c.ResourceBriefOf(context.Background(), "r-1")
	if err != nil || b != (ResourceBrief{OID: "r-1", Name: "HR CSV", Availability: "broken"}) {
		t.Fatalf("ResourceBriefOf = %+v, %v", b, err)
	}
	q := (*reqs)[0].rawQuery
	if !strings.Contains(q, "options=noFetch") || !strings.Contains(q, "exclude=schema") {
		t.Errorf("query = %s", q)
	}
	plan, err := c.PlanTestResource(b)
	if err != nil || plan.Method != http.MethodPost || plan.Path != "/resources/r-1/test" || plan.Summary != `Test resource "HR CSV"` {
		t.Errorf("plan = %+v, %v", plan, err)
	}
}

func TestResourceTestFailed(t *testing.T) {
	// A failed test is HTTP 200 with a fatal_error result (live, 4.10.3).
	c, _ := taskServer(t, map[string]func(http.ResponseWriter, *http.Request){
		"POST /ws/rest/resources/{oid}/test": answer(200, fixture(t, "resource_test_fatal.json")),
	})
	res, err := c.TestResource(context.Background(), Plan{Method: http.MethodPost, Path: "/resources/r-1/test"})
	if err != nil {
		t.Fatalf("TestResource: %v", err)
	}
	if res.Status != "fatal_error" || !strings.HasPrefix(res.Message, "Connector initialization failed.") {
		t.Errorf("result = %s %q", res.Status, res.Message)
	}
	var names, failed []string
	for _, c := range res.Checks {
		names = append(names, c.Name+"="+c.Status)
		if c.Failed() {
			failed = append(failed, c.Name)
			if c.Message == "" {
				t.Errorf("failed check %s has no message", c.Name)
			}
		}
	}
	want := "connector=fatal_error connector.instantiation=success connector.initialization=fatal_error"
	if strings.Join(names, " ") != want {
		t.Errorf("checks = %s", strings.Join(names, " "))
	}
	if strings.Join(failed, " ") != "connector connector.initialization" {
		t.Errorf("failed = %v", failed)
	}
}

func TestResourceTestPassed(t *testing.T) {
	c, _ := taskServer(t, map[string]func(http.ResponseWriter, *http.Request){
		"POST /ws/rest/resources/{oid}/test": answer(200, fixture(t, "resource_test_success.json")),
	})
	res, err := c.TestResource(context.Background(), Plan{Method: http.MethodPost, Path: "/resources/r-1/test"})
	if err != nil || res.Status != "success" || res.Message != "" {
		t.Fatalf("TestResource = %+v, %v", res, err)
	}
	var names []string
	for _, c := range res.Checks {
		if c.Failed() {
			t.Errorf("check %s failed", c.Name)
		}
		names = append(names, c.Name)
	}
	want := "connector connector.instantiation connector.initialization connector.connection connector.capabilities resourceSchema"
	if strings.Join(names, " ") != want {
		t.Errorf("checks = %s", strings.Join(names, " "))
	}
}

func TestResourceTestRefused(t *testing.T) {
	c, _ := taskServer(t, map[string]func(http.ResponseWriter, *http.Request){
		// The REST action authorization is checked before the controller: an
		// End user gets Spring's HTML error page (live).
		"POST /ws/rest/resources/{oid}/test": answer(403, []byte("<!DOCTYPE html>")),
	})
	_, err := c.TestResource(context.Background(), Plan{Method: http.MethodPost, Path: "/resources/r-1/test"})
	if code, _ := ErrorCode(err); code != CodeNotAuthorized {
		t.Errorf("err = %v (%s)", err, code)
	}
}
