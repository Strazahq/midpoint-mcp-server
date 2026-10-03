package main

import (
	"fmt"
	"strings"

	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

// A write tool names the objects it changes twice: by OID, which is what it
// acts on, and by midPoint name (the unique `name` attribute, not the display
// name), which is what a person reads when their app asks them to confirm the
// call. confirmName checks that the two agree before anything is written, so
// a confirmation can't show one object while the call changes another.

// nameArgsNote ends the description of every write tool that takes names.
const nameArgsNote = "userName and roleName are the midPoint names (the unique name attribute, such as a login, not display names) " +
	"of the objects the OIDs point to, as list and get results give them; the call is refused when they don't match, " +
	"so the person confirming it reads the right names."

const nameArgNote = "midPoint name (the unique name attribute, e.g. a login, not the display name)"

// confirmName checks the name a caller gave for field against the name midPoint
// shows the acting identity. An empty actual name means midPoint hides it; the
// argument must then be empty too, since nothing can confirm it.
func confirmName(field, kind, given, actual string) error {
	given, actual = strings.TrimSpace(given), strings.TrimSpace(actual)
	switch {
	case actual == "" && given == "":
		return nil
	case actual == "":
		return nameError(fmt.Errorf("refused: %s %q can't be confirmed, because midPoint doesn't show you this %s's name; leave %s empty",
			field, given, kind, field))
	case given == "":
		return nameError(fmt.Errorf("%s is required: the %s of the %s, which is %q here; the person sees it when they confirm this call",
			field, nameArgNote, kind, actual))
	case !strings.EqualFold(given, actual):
		return nameError(fmt.Errorf("refused: %s %q doesn't match the %s; its midPoint name is %q", field, given, kind, actual))
	}
	return nil
}

// confirmRef is confirmName for a reference the server resolved.
func confirmRef(field, kind, given string, ref *midpoint.ObjectRef) error {
	actual := ""
	if ref != nil {
		actual = ref.Name
	}
	return confirmName(field, kind, given, actual)
}

func nameError(err error) error {
	return &midpoint.CodedError{Code: midpoint.CodeInvalidInput, Err: err}
}
