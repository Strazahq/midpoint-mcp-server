package midpoint

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A non-2xx answer keeps the text it had before it was typed, and its code
// follows the status.
func TestStatusErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
	}{
		{http.StatusBadRequest, CodeInternal},
		{http.StatusUnauthorized, CodeNotAuthorized},
		{http.StatusForbidden, CodeNotAuthorized},
		{http.StatusNotFound, CodeNotFound},
		{http.StatusConflict, CodeRefused},
		{http.StatusInternalServerError, CodeMidpointUnavailable},
		{http.StatusBadGateway, CodeMidpointUnavailable},
		{http.StatusServiceUnavailable, CodeMidpointUnavailable},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
		}))
		_, err := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"}).Self(context.Background())
		srv.Close()

		want := fmt.Sprintf("midPoint /self: unexpected status %d %s", tc.status, http.StatusText(tc.status))
		if err == nil || err.Error() != want {
			t.Errorf("%d: error = %v, want %q", tc.status, err, want)
			continue
		}
		var se *StatusError
		if !errors.As(err, &se) || se.StatusCode != tc.status || se.Path != "/self" {
			t.Errorf("%d: error %#v is not a StatusError for /self", tc.status, err)
		}
		if code, _ := ErrorCode(err); code != tc.code {
			t.Errorf("%d: code = %q, want %q", tc.status, code, tc.code)
		}
	}
}

func TestTransportErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close() // the dial fails

	_, err := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"}).Self(context.Background())
	if err == nil || !strings.HasPrefix(err.Error(), "calling midPoint GET /self: ") {
		t.Fatalf("error = %v, want a transport failure", err)
	}
	if code, _ := ErrorCode(err); code != CodeMidpointUnavailable {
		t.Errorf("code = %q, want %q", code, CodeMidpointUnavailable)
	}
}

// Codes survive wrapping, the outermost code wins, and the text is the
// wrapped error's.
func TestErrorCode(t *testing.T) {
	forbidden := &StatusError{Path: "/rpc/executeScript", StatusCode: http.StatusForbidden, Status: "403 Forbidden"}
	for _, tc := range []struct {
		name, code, field string
		err               error
	}{
		{"nil", "", "", nil},
		{"uncoded", "", "", errors.New("boom")},
		{"status", CodeNotAuthorized, "", forbidden},
		{"wrapped status", CodeNotFound, "",
			fmt.Errorf("reading case: %w", &StatusError{Path: "/cases/x", StatusCode: 404, Status: "404 Not Found"})},
		{"sentinel twice wrapped", CodeSharedCredential, "",
			fmt.Errorf("resolving the acting identity: %w", fmt.Errorf("resolving self: %w", ErrNoCallerIdentity))},
		{"outermost wins", CodeAuditUnavailable, "", &CodedError{Code: CodeAuditUnavailable, Err: forbidden}},
		{"field", CodeInvalidField, "costCenter",
			fmt.Errorf("x: %w", &CodedError{Code: CodeInvalidField, Field: "costCenter", Err: errors.New("invalid request field")})},
	} {
		code, field := ErrorCode(tc.err)
		if code != tc.code || field != tc.field {
			t.Errorf("%s: ErrorCode = %q, %q; want %q, %q", tc.name, code, field, tc.code, tc.field)
		}
	}

	wrapped := &CodedError{Code: CodeAuditUnavailable, Err: forbidden}
	if wrapped.Error() != forbidden.Error() {
		t.Errorf("coded text = %q, want %q", wrapped.Error(), forbidden.Error())
	}
	if err := fmt.Errorf("x: %w", ErrNoCallerIdentity); !errors.Is(err, ErrNoCallerIdentity) {
		t.Error("a wrapped ErrNoCallerIdentity is no longer recognised")
	}
}

// midPoint's refusals say why (D42). The answers are recorded from midPoint
// 4.10.3 (S29), with names and OIDs neutral; the list form was seen live when
// a rule's constraints were combined.
func TestStatusErrorSays(t *testing.T) {
	for _, tc := range []struct {
		file, code, reason, message string
		status                      int
	}{
		{"request_refused_policy.json", CodeRefused,
			"Requests for this role need a justification.",
			"Could not modify object. Requests for this role need a justification.", http.StatusConflict},
		{"request_refused_policy_list.json", CodeRefused,
			"Requests for this role need a justification, except from managers.",
			`Could not modify object. Assignment of role "Database admin" (relation default) is to be added; Requests for this role need a justification, except from managers.`,
			http.StatusConflict},
		{"request_refused_authorization.json", CodeNotAuthorized,
			"User 'bstone' not authorized for operation with assignment on bstone with target Database admin",
			"Could not modify object. User ''bstone'' not authorized for operation with assignment on user:10000000-0000-0000-0000-0000000000b1(bstone) with target role:20000000-0000-0000-0000-0000000000f1(Database admin)",
			http.StatusForbidden},
		{"request_failed_expression.json", CodeInternal, "",
			"Could not modify object. Groovy Evaluation Failed: No such property: undefinedVariable for class: (new)_expression_in_assignment_state_constraint_null_(AFTER)",
			http.StatusBadRequest},
	} {
		body := fixture(t, tc.file)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write(body)
		}))
		_, err := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"}).Self(context.Background())
		srv.Close()
		reason, message := MidpointSaid(err)
		if reason != tc.reason || message != tc.message {
			t.Errorf("%s:\nreason  %q\nwant    %q\nmessage %q\nwant    %q", tc.file, reason, tc.reason, message, tc.message)
		}
		if code, _ := ErrorCode(err); code != tc.code {
			t.Errorf("%s: code %q, want %q", tc.file, code, tc.code)
		}
		if want := fmt.Sprintf("midPoint /self: unexpected status %d %s", tc.status, http.StatusText(tc.status)); err.Error() != want {
			t.Errorf("%s: text %q changed", tc.file, err)
		}
	}
}

// Text from midPoint leaves the server on one line, without addresses, and cut.
func TestAnswerText(t *testing.T) {
	if got := answerText("Connection to ldaps://dir.example.com:636/ou=x failed\n\tretry", 100); got != "Connection to [address removed] failed retry" {
		t.Errorf("answerText = %q", got)
	}
	if got := answerText(strings.Repeat("é", 10), 5); got != "éééé…" {
		t.Errorf("cut = %q", got)
	}
	if got := readableReason("on user:10000000-0000-0000-0000-0000000000b1(bstone), not ''x''"); got != "on bstone, not 'x'" {
		t.Errorf("readableReason = %q", got)
	}
	if r, m := MidpointSaid(errors.New("plain")); r != "" || m != "" {
		t.Errorf("a plain error said %q %q", r, m)
	}
}
