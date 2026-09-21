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

// newSearchClient serves /users/search, dispatching the response by inspecting
// the query-language filter text of each request.
func newSearchClient(t *testing.T, respond func(filter string) (int, string)) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var sr searchRequest
		_ = json.Unmarshal(body, &sr)
		filter := ""
		if sr.Query.Filter != nil {
			filter = sr.Query.Filter.Text
		}
		status, resp := respond(filter)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)
	return NewClient(Config{BaseURL: srv.URL, Username: "svc", Password: "p"})
}

func TestCorrelateUser(t *testing.T) {
	one := func(oid string) string { return `{"object":[{"oid":"` + oid + `","name":"x"}]}` }
	empty := `{"object":[]}`
	two := `{"object":[{"oid":"a"},{"oid":"b"}]}`

	tests := []struct {
		name              string
		subject, username string
		attribute         string   // "" = default (name)
		archetypes        []string // nil = a person's token, no archetype guard
		respond           func(filter string) (int, string)
		wantOID           string
		wantErr           bool
	}{
		{
			name: "externalId primary wins", subject: "sub-1", username: "jdoe",
			respond: func(f string) (int, string) {
				if strings.Contains(f, "externalId") {
					return 200, one("oid-ext")
				}
				return 200, one("oid-name")
			},
			wantOID: "oid-ext",
		},
		{
			name: "falls back to name when externalId has no match", subject: "sub-1", username: "jdoe",
			respond: func(f string) (int, string) {
				if strings.Contains(f, "externalId") {
					return 200, empty
				}
				return 200, one("oid-name")
			},
			wantOID: "oid-name",
		},
		{
			name: "falls back to name when externalId path errors", subject: "sub-1", username: "jdoe",
			respond: func(f string) (int, string) {
				if strings.Contains(f, "externalId") {
					return 400, `{"error":"unknown path externalId"}`
				}
				return 200, one("oid-name")
			},
			wantOID: "oid-name",
		},
		{
			name: "subject only", subject: "sub-1", username: "",
			respond: func(f string) (int, string) {
				if strings.Contains(f, "externalId") {
					return 200, one("oid-ext")
				}
				return 200, empty
			},
			wantOID: "oid-ext",
		},
		{
			// A custom attribute must be used verbatim in the fallback filter.
			name: "custom attribute (emailAddress)", subject: "", username: "jane@example.com", attribute: "emailAddress",
			respond: func(f string) (int, string) {
				if strings.Contains(f, `emailAddress = "jane@example.com"`) {
					return 200, one("oid-email")
				}
				return 200, empty
			},
			wantOID: "oid-email",
		},
		{
			// With no archetypes the filter is exactly what it was before the
			// client settings existed.
			name: "person token sends the bare name filter", subject: "", username: "jdoe",
			respond: func(f string) (int, string) {
				if f == `name = "jdoe"` {
					return 200, one("oid-name")
				}
				return 200, empty
			},
			wantOID: "oid-name",
		},
		{
			name: "client token requires the archetype on the name query", subject: "", username: "build-agent",
			archetypes: []string{"oid-agent"},
			respond: func(f string) (int, string) {
				if f == `name = "build-agent" and (archetypeRef matches (oid = "oid-agent"))` {
					return 200, one("oid-build-agent")
				}
				return 200, empty
			},
			wantOID: "oid-build-agent",
		},
		{
			name: "several archetypes are OR-ed", subject: "", username: "build-agent",
			archetypes: []string{"oid-agent", "oid-service"},
			respond: func(f string) (int, string) {
				if f == `name = "build-agent" and (archetypeRef matches (oid = "oid-agent") or archetypeRef matches (oid = "oid-service"))` {
					return 200, one("oid-build-agent")
				}
				return 200, empty
			},
			wantOID: "oid-build-agent",
		},
		{
			name: "client token requires the archetype on the externalId query", subject: "sub-1", username: "",
			archetypes: []string{"oid-agent"},
			respond: func(f string) (int, string) {
				if f == `externalId = "sub-1" and (archetypeRef matches (oid = "oid-agent"))` {
					return 200, one("oid-ext")
				}
				return 200, empty
			},
			wantOID: "oid-ext",
		},
		{
			// midPoint answers an unguarded query with the person and a guarded
			// one with nobody, as it does for a user without the archetype.
			name: "client named like a person is refused", subject: "sub-1", username: "alice",
			archetypes: []string{"oid-agent"},
			respond: func(f string) (int, string) {
				if strings.Contains(f, "archetypeRef") {
					return 200, empty
				}
				return 200, one("oid-alice")
			},
			wantErr: true,
		},
		{
			name: "no match anywhere", subject: "sub-1", username: "jdoe",
			respond: func(string) (int, string) { return 200, empty },
			wantErr: true,
		},
		{
			name: "ambiguous name is an error", subject: "", username: "jdoe",
			respond: func(string) (int, string) { return 200, two },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newSearchClient(t, tt.respond)
			got, err := c.CorrelateUser(context.Background(), tt.subject, tt.username, tt.attribute, tt.archetypes)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("CorrelateUser = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("CorrelateUser: %v", err)
			}
			if got != tt.wantOID {
				t.Errorf("CorrelateUser = %q, want %q", got, tt.wantOID)
			}
		})
	}
}

// A refused client token must tell the operator which setting and which midPoint
// fact to look at. A person's refusal keeps its wording.
func TestCorrelateUserNoMatchMessages(t *testing.T) {
	c := newSearchClient(t, func(string) (int, string) { return 200, `{"object":[]}` })

	_, err := c.CorrelateUser(context.Background(), "sub-1", "jdoe", "", nil)
	if err == nil || err.Error() != `no midPoint user matches subject="sub-1" name="jdoe"` {
		t.Errorf("person no-match error = %v", err)
	}

	_, err = c.CorrelateUser(context.Background(), "sub-2", "build-agent", "", []string{"oid-agent"})
	if err == nil {
		t.Fatal("client no-match: want an error")
	}
	for _, want := range []string{`subject="sub-2"`, `name="build-agent"`, EnvOIDCClientArchetypes, "archetype"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("client no-match error %q does not name %q", err, want)
		}
	}
}

func TestSwitchToPrincipalHeader(t *testing.T) {
	var got string
	var seen bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get(SwitchToPrincipalHeader)
		_, seen = r.Header[SwitchToPrincipalHeader]
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"user":{"oid":"x","name":"x"}}`)
	}))
	defer srv.Close()
	c := NewClient(Config{BaseURL: srv.URL, Username: "svc", Password: "p"})

	// Personal mode: no principal in context → no impersonation header.
	if _, err := c.Self(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen {
		t.Errorf("Switch-To-Principal sent without a principal: %q", got)
	}

	// Resource-server mode: principal in context → header carries the target oid.
	if _, err := c.Self(WithPrincipal(context.Background(), "target-oid")); err != nil {
		t.Fatal(err)
	}
	if got != "target-oid" {
		t.Errorf("Switch-To-Principal = %q, want target-oid", got)
	}
}
