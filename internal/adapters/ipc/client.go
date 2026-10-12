package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Diaszano/quota-guardian/internal/application"
	"github.com/Diaszano/quota-guardian/internal/domain"
)

type Client struct {
	http *http.Client
	base string
}

func NewClient(socketPath string) *Client {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socketPath)
	}}
	return &Client{http: &http.Client{Transport: transport, Timeout: 5 * time.Second}, base: "http://unix"}
}
func (c *Client) call(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		b, e := json.Marshal(input)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if e != nil {
		return e
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, e := c.http.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("daemon returned %s", resp.Status)
	}
	if output != nil {
		return json.NewDecoder(resp.Body).Decode(output)
	}
	return nil
}

type RunRequest struct {
	SubmissionKey string `json:"submission_key"`
	Provider      string `json:"provider"`
	ContextID     string `json:"context_id"`
	Prompt        string `json:"prompt"`
	AutoResume    bool   `json:"auto_resume,omitempty"`
}

func (c *Client) Run(ctx context.Context, req RunRequest) (domain.Session, error) {
	var out domain.Session
	e := c.call(ctx, http.MethodPost, "/v1/sessions", req, &out)
	return out, e
}
func (c *Client) Inspect(ctx context.Context, id string) (domain.Session, error) {
	var out domain.Session
	e := c.call(ctx, http.MethodGet, "/v1/sessions/"+url.PathEscape(id), nil, &out)
	return out, e
}
func (c *Client) List(ctx context.Context) ([]domain.Session, error) {
	var out []domain.Session
	e := c.call(ctx, http.MethodGet, "/v1/sessions", nil, &out)
	return out, e
}

type OutputResponse struct {
	Records []application.OutputEvent `json:"records"`
	Dropped uint64                    `json:"dropped"`
}

func (c *Client) Output(ctx context.Context, id string, after uint64) (OutputResponse, error) {
	var out OutputResponse
	e := c.call(ctx, http.MethodGet, "/v1/sessions/"+url.PathEscape(id)+"/output?after="+strconv.FormatUint(after, 10), nil, &out)
	return out, e
}
func (c *Client) Resume(ctx context.Context, id string, oneShot bool) error {
	return c.call(ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(id)+"/resume", map[string]bool{"one_shot": oneShot}, nil)
}
func (c *Client) Cancel(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(id)+"/cancel", map[string]bool{}, nil)
}
func (c *Client) SetConsent(ctx context.Context, id string, consent domain.Consent) error {
	item, err := c.Inspect(ctx, id)
	if err != nil {
		return err
	}
	return c.call(ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(id)+"/consent", map[string]any{"revision": item.Revision, "consent": consent}, nil)
}
func (c *Client) Health(ctx context.Context) error {
	return c.call(ctx, http.MethodGet, "/v1/health", nil, nil)
}

func socketPath(runtimeDir string) string {
	return filepath.Join(runtimeDir, "quota-guardian", "control.sock")
}
