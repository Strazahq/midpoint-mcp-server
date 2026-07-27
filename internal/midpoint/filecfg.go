package midpoint

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// EnvConfigFile names a JSON file of NON-SECRET settings: how this deployment
// models teams, and which self-service guardrails apply. Credentials are never
// read from it — they stay in the environment (see ConfigFromEnv).
const EnvConfigFile = "MIDPOINT_MCP_CONFIG"

// Org sources for the team tools. midPoint maintains parentOrgRef as the
// computed org membership, but a deployment whose orgs are modelled purely as
// assignments (or whose recompute has not run) can have the assignment and not
// the computed ref — hence the fallback.
const (
	// OrgSourceParentOrgRef uses the computed parentOrgRef only (the default).
	OrgSourceParentOrgRef = "parentOrgRef"
	// OrgSourceAssignment uses org assignments only.
	OrgSourceAssignment = "assignment"
	// OrgSourceFallback uses parentOrgRef, falling back to assignments only when
	// parentOrgRef yields nothing.
	OrgSourceFallback = "fallback"
	// OrgSourceBoth always unions the two.
	OrgSourceBoth = "both"
)

// FileConfig is the on-disk settings file.
type FileConfig struct {
	Identity IdentityConfig `json:"identity"`
	Team     TeamConfig     `json:"team"`
	Requests RequestsConfig `json:"requests"`
}

// IdentityConfig describes what the configured credentials mean.
type IdentityConfig struct {
	// CredentialIsShared declares that MIDPOINT_USERNAME is a technical/shared
	// account rather than one person's login. When it is set, self-scoped tools
	// ("my team", "my inbox") refuse in personal mode instead of answering for
	// the service account — an answer that is never what the caller meant. They
	// still work normally for requests that carry a mapped end user
	// (resource-server mode).
	CredentialIsShared bool `json:"credentialIsShared"`
}

// TeamConfig tunes how a caller's orgs are derived and which of them count as
// "my team". Deployments model org structure differently, and guessing wrong
// silently returns nobody.
type TeamConfig struct {
	// OrgSource selects where org links come from: parentOrgRef (default),
	// assignment, fallback, or both.
	OrgSource string `json:"orgSource"`
	// ManagerRelation is the relation local part that marks a user as manager of
	// an org (default "manager"). It classifies the caller's own links and is
	// used to find other users' manager links.
	ManagerRelation string `json:"managerRelation"`
	// MemberRelation is the relation local part that marks plain membership
	// (default "default"), used when searching an org for members.
	MemberRelation string `json:"memberRelation"`
	// OrgOIDs and OrgNames narrow which of the caller's orgs are considered. Empty
	// (the default) considers all of them; a user in several orgs can otherwise
	// pull in peers from an org that is not their team. Names are matched
	// case-insensitively against the org's resolved name.
	OrgOIDs  []string `json:"orgOids"`
	OrgNames []string `json:"orgNames"`
}

// RequestsConfig holds the self-service guardrails.
type RequestsConfig struct {
	// RequireRequestable (default true) refuses request_role for roles that are
	// not flagged requestable in midPoint's catalog. Without it, request_role is
	// an unrestricted grant path wearing a reassuring name: midPoint turns an
	// assignment-add into an approval case only where policy says so, and
	// executes it immediately everywhere else.
	RequireRequestable *bool `json:"requireRequestable"`
}

// requireRequestable resolves the tri-state pointer against its default.
func (r RequestsConfig) requireRequestable() bool {
	return r.RequireRequestable == nil || *r.RequireRequestable
}

// managerRelation / memberRelation resolve to their defaults when unset.
func (t TeamConfig) managerRelation() string {
	if t.ManagerRelation == "" {
		return relationManager
	}
	return t.ManagerRelation
}

func (t TeamConfig) memberRelation() string {
	if t.MemberRelation == "" {
		return relationDefault
	}
	return t.MemberRelation
}

