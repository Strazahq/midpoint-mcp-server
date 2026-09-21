// Package midpoint is a thin client for the Evolveum midPoint REST API
// (/ws/rest/...). It reads credentials from the environment at runtime and
// never writes them to disk, logs, or tool output.
package midpoint

import (
	"fmt"
	"os"
	"strings"
)

// Environment variables consumed by ConfigFromEnv.
const (
	EnvURL          = "MIDPOINT_URL"
	EnvUsername     = "MIDPOINT_USERNAME"
	EnvPassword     = "MIDPOINT_PASSWORD"
	EnvInsecureTLS  = "MIDPOINT_INSECURE_TLS"
	EnvAllowWrites  = "MIDPOINT_MCP_ALLOW_WRITES"
	EnvOIDCIssuer   = "MIDPOINT_MCP_OIDC_ISSUER"
	EnvOIDCAudience = "MIDPOINT_MCP_OIDC_AUDIENCE"
	// EnvOIDCCorrelationClaim overrides which token claim is matched against a
	// midPoint user (default preferred_username). EnvOIDCCorrelationAttribute
	// overrides the midPoint attribute it is matched against (default name).
	EnvOIDCCorrelationClaim     = "MIDPOINT_MCP_OIDC_CORRELATION_CLAIM"
	EnvOIDCCorrelationAttribute = "MIDPOINT_MCP_OIDC_CORRELATION_ATTRIBUTE"
	// EnvOIDCClientCorrelationClaim names a token claim that only an OAuth
	// client's own token carries (client_id on Keycloak). EnvOIDCClientArchetypes
	// lists, comma-separated, the oids of the archetypes such a token may run as.
	// Both are set together; see Config.OIDCClientCorrelationClaim.
	EnvOIDCClientCorrelationClaim = "MIDPOINT_MCP_OIDC_CLIENT_CORRELATION_CLAIM"
	EnvOIDCClientArchetypes       = "MIDPOINT_MCP_OIDC_CLIENT_ARCHETYPES"
	// EnvAnonymousDiscovery opens the MCP handshake and tool listing to callers
	// with no bearer token. Off by default; see Config.AnonymousDiscovery.
	EnvAnonymousDiscovery = "MIDPOINT_MCP_ANONYMOUS_DISCOVERY"
)

// Config holds everything needed to reach a midPoint deployment.
//
// BaseURL is the deployment root (e.g. https://localhost:8443/midpoint); the
// client appends /ws/rest/... to it. Credentials are used for HTTP Basic auth,
// which is midPoint's native REST authentication.
type Config struct {
	BaseURL  string
	Username string
	Password string

	// InsecureTLS disables TLS certificate verification. It exists only so the
	// server can talk to midPoint dev instances that ship self-signed certs;
	// never enable it against a deployment you care about.
	InsecureTLS bool

	// AllowWrites gates the write tools. When false (the default), write tools
	// return a dry-run preview instead of calling midPoint. The client itself
	// never enforces this — it is a policy the tool layer applies.
	AllowWrites bool

	// OIDCIssuer and OIDCAudience enable resource-server mode (HTTP): incoming
	// bearer tokens are validated against the issuer's JWKS and the caller is
	// mapped to a midPoint user (see PLAN.md M4.5). Both must be set together.
	OIDCIssuer   string
	OIDCAudience string

	// OIDCCorrelationClaim and OIDCCorrelationAttribute customize how a validated
	// token maps to a midPoint user in resource-server mode. Empty values keep the
	// defaults (preferred_username → name); the subject → externalId match always
	// runs first regardless. Inert outside resource-server mode.
	OIDCCorrelationClaim     string
	OIDCCorrelationAttribute string

	// OIDCClientCorrelationClaim enables tokens that an OAuth client obtained for
	// itself (the client credentials grant), as an agent or a service does. When
	// a validated token carries this claim, the claim's value is matched on the
	// user's name, and every correlation query for that token, the
	// externalId attempt included, also requires one of OIDCClientArchetypes. A
	// client that someone named like a person therefore never runs as that
	// person. Empty (the default) leaves every token on the person path. A token
	// without the claim is a person's and is not restricted by archetype.
	OIDCClientCorrelationClaim string
	OIDCClientArchetypes       []string

	// AnonymousDiscovery lets a caller with no bearer token complete the MCP
	// handshake and list tools (initialize, notifications/initialized, ping,
	// tools/list). Every tools/call still requires a validated token, so this
	// exposes the static tool surface — names, descriptions, input schemas —
	// and no midPoint data. It exists for gateways and catalog builders that
	// inventory a server's capabilities before they hold a user token.
	//
	// Off by default, and inert outside resource-server mode: it is the one
	// place this server serves anything unauthenticated on a network-reachable
	// address, so turning it on is an explicit deployment decision.
	AnonymousDiscovery bool

	// File holds the optional non-secret settings file (MIDPOINT_MCP_CONFIG):
	// how this deployment models org structure and which self-service
	// guardrails apply. Zero value = documented defaults.
	File FileConfig
}

// ResourceServerMode reports whether OIDC bearer-token auth is configured.
func (c Config) ResourceServerMode() bool {
	return c.OIDCIssuer != "" && c.OIDCAudience != ""
}

