package midpoint

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// restPrefix is joined to Config.BaseURL to form the REST base.
const restPrefix = "/ws/rest"

// maxResponseBytes caps how much of a midPoint response we read, so a
// misbehaving or unexpected endpoint can't exhaust memory.
const maxResponseBytes = 4 << 20 // 4 MiB

// Client talks to a midPoint deployment's REST API using HTTP Basic auth.
type Client struct {
	cfg         Config
	http        *http.Client
	requestForm *RequestForm // immutable after startup
}

// NewClient returns a Client for the given config. It configures a sane
// request timeout and, only when Config.InsecureTLS is set, a transport that
// skips certificate verification (for self-signed dev instances).
func NewClient(cfg Config) *Client {
	httpClient := &http.Client{Timeout: 30 * time.Second}
	if cfg.InsecureTLS {
		httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // opt-in dev-only escape hatch
		}
	}
	return &Client{cfg: cfg, http: httpClient}
}

// Identity is the authenticated principal reported by /ws/rest/self.
type Identity struct {
	OID          string
	Name         string
	FullName     string
	EmailAddress string
}

// Self returns the identity midPoint associates with the configured
// credentials by calling GET /ws/rest/self. It is the connectivity/identity
// check behind the ping tool.
func (c *Client) Self(ctx context.Context) (Identity, error) {
	if selfMemoFromContext(ctx) != nil {
		u, err := c.selfUser(ctx)
		if err != nil {
			return Identity{}, err
		}
		s := u.summary()
		return Identity{OID: s.OID, Name: s.Name, FullName: s.FullName, EmailAddress: s.EmailAddress}, nil
	}
	body, err := c.get(ctx, "/self", nil)
	if err != nil {
		return Identity{}, err
	}

	var resp selfResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Identity{}, fmt.Errorf("decoding /self response: %w", err)
	}
	return Identity{
		OID:          resp.User.OID,
		Name:         resp.User.Name.value(),
		FullName:     resp.User.FullName.value(),
		EmailAddress: resp.User.EmailAddress,
	}, nil
}

// get performs an authenticated GET against restPrefix+path.
func (c *Client) get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	return c.do(ctx, http.MethodGet, path, query, nil)
}

// post performs an authenticated POST with a JSON body.
func (c *Client) post(ctx context.Context, path string, query url.Values, body []byte) ([]byte, error) {
	return c.do(ctx, http.MethodPost, path, query, body)
}

// rawResponse is the subset of an HTTP response callers need, including headers
// (the create operation reads the new oid from Location).
type rawResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// do issues an authenticated request and returns the response body for any 2xx
// status. Errors carry the path and status but never the credentials.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body []byte) ([]byte, error) {
	resp, err := c.doFull(ctx, method, path, query, body)
	return resp.Body, err
}

// doFull is like do but exposes the full response (status and headers).
func (c *Client) doFull(ctx context.Context, method, path string, query url.Values, body []byte) (rawResponse, error) {
	u := strings.TrimRight(c.cfg.BaseURL, "/") + restPrefix + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return rawResponse{}, fmt.Errorf("building request for %s: %w", path, withoutURL(err))
	}
	req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Resource-server mode: execute as the mapped end user, not the service
	// account whose Basic credentials we present.
	if principal := principalFromContext(ctx); principal != "" {
		req.Header.Set(SwitchToPrincipalHeader, principal)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return rawResponse{}, &CodedError{Code: CodeMidpointUnavailable,
			Err: fmt.Errorf("calling midPoint %s %s: %w", method, path, withoutURL(err))}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return rawResponse{}, &CodedError{Code: CodeMidpointUnavailable,
			Err: fmt.Errorf("reading %s response: %w", path, err)}
	}

	out := rawResponse{StatusCode: resp.StatusCode, Header: resp.Header, Body: respBody}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		se := &StatusError{Path: path, StatusCode: resp.StatusCode, Status: resp.Status}
		if r, ok := decodeOpResult(respBody); ok {
			se.Message = answerText(r.message(), maxAnswerMessage)
			se.Reason = readableReason(r.reason())
		}
		return out, se
	}
	return out, nil
}

// withoutURL drops the request URL from a net/http error. A *url.Error's
// message repeats the full URL, midPoint's base address included, and these
// errors end up in tool results; the callers already name the REST path.
func withoutURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// selfResponse mirrors midPoint's JSON envelope for a focus object, which wraps
// the object under a key named for its type ("user" for the REST principal).
type selfResponse struct {
	User struct {
		OID          string     `json:"oid"`
		Name         polyString `json:"name"`
		FullName     polyString `json:"fullName"`
		EmailAddress string     `json:"emailAddress"`
	} `json:"user"`
}

// polyString decodes a midPoint PolyStringType, which serializes either as a
// bare JSON string or as an object like {"orig":"...","norm":"..."}.
type polyString struct {
	Orig string
}

func (p polyString) value() string { return p.Orig }

func (p *polyString) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		p.Orig = s
		return nil
	}
	var obj struct {
		Orig string `json:"orig"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	p.Orig = obj.Orig
	return nil
}