func (t TeamConfig) orgSource() string {
	if t.OrgSource == "" {
		return OrgSourceParentOrgRef
	}
	return t.OrgSource
}

// selects reports whether the org link passes the configured org selector. With
// no selector configured every org qualifies.
func (t TeamConfig) selects(r orgRef) bool {
	if len(t.OrgOIDs) == 0 && len(t.OrgNames) == 0 {
		return true
	}
	for _, oid := range t.OrgOIDs {
		if oid == r.OID {
			return true
		}
	}
	name := r.TargetName.value()
	if name == "" {
		return false
	}
	for _, want := range t.OrgNames {
		if strings.EqualFold(want, name) {
			return true
		}
	}
	return false
}

// LoadFileConfig reads the settings file named by EnvConfigFile. A missing
// variable is not an error — the file is optional and the defaults are the
// documented behavior. A named-but-unreadable or invalid file IS an error:
// silently falling back to defaults would change who a manager can see.
func LoadFileConfig() (FileConfig, error) {
	path := strings.TrimSpace(os.Getenv(EnvConfigFile))
	if path == "" {
		return FileConfig{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, fmt.Errorf("reading %s: %w", EnvConfigFile, err)
	}
	clean, err := stripComments(data)
	if err != nil {
		return FileConfig{}, fmt.Errorf("parsing %s (%s): %w", EnvConfigFile, path, err)
	}
	var fc FileConfig
	dec := json.NewDecoder(bytes.NewReader(clean))
	// Unknown keys are an error: a typo'd setting that silently kept the default
	// is exactly the failure this file exists to prevent.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fc); err != nil {
		return FileConfig{}, fmt.Errorf("parsing %s (%s): %w", EnvConfigFile, path, err)
	}
	if err := fc.validate(); err != nil {
		return FileConfig{}, fmt.Errorf("%s (%s): %w", EnvConfigFile, path, err)
	}
	return fc, nil
}

// stripComments removes object keys whose name starts with "//". JSON has no
// comments, but a settings file this consequential needs to explain itself in
// place — so the convention is a sibling "//key" entry, dropped before the
// strict decode that catches typos.
func stripComments(data []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return json.Marshal(dropCommentKeys(v))
}

func dropCommentKeys(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if strings.HasPrefix(k, "//") {
				continue
			}
			out[k] = dropCommentKeys(val)
		}
		return out
	case []any:
		for i, val := range t {
			t[i] = dropCommentKeys(val)
		}
		return t
	default:
		return v
	}
}

// validate rejects a config that would misbehave rather than accepting it and
// returning wrong answers.
func (f FileConfig) validate() error {
	switch f.Team.orgSource() {
	case OrgSourceParentOrgRef, OrgSourceAssignment, OrgSourceFallback, OrgSourceBoth:
	default:
		return fmt.Errorf("team.orgSource %q must be one of %s, %s, %s, %s",
			f.Team.OrgSource, OrgSourceParentOrgRef, OrgSourceAssignment, OrgSourceFallback, OrgSourceBoth)
	}
	// Relations are interpolated into midPoint query filters, so they must be
	// plain local parts and nothing else.
	for field, rel := range map[string]string{
		"team.managerRelation": f.Team.ManagerRelation,
		"team.memberRelation":  f.Team.MemberRelation,
	} {
		if rel != "" && !validRelationLocal(rel) {
			return fmt.Errorf("%s %q is not a valid relation local part (letters, digits, '-' and '_')", field, rel)
		}
	}
	return nil
}

// validRelationLocal reports whether s is a bare relation local part such as
// "manager" or "org-owner" — a letter-led run of letters, digits, '-' and '_'.
// Prefixed QNames ("org:manager") are rejected: the query filter takes the local
// part, and accepting a prefix here would silently never match.
func validRelationLocal(s string) bool {
	if s == "" || !isAsciiLetter(rune(s[0])) {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case isAsciiLetter(rune(ch)), ch >= '0' && ch <= '9', ch == '-', ch == '_':
		default:
			return false
		}
	}
	return true
}
