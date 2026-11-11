// Package connectd is a small HTTP client for the Datum Connect daemon
// (datum-cloud/connect, branch feat/connect-daemon, binary datum-connectd).
//
// It only uses the daemon's public /v1 API. Field names mirror
// connect-lib/daemon/src/model.rs and api.rs at commit 61bb05d.
package connectd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// DefaultURL is where datum-connectd listens unless DATUM_CONNECT_DAEMON_PORT
// says otherwise. It only ever binds loopback.
const DefaultURL = "http://127.0.0.1:47780"

type Client struct {
	base  string
	token string
	http  *http.Client
}

func New(base, token string) *Client {
	if base == "" {
		base = DefaultURL
	}
	// No client-wide timeout: each call gets one from its context, or a
	// default by kind (see do).
	return &Client{base: base, token: token, http: &http.Client{}}
}

// Default per-call limits when the caller's context has no deadline. Reads
// are quick. Changes can wait on the daemon's mutation lock and then do
// several control-plane round trips, so 5 s could expire while the daemon
// carries on and completes the change.
var (
	ReadTimeout  = 10 * time.Second
	WriteTimeout = 2 * time.Minute
)

// WithToken returns a copy of the client that authenticates with token.
func (c *Client) WithToken(token string) *Client {
	cp := *c
	cp.token = token
	return &cp
}

// UnreachableError means the daemon didn't answer at all, as opposed to
// answering with an error. The dashboard shows these differently.
type UnreachableError struct{ Cause error }

func (e *UnreachableError) Error() string { return "Connect daemon unreachable: " + e.Cause.Error() }
func (e *UnreachableError) Unwrap() error { return e.Cause }

// APIError is a non-2xx answer from the daemon.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("daemon: %s (HTTP %d)", e.Message, e.Status) }

func IsUnreachable(err error) bool {
	var u *UnreachableError
	return errors.As(err, &u)
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	if _, ok := ctx.Deadline(); !ok {
		limit := WriteTimeout
		if method == http.MethodGet {
			limit = ReadTimeout
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, limit)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return &UnreachableError{Cause: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Error
		if msg == "" {
			msg = e.Message
		}
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return &APIError{Status: resp.StatusCode, Message: msg}
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// Health is the daemon's only unauthenticated route.
func (c *Client) Health(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/v1/health", nil, nil, nil)
}

// StatusRaw returns /v1/status for a project exactly as the daemon sent it,
// so no field is lost on the way to the dashboard.
func (c *Client) StatusRaw(ctx context.Context, project string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := c.do(ctx, http.MethodGet, "/v1/status", url.Values{"project": {project}}, nil, &raw)
	return raw, err
}

func (c *Client) Status(ctx context.Context, project string) (*Status, error) {
	var s Status
	err := c.do(ctx, http.MethodGet, "/v1/status", url.Values{"project": {project}}, nil, &s)
	return &s, err
}

type MintRequest struct {
	Role       string   `json:"role"` // "viewer" or "operate"
	Scopes     []string `json:"scopes,omitempty"`
	TTLSeconds *uint64  `json:"ttl_seconds,omitempty"`
}

type MintedToken struct {
	TokenID         string   `json:"token_id"`
	Bearer          string   `json:"bearer"`
	Role            string   `json:"role"`
	Project         *string  `json:"project"`
	Scopes          []string `json:"scopes"`
	CreatedAtUnixMs int64    `json:"created_at_unix_ms"`
	ExpiresAtUnixMs *int64   `json:"expires_at_unix_ms"`
}

// MintToken needs a Setup token. Viewer and Operate tokens are always tied
// to one project.
func (c *Client) MintToken(ctx context.Context, project string, req MintRequest) (*MintedToken, error) {
	var t MintedToken
	err := c.do(ctx, http.MethodPost, "/v1/tokens", url.Values{"project": {project}}, req, &t)
	return &t, err
}

// ServiceRequest mirrors ServiceRequest in api.rs.
type ServiceRequest struct {
	Endpoint string   `json:"endpoint"`
	Protocol string   `json:"protocol,omitempty"`
	Public   bool     `json:"public"`
	Hostname *string  `json:"hostname,omitempty"`
	Allow    []string `json:"allow,omitempty"`
}

// CreateService needs a project-scoped Operate token. the Connect daemon allows
// one service per port and protocol; a second one is a 409 service_conflict.
func (c *Client) CreateService(ctx context.Context, project string, req ServiceRequest) (*Service, error) {
	var s Service
	err := c.do(ctx, http.MethodPost, "/v1/services", url.Values{"project": {project}}, req, &s)
	return &s, err
}

func (c *Client) DeleteService(ctx context.Context, project, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/services/"+url.PathEscape(id), url.Values{"project": {project}}, nil, nil)
}

func IsStatus(err error, code int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == code
}

// RevokeToken needs a Setup token.
func (c *Client) RevokeToken(ctx context.Context, project, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/tokens/"+url.PathEscape(id), url.Values{"project": {project}}, nil, nil)
}

// ResumeService re-activates a paused service (Operate token).
func (c *Client) ResumeService(ctx context.Context, project, id string) error {
	return c.do(ctx, http.MethodPost, "/v1/services/"+url.PathEscape(id)+"/resume", url.Values{"project": {project}}, nil, nil)
}
