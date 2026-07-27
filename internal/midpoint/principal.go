package midpoint

import (
	"context"
	"fmt"
)

// SwitchToPrincipalHeader is midPoint's REST impersonation header: the service
// account executes the request as the user whose OID is given.
const SwitchToPrincipalHeader = "Switch-To-Principal"

// How the identity behind a request was established.
const (
	// ModePersonal: no per-request identity. midPoint sees the configured
	// credentials, whoever is driving the MCP client. When those credentials are
	// a shared service account, self-scoped tools answer for THAT account.
	ModePersonal = "personal"
	// ModeResourceServer: the request carries a validated end-user identity and
	// runs as that user via Switch-To-Principal.
	ModeResourceServer = "resource-server"
)

type principalKey struct{}

// WithPrincipal returns a context that makes the client execute requests as the
// given midPoint user OID (via the Switch-To-Principal header). This is how
// resource-server mode runs each request as the mapped end user while
// authenticating as the #proxy service account. An empty oid is ignored.
func WithPrincipal(ctx context.Context, oid string) context.Context {
	if oid == "" {
		return ctx
	}
	return context.WithValue(ctx, principalKey{}, oid)
}

// principalFromContext returns the impersonation target OID, or "" if none.
func principalFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(principalKey{}).(string); ok {
		return v
	}
	return ""
}

// Subject is whose data a self-scoped tool ("my team", "my inbox") actually
// answered for, and how that identity was established. It travels with every
// such answer because the two are not interchangeable: in personal mode the
// subject is the account the server authenticates as, which may be a service
// account and not the human operating the MCP client.
type Subject struct {
	OID  string `json:"oid"`
	Name string `json:"name"`
	Mode string `json:"mode" jsonschema:"how this identity was established: personal (the configured credentials) or resource-server (a validated per-request end user)"`
}

// Mode reports how the identity for this request was established. It keys off
// the request context rather than the configuration: a resource-server
// deployment still runs as the service account for any request that arrived
// without a mapped principal, and saying otherwise would overstate it.
func (c *Client) Mode(ctx context.Context) string {
	if principalFromContext(ctx) != "" {
		return ModeResourceServer
	}
	return ModePersonal
}

// Principal is the full answer to "who is this server acting as?": the identity
// midPoint executes as, how it was established, and the org links every
// team/self-scoped tool derives its results from. It exists so an empty "my
// team" or "my inbox" can be diagnosed in one call instead of guessed at.
type Principal struct {
	Subject
	FullName     string    `json:"fullName,omitempty"`
	EmailAddress string    `json:"emailAddress,omitempty"`
	Impersonated bool      `json:"impersonated" jsonschema:"true when this request runs as an end user via Switch-To-Principal"`
	Orgs         []OrgLink `json:"orgs" jsonschema:"the orgs this identity is linked to, with the relation that links them"`
}

// Whoami resolves the acting identity together with its org links.
func (c *Client) Whoami(ctx context.Context) (Principal, error) {
	self, err := c.selfUser(ctx)
	if err != nil {
		return Principal{}, err
	}
	s := self.summary()
	return Principal{
		Subject:      Subject{OID: s.OID, Name: s.Name, Mode: c.Mode(ctx)},
		FullName:     s.FullName,
		EmailAddress: s.EmailAddress,
		Impersonated: principalFromContext(ctx) != "",
		Orgs:         orgLinks(self.parentOrgs()),
	}, nil
}

// subject resolves who a self-scoped call answers for.
func (c *Client) subject(ctx context.Context) (Subject, error) {
	self, err := c.selfUser(ctx)
	if err != nil {
		return Subject{}, fmt.Errorf("resolving self: %w", err)
	}
	return Subject{OID: self.OID, Name: self.Name.value(), Mode: c.Mode(ctx)}, nil
}
