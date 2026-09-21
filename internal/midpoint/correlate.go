package midpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// DefaultCorrelationAttribute is the midPoint attribute the correlation value is
// matched against when a deployment does not configure one.
const DefaultCorrelationAttribute = "name"

// CorrelateUser maps an OIDC identity to a midPoint user OID. It first tries the
// token subject against the user's externalId, then falls back to matching
// correlationValue against correlationAttribute (defaulting to the user's name).
// The externalId attempt is best-effort: if a deployment's schema has no
// externalId path the search may error, in which case correlation falls through
// to the attribute match.
//
// correlationValue is whichever token claim the deployment correlates on
// (preferred_username by default); correlationAttribute is the midPoint attribute
// that holds it (name by default). The attribute is interpolated into the query
// filter, so callers must pass a validated path — see ValidCorrelationAttribute.
//
// clientArchetypes is nil for a person's token. For a client's own token it holds
// the configured archetype oids, and every query, the externalId attempt
// included, then also requires the user to hold one of them. The oids are
// interpolated too and are validated when the configuration loads.
//
// This is how resource-server mode resolves "who is the human behind this
// token"; the resulting OID is the Switch-To-Principal target.
func (c *Client) CorrelateUser(ctx context.Context, subject, correlationValue, correlationAttribute string, clientArchetypes []string) (string, error) {
	attr := correlationAttribute
	if attr == "" {
		attr = DefaultCorrelationAttribute
	}
	guard := archetypeCondition(clientArchetypes)
	if subject != "" {
		if oid, err := c.uniqueUserByFilter(ctx, fmt.Sprintf("externalId = %s", quoteQueryString(subject))+guard); err == nil && oid != "" {
			return oid, nil
		}
	}
	if correlationValue != "" {
		oid, err := c.uniqueUserByFilter(ctx, fmt.Sprintf("%s = %s", attr, quoteQueryString(correlationValue))+guard)
		if err != nil {
			return "", err
		}
		if oid != "" {
			return oid, nil
		}
	}
	if guard != "" {
		return "", fmt.Errorf("no midPoint user that holds an archetype listed in %s matches subject=%q %s=%q. A client's token only runs as such a user. Give the client's midPoint user that %s and one of those archetypes, or add its archetype's oid to %s",
			EnvOIDCClientArchetypes, subject, attr, correlationValue, attr, EnvOIDCClientArchetypes)
	}
	return "", fmt.Errorf("no midPoint user matches subject=%q %s=%q", subject, attr, correlationValue)
}

// archetypeCondition returns the filter suffix that limits a query to users
// holding one of oids, or "" for no oids.
func archetypeCondition(oids []string) string {
	if len(oids) == 0 {
		return ""
	}
	matches := make([]string, len(oids))
	for i, oid := range oids {
		matches[i] = fmt.Sprintf("archetypeRef matches (oid = %s)", quoteQueryString(oid))
	}
	return " and (" + strings.Join(matches, " or ") + ")"
}

// uniqueUserByFilter returns the OID of the single user matching filter, "" if
// none, or an error if the match is ambiguous.
func (c *Client) uniqueUserByFilter(ctx context.Context, filter string) (string, error) {
	raws, err := c.searchRaw(ctx, collUsers, filter, 2)
	if err != nil {
		return "", err
	}
	if len(raws) == 0 {
		return "", nil
	}
	if len(raws) > 1 {
		return "", fmt.Errorf("ambiguous correlation: %d users match %q", len(raws), filter)
	}
	var u userJSON
	if err := json.Unmarshal(raws[0], &u); err != nil {
		return "", fmt.Errorf("decoding correlated user: %w", err)
	}
	return u.OID, nil
}
