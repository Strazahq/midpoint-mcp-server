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
		{http.StatusConflict, CodeInternal},
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
