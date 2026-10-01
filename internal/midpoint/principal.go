package midpoint

import (
	"context"
	"errors"
	"fmt"
	"sync"
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

// ErrNoCallerIdentity is returned by self-scoped operations when the deployment
// has declared its credentials shared (identity.credentialIsShared) and the
// request carries no mapped end user. Answering would describe the service
// account, which is never what "my team" or "my inbox" meant.
var ErrNoCallerIdentity error = &CodedError{Code: CodeSharedCredential, Err: errors.New(
	"this server authenticates to midPoint with a shared/technical account and this request carries no caller identity, " +
		"so a self-scoped answer would describe that account rather than you; " +
		"use resource-server mode to pass the end user's identity, or set identity.credentialIsShared=false in " +
		EnvConfigFile + " if the configured credentials really are one person's")}

// requireCallerIdentity refuses a self-scoped call that could only answer for a
// shared service account.
func (c *Client) requireCallerIdentity(ctx context.Context) error {
	if c.cfg.File.Identity.CredentialIsShared && c.Mode(ctx) == ModePersonal {
		return ErrNoCallerIdentity
	}
	return nil
}

// Whoami resolves the acting identity together with its org links. It is
// deliberately exempt from requireCallerIdentity: when self-scoped tools are
// refused, this is the call that explains why.
func (c *Client) Whoami(ctx context.Context) (Principal, error) {
	self, err := c.selfUser(ctx)
	if err != nil {
		return Principal{}, err
	}
	s := self.summary()
	team := c.teamConfig()
	return Principal{
		Subject:      Subject{OID: s.OID, Name: s.Name, Mode: c.Mode(ctx)},
		FullName:     s.FullName,
		EmailAddress: s.EmailAddress,
		Impersonated: principalFromContext(ctx) != "",
		Orgs:         orgLinks(callerOrgs(self, team), team),
	}, nil
}

// ActingIdentity is who midPoint executed one tool call as, in the shape the
// views' header needs. Unlike Subject it travels on results that are not
// self-scoped too, and it never refuses: it describes the shared account
// rather than pretending to be the caller, and says so in SharedCredential.
type ActingIdentity struct {
	OID              string    `json:"oid"`
	Name             string    `json:"name" jsonschema:"login name"`
	FullName         string    `json:"fullName,omitempty"`
	Mode             string    `json:"mode" jsonschema:"how this identity was established: personal (the configured credentials) or resource-server (a validated per-request end user)"`
	Impersonated     bool      `json:"impersonated" jsonschema:"true when this call ran as an end user via Switch-To-Principal"`
	SharedCredential bool      `json:"sharedCredential" jsonschema:"true when the deployment declared its credentials a shared account (identity.credentialIsShared) and this call ran as that account"`
	Orgs             []OrgLink `json:"orgs" jsonschema:"the orgs this identity is linked to, with the relation that links them"`
}

// Acting resolves the identity a call executes as. Like Whoami it is exempt
// from requireCallerIdentity.
func (c *Client) Acting(ctx context.Context) (ActingIdentity, error) {
	p, err := c.Whoami(ctx)
	if err != nil {
		return ActingIdentity{}, err
	}
	orgs := p.Orgs
	if orgs == nil {
		orgs = []OrgLink{}
	}
	return ActingIdentity{
		OID:              p.OID,
		Name:             p.Name,
		FullName:         p.FullName,
		Mode:             p.Mode,
		Impersonated:     p.Impersonated,
		SharedCredential: c.cfg.File.Identity.CredentialIsShared && p.Mode == ModePersonal,
		Orgs:             orgs,
	}, nil
}

type selfMemoKey struct{}

// selfMemo holds the caller's own user object for the rest of one call.
type selfMemo struct {
	mu   sync.Mutex
	user *userJSON
}

// WithSelfMemo returns a context in which the caller's own user object is read
// from midPoint at most once: every self lookup made with it (Self, Whoami,
// Acting and the self-scoped tools) shares the first successful read. It is
// scoped to one tool call, so a later call sees changes. Nothing else is
// memoized, so a read-back after a write still reaches midPoint.
func WithSelfMemo(ctx context.Context) context.Context {
	return context.WithValue(ctx, selfMemoKey{}, &selfMemo{})
}

func selfMemoFromContext(ctx context.Context) *selfMemo {
	m, _ := ctx.Value(selfMemoKey{}).(*selfMemo)
	return m
}

// subject resolves who a self-scoped call answers for, refusing first when the
// deployment says the credentials cannot stand in for a caller.
func (c *Client) subject(ctx context.Context) (Subject, error) {
	s, _, err := c.subjectUser(ctx)
	return s, err
}

// subjectUser is subject plus the caller's own user object, for self-scoped
// calls that need more of it than the name.
func (c *Client) subjectUser(ctx context.Context) (Subject, userJSON, error) {
	if err := c.requireCallerIdentity(ctx); err != nil {
		return Subject{}, userJSON{}, err
	}
	self, err := c.selfUser(ctx)
	if err != nil {
		return Subject{}, userJSON{}, fmt.Errorf("resolving self: %w", err)
	}
	return Subject{OID: self.OID, Name: self.Name.value(), Mode: c.Mode(ctx)}, self, nil
}
