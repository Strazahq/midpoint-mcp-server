package midpoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig writes a settings file and points EnvConfigFile at it.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "midpoint-mcp.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	t.Setenv(EnvConfigFile, path)
	return path
}

// No config file is the supported default, not an error.
func TestLoadFileConfigAbsent(t *testing.T) {
	t.Setenv(EnvConfigFile, "")
	fc, err := LoadFileConfig()
	if err != nil {
		t.Fatalf("LoadFileConfig with no file: %v", err)
	}
	if fc.Team.orgSource() != OrgSourceParentOrgRef || fc.Team.managerRelation() != "manager" ||
		fc.Team.memberRelation() != "default" || !fc.Requests.requireRequestable() ||
		fc.Identity.CredentialIsShared {
		t.Errorf("defaults = %+v, want parentOrgRef/manager/default/requireRequestable/not-shared", fc)
	}
}

func TestLoadFileConfigParses(t *testing.T) {
	writeConfig(t, `{
		"identity": {"credentialIsShared": true},
		"team": {
			"orgSource": "fallback",
			"managerRelation": "org-owner",
			"memberRelation": "member",
			"orgOids": ["o-1"],
			"orgNames": ["Dev Ops"]
		},
		"requests": {"requireRequestable": false}
	}`)

	fc, err := LoadFileConfig()
	if err != nil {
		t.Fatalf("LoadFileConfig: %v", err)
	}
	if !fc.Identity.CredentialIsShared {
		t.Error("identity.credentialIsShared not read")
	}
	if fc.Team.orgSource() != OrgSourceFallback || fc.Team.managerRelation() != "org-owner" ||
		fc.Team.memberRelation() != "member" {
		t.Errorf("team = %+v", fc.Team)
	}
	if fc.Requests.requireRequestable() {
		t.Error("requests.requireRequestable=false not read")
	}
}

// A named file that cannot be read or understood must fail loudly: silently
// using defaults would change which people a manager can see.
func TestLoadFileConfigRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"unknown orgSource", `{"team":{"orgSource":"guess"}}`, "orgSource"},
		{"prefixed relation", `{"team":{"managerRelation":"org:manager"}}`, "managerRelation"},
		{"injected relation", `{"team":{"memberRelation":"default\" or name = \"admin"}}`, "memberRelation"},
		{"unknown field", `{"team":{"orgSourc":"parentOrgRef"}}`, "MIDPOINT_MCP_CONFIG"},
		{"malformed json", `{"team":`, "MIDPOINT_MCP_CONFIG"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeConfig(t, tt.body)
			_, err := LoadFileConfig()
			if err == nil {
				t.Fatalf("LoadFileConfig(%s) = nil error, want refusal", tt.body)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

// The shipped example annotates every key with a "//"-prefixed sibling; those
// must load, while a genuine typo must not.
func TestLoadFileConfigAcceptsCommentKeys(t *testing.T) {
	writeConfig(t, `{
		"//": "top-level note",
		"team": {"//orgSource": "why this is set", "orgSource": "both"}
	}`)
	fc, err := LoadFileConfig()
	if err != nil {
		t.Fatalf("LoadFileConfig with comment keys: %v", err)
	}
	if fc.Team.orgSource() != OrgSourceBoth {
		t.Errorf("orgSource = %q, want %q", fc.Team.orgSource(), OrgSourceBoth)
	}
}

// The example file in the repo must actually be loadable.
func TestShippedExampleConfigLoads(t *testing.T) {
	t.Setenv(EnvConfigFile, filepath.Join("..", "..", "examples", "midpoint-mcp.config.json"))
	if _, err := LoadFileConfig(); err != nil {
		t.Fatalf("examples/midpoint-mcp.config.json does not load: %v", err)
	}
}

func TestLoadFileConfigMissingFile(t *testing.T) {
	t.Setenv(EnvConfigFile, filepath.Join(t.TempDir(), "nope.json"))
	if _, err := LoadFileConfig(); err == nil {
		t.Fatal("a config file that was named but does not exist must be an error")
	}
}

func TestTeamConfigSelects(t *testing.T) {
	devOps := orgRef{OID: "o-1", TargetName: polyString{Orig: "dev-ops"}}
	other := orgRef{OID: "o-2", TargetName: polyString{Orig: "all-staff"}}
	unnamed := orgRef{OID: "o-3"}

	// No selector: everything qualifies.
	empty := TeamConfig{}
	for _, r := range []orgRef{devOps, other, unnamed} {
		if !empty.selects(r) {
			t.Errorf("unconfigured selector rejected %+v", r)
		}
	}

	byOID := TeamConfig{OrgOIDs: []string{"o-1"}}
	if !byOID.selects(devOps) || byOID.selects(other) {
		t.Error("OID selector must keep o-1 and drop o-2")
	}

	// Names are matched case-insensitively; an org whose name midPoint did not
	// resolve cannot match a name selector.
	byName := TeamConfig{OrgNames: []string{"DEV-OPS"}}
	if !byName.selects(devOps) || byName.selects(other) || byName.selects(unnamed) {
		t.Error("name selector must match dev-ops case-insensitively and nothing else")
	}
}

func TestValidRelationLocal(t *testing.T) {
	for s, want := range map[string]bool{
		"manager":     true,
		"org-owner":   true,
		"approver_2":  true,
		"org:manager": false, // a prefixed QName would never match the local part
		"":            false,
		"2fast":       false,
		`a" or "1`:    false,
	} {
		if got := validRelationLocal(s); got != want {
			t.Errorf("validRelationLocal(%q) = %v, want %v", s, got, want)
		}
	}
}
