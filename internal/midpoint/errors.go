package midpoint

import (
	"errors"
	"fmt"
	"net/http"
)

// Stable error codes (docs/ui-contract.md 6.8, S18). Every error result carries
// one, so a view tells failures apart without matching their text. The codes
// are part of the contract: renaming or removing one breaks views.
const (
	CodeSharedCredential    = "shared-credential"    // ErrNoCallerIdentity
	CodeNotRequestable      = "not-requestable"      // EnsureRequestable
	CodeNotYourRequest      = "not-your-request"     // withdrawing someone else's request (S16)
	CodeRequestClosed       = "request-closed"       // the case is not open
	CodeAlreadyDecided      = "already-decided"      // the work item is closed, the case is not
	CodeNotInInbox          = "not-in-inbox"         // the work item is not the caller's to decide
	CodeNotClaimed          = "not-claimed"          // the work item is offered to a group and nobody claimed it (Q4)
	CodeNotAssigned         = "not-assigned"         // no direct assignment to remove
	CodeAuditUnavailable    = "audit-unavailable"    // the audit script path failed
	CodeInvalidInput        = "invalid-input"        // arguments the tool cannot use
	CodeInvalidField        = "invalid-field"        // a request form item failed validation (S21)
	CodeInvalidValidity     = "invalid-validity"     // a requested validity failed validation (S22)
	CodeNotAuthorized       = "not-authorized"       // HTTP 401 or 403 from midPoint
	CodeRefused             = "refused"              // HTTP 409 from midPoint: a policy rule, or a conflict
	CodeNotFound            = "not-found"            // HTTP 404 from midPoint
	CodeMidpointUnavailable = "midpoint-unavailable" // HTTP 5xx, or midPoint not reached
	CodeInternal            = "internal"             // anything else
)

// CodedError gives an error a stable code. Its text is the wrapped error's,
// unchanged.
type CodedError struct {
	Code  string
	Field string // the request form item, for CodeInvalidField
	Err   error
}

func (e *CodedError) Error() string { return e.Err.Error() }
func (e *CodedError) Unwrap() error { return e.Err }

// StatusError is a non-2xx answer from midPoint. Its text names the REST path
// and the status line, never the base URL. What midPoint said about it comes
// from the operation result it answered with; both are untrusted text from
// midPoint, on one line, without addresses.
type StatusError struct {
	Path       string
	StatusCode int
	Status     string // the status line, e.g. "404 Not Found"
	// Message is the result's message, or a failed sub-result's: midPoint's
	// technical account of the failure, or "".
	Message string
	// Reason is the result's message meant for people (its
	// userFriendlyMessage), with objects named and not "type:oid(name)", or "".
	Reason string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("midPoint %s: unexpected status %s", e.Path, e.Status)
}

// coded is implemented by the errors that carry a code.
type coded interface {
	code() (code, field string)
}

func (e *CodedError) code() (string, string) { return e.Code, e.Field }

func (e *StatusError) code() (string, string) {
	switch {
	case e.StatusCode == http.StatusUnauthorized, e.StatusCode == http.StatusForbidden:
		return CodeNotAuthorized, ""
	case e.StatusCode == http.StatusNotFound:
		return CodeNotFound, ""
	case e.StatusCode == http.StatusConflict:
		return CodeRefused, ""
	case e.StatusCode >= 500:
		return CodeMidpointUnavailable, ""
	}
	return CodeInternal, ""
}

// MidpointSaid returns what midPoint said about the first status error in
// err's chain: its reason for people and its technical message, either ""
// when midPoint said nothing or the error is not midPoint's answer.
func MidpointSaid(err error) (reason, message string) {
	var se *StatusError
	if !errors.As(err, &se) {
		return "", ""
	}
	return se.Reason, se.Message
}

// ErrorCode returns the code of the first coded error in err's chain, or ""
// when there is none. The outermost code wins, so a caller that wraps a
// failure with a code of its own overrides the code of the cause.
func ErrorCode(err error) (code, field string) {
	var c coded
	if !errors.As(err, &c) {
		return "", ""
	}
	return c.code()
}
