package midpoint

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// --- operation results ---

// opResultJSON is midPoint's OperationResultType, as far as it is read here.
type opResultJSON struct {
	Operation           string           `json:"operation"`
	Status              string           `json:"status"`
	Message             string           `json:"message"`
	UserFriendlyMessage *localizableJSON `json:"userFriendlyMessage"`
	PartialResults      flexSlice        `json:"partialResults"`
}

// decodeOpResult reads an operation result answered as a REST body, wrapped as
// {"object":{"@type":"c:OperationResultType",...}}.
func decodeOpResult(body []byte) (opResultJSON, bool) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' {
		return opResultJSON{}, false
	}
	var env struct {
		Object json.RawMessage `json:"object"`
	}
	if json.Unmarshal(body, &env) != nil || len(env.Object) == 0 {
		return opResultJSON{}, false
	}
	var r opResultJSON
	if json.Unmarshal(env.Object, &r) != nil || r.Status == "" {
		return opResultJSON{}, false
	}
	return r, true
}

func (r opResultJSON) children() []opResultJSON {
	out := make([]opResultJSON, 0, len(r.PartialResults))
	for _, raw := range r.PartialResults {
		var p opResultJSON
		if json.Unmarshal(raw, &p) == nil {
			out = append(out, p)
		}
	}
	return out
}

// message is the result's own message, or else the first message of a
// sub-result with the same status (depth first), so a failed result whose top
// says nothing still tells why.
func (r opResultJSON) message() string {
	if m := strings.TrimSpace(r.Message); m != "" {
		return m
	}
	if r.Status == "" || r.Status == "success" {
		return ""
	}
	for _, p := range r.children() {
		if p.Status == r.Status {
			if m := p.message(); m != "" {
				return m
			}
		}
	}
	return ""
}

// reason is the message midPoint meant for people: the readable text of the
// result's userFriendlyMessage, found the way message finds a message. A
// policy rule's own message arrives here (live on 4.10.3: an enforcement
// rule's fallbackMessage, HTTP 409); midPoint's other answers may have none.
func (r opResultJSON) reason() string {
	if r.UserFriendlyMessage != nil {
		if m := r.UserFriendlyMessage.text(); m != "" {
			return m
		}
	}
	if r.Status == "" || r.Status == "success" {
		return ""
	}
	for _, p := range r.children() {
		if p.Status == r.Status {
			if m := p.reason(); m != "" {
				return m
			}
		}
	}
	return ""
}

// localizableJSON is midPoint's LocalizableMessageType: a single message (a
// key, its arguments and a fallback text) or a list of them with a separator.
type localizableJSON struct {
	Key       string           `json:"key"`
	Fallback  string           `json:"fallbackMessage"`
	Message   flexSlice        `json:"message"`
	Separator *localizableJSON `json:"separator"`
}

// text is what can be read without midPoint's localization catalog: a single
// message's fallback text, or the readable parts of a list joined by its
// separator. A part that has only a key is left out.
func (m localizableJSON) text() string {
	if s := strings.TrimSpace(m.Fallback); s != "" {
		return s
	}
	var parts []string
	for _, raw := range m.Message {
		var p localizableJSON
		if json.Unmarshal(raw, &p) == nil {
			if s := p.text(); s != "" {
				parts = append(parts, s)
			}
		}
	}
	sep := "; "
	if m.Separator != nil && m.Separator.Fallback != "" {
		sep = m.Separator.Fallback
	}
	return strings.Join(parts, sep)
}

// --- midPoint's text, made safe to pass on ---

const (
	maxAnswerMessage = 600 // runes of a technical message kept
	maxAnswerReason  = 300 // runes of a reason kept
)

var (
	// anAddress is a URL of any scheme; midPoint's messages can name a
	// connected system's address, which never leaves the server (S14).
	anAddress = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9+.-]*://[^\s"'<>]+`)
	// anObjectRef is midPoint's "type:oid(name)" spelling of an object.
	anObjectRef = regexp.MustCompile(`\b[A-Za-z]+:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\(([^()]*)\)`)
)

// answerText is midPoint's text on one line: control characters and runs of
// white space become one space, addresses are removed, and it is cut at limit
// runes.
func answerText(s string, limit int) string {
	s = anAddress.ReplaceAllString(s, "[address removed]")
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || r == ' ' || r == ' '
	}), " ")
	if utf8.RuneCountInString(s) > limit {
		s = string([]rune(s)[:limit-1]) + "…"
	}
	return s
}

// readableReason is a reason as a person reads it: objects by name instead of
// "type:oid(name)", and the apostrophes midPoint's message formatting doubles
// ("User ”bob”") single again.
func readableReason(s string) string {
	s = anObjectRef.ReplaceAllString(s, "$1")
	s = strings.ReplaceAll(s, "''", "'")
	return answerText(s, maxAnswerReason)
}
