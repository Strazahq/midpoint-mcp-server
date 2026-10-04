package midpoint

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestCatalogSearch(t *testing.T) {
	var filter string
	var max int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(SwitchToPrincipalHeader) != "manager" {
			t.Error("catalog read not as caller")
		}
		switch r.URL.Path {
		case "/ws/rest/roles/search":
			var req searchRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			filter = req.Query.Filter.Text
			max = req.Query.Paging.MaxSize
			_, _ = io.WriteString(w, `{"object":[{"oid":"held","name":"held"},{"oid":"free","name":"catalog","riskLevel":"high"}]}`)
		case "/ws/rest/users/report":
			_, _ = io.WriteString(w, `{"user":{"oid":"report","name":"report-login","fullName":"Alex Morgan","roleMembershipRef":{"oid":"held","type":"RoleType"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := NewClient(Config{BaseURL: srv.URL})
	got, err := c.RequestableCatalog(WithPrincipal(context.Background(), "manager"), "report", `  he"llo\  `, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Roles) != 1 || got.Roles[0].RiskLevel != "high" || !got.LimitReached || got.ForUserRef.DisplayName != "Alex Morgan" {
		t.Fatalf("catalog %+v", got)
	}
	want := `requestable = true and (name contains[origIgnoreCase] "he\"llo\\" or displayName contains[origIgnoreCase] "he\"llo\\")`
	if filter != want || max != 2 || got.Query != `he"llo\` {
		t.Fatalf("query %q (%d), echo %q", filter, max, got.Query)
	}
	if _, err = c.RequestableCatalog(context.Background(), "", strings.Repeat("界", 101), 100); err == nil {
		t.Error("overlong query accepted")
	}
}