// ConfigFromEnv builds a Config from the MIDPOINT_* environment variables,
// returning an error that names every missing required variable. The password
// value is never included in the error.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		BaseURL:                  strings.TrimSpace(os.Getenv(EnvURL)),
		Username:                 strings.TrimSpace(os.Getenv(EnvUsername)),
		Password:                 os.Getenv(EnvPassword),
		InsecureTLS:              strings.EqualFold(strings.TrimSpace(os.Getenv(EnvInsecureTLS)), "true"),
		AllowWrites:              strings.EqualFold(strings.TrimSpace(os.Getenv(EnvAllowWrites)), "true"),
		OIDCIssuer:               strings.TrimSpace(os.Getenv(EnvOIDCIssuer)),
		OIDCAudience:             strings.TrimSpace(os.Getenv(EnvOIDCAudience)),
		OIDCCorrelationClaim:     strings.TrimSpace(os.Getenv(EnvOIDCCorrelationClaim)),
		OIDCCorrelationAttribute: strings.TrimSpace(os.Getenv(EnvOIDCCorrelationAttribute)),
		AnonymousDiscovery:       strings.EqualFold(strings.TrimSpace(os.Getenv(EnvAnonymousDiscovery)), "true"),

		OIDCClientCorrelationClaim: strings.TrimSpace(os.Getenv(EnvOIDCClientCorrelationClaim)),
	}
	for _, oid := range strings.Split(os.Getenv(EnvOIDCClientArchetypes), ",") {
		if oid = strings.TrimSpace(oid); oid != "" {
			cfg.OIDCClientArchetypes = append(cfg.OIDCClientArchetypes, oid)
		}
	}

	var missing []string
	if cfg.BaseURL == "" {
		missing = append(missing, EnvURL)
	}
	if cfg.Username == "" {
		missing = append(missing, EnvUsername)
	}
	if cfg.Password == "" {
		missing = append(missing, EnvPassword)
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required environment variable(s): %s", strings.Join(missing, ", "))
	}

	// OIDC is all-or-nothing: a half-configured resource server would silently
	// fall back to unauthenticated behavior, which must never happen.
	if (cfg.OIDCIssuer == "") != (cfg.OIDCAudience == "") {
		return Config{}, fmt.Errorf("%s and %s must be set together", EnvOIDCIssuer, EnvOIDCAudience)
	}

	// The correlation attribute is interpolated into a query filter, so reject
	// anything that is not a plain midPoint path before it can reach the query.
	if cfg.OIDCCorrelationAttribute != "" && !ValidCorrelationAttribute(cfg.OIDCCorrelationAttribute) {
		return Config{}, fmt.Errorf("%s %q is not a valid midPoint attribute path (letters, digits, and '/' only)",
			EnvOIDCCorrelationAttribute, cfg.OIDCCorrelationAttribute)
	}

	// The archetype list is what keeps a client token away from people, so the
	// claim never goes live without it. The reverse is refused too: a list that
	// nothing reads would look like a guard and restrict nothing.
	if cfg.OIDCClientCorrelationClaim != "" && len(cfg.OIDCClientArchetypes) == 0 {
		return Config{}, fmt.Errorf("%s is set but %s is empty. A client's token is only matched to a midPoint user that holds one of the listed archetypes, which keeps a client named like a person from running as that person. Set %s to the comma-separated oids of the archetypes your agent or service users hold, or unset %s",
			EnvOIDCClientCorrelationClaim, EnvOIDCClientArchetypes, EnvOIDCClientArchetypes, EnvOIDCClientCorrelationClaim)
	}
	if cfg.OIDCClientCorrelationClaim == "" && len(cfg.OIDCClientArchetypes) > 0 {
		return Config{}, fmt.Errorf("%s is set but %s is not, so no token would ever be checked against the archetype list. Set %s to a claim that only a client's own token carries (client_id on Keycloak), or unset %s",
			EnvOIDCClientArchetypes, EnvOIDCClientCorrelationClaim, EnvOIDCClientCorrelationClaim, EnvOIDCClientArchetypes)
	}
	// The oids are interpolated into a query filter, like the correlation
	// attribute above.
	for _, oid := range cfg.OIDCClientArchetypes {
		if !validOID(oid) {
			return Config{}, fmt.Errorf("%s entry %q is not a midPoint oid (letters, digits and '-' only). Copy each archetype's oid from midPoint and separate the oids with commas",
				EnvOIDCClientArchetypes, oid)
		}
	}

	file, err := LoadFileConfig()
	if err != nil {
		return Config{}, err
	}
	cfg.File = file

	return cfg, nil
}

// ValidCorrelationAttribute reports whether s is a safe midPoint attribute path
// to interpolate into a query filter: a letter-led name of letters, digits, and
// '/' segments (e.g. name, emailAddress, employeeNumber, extension/badgeId). This
// keeps a misconfigured value from breaking out of the filter.
func ValidCorrelationAttribute(s string) bool {
	if s == "" {
		return false
	}
	if !isAsciiLetter(rune(s[0])) {
		return false
	}
	prev := byte('a')
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case isAsciiLetter(rune(ch)) || (ch >= '0' && ch <= '9'):
		case ch == '/':
			// No leading, trailing, or doubled separators.
			if prev == '/' || i == len(s)-1 {
				return false
			}
		default:
			return false
		}
		prev = ch
	}
	return true
}

// validOID reports whether s is safe to interpolate into a query filter as an
// oid: letters, digits and '-' only.
func validOID(s string) bool {
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if !isAsciiLetter(rune(ch)) && (ch < '0' || ch > '9') && ch != '-' {
			return false
		}
	}
	return s != ""
}

func isAsciiLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}
