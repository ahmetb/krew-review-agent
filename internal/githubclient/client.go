// Package githubclient provides a minimal GitHub REST API client for the
// operations the agent needs (fetching a PR diff and posting an issue comment)
// together with the webhook event types used to parse incoming payloads.
//
// The client uses an injected *http.Client so transport behavior (timeouts,
// test doubles) is controllable by callers. It targets the public GitHub REST
// API by default; the base URL can be overridden for testing.
package githubclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// DefaultBaseURL is the public GitHub REST API root.
const DefaultBaseURL = "https://api.github.com"

// Client is an authenticated GitHub API client.
type Client struct {
	token   string
	baseURL string
	http    *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the API base URL.
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = u }
}

// WithHTTPClient overrides the underlying HTTP transport.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// New creates a GitHub API client authenticated with the given token.
func New(token string, opts ...Option) *Client {
	c := &Client{
		token:   token,
		baseURL: DefaultBaseURL,
		http:    http.DefaultClient,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// apiURL builds a GitHub REST API URL from path segments.
func (c *Client) apiURL(path string) (string, error) {
	base := strings.TrimRight(c.baseURL, "/")
	parsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid base url %q: %w", c.baseURL, err)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + strings.TrimLeft(path, "/")
	return parsed.String(), nil
}

// do performs an authenticated request, returning the response body bytes and
// status. A non-2xx status is reported as an error.
func (c *Client) do(ctx context.Context, method, path string, headers map[string]string, body []byte) (int, []byte, error) {
	u, err := c.apiURL(path)
	if err != nil {
		return 0, nil, err
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("building %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "krew-review-agent")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("github %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	data, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return resp.StatusCode, nil, fmt.Errorf("reading github response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, data, &APIError{
			StatusCode: resp.StatusCode,
			Path:       path,
			Method:     method,
			Body:       string(data),
		}
	}
	return resp.StatusCode, data, nil
}

// APIError is returned for non-2xx GitHub responses.
type APIError struct {
	StatusCode int
	Path       string
	Method     string
	Body       string
}

func (e *APIError) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > 200 {
		body = body[:200] + "..."
	}
	return fmt.Sprintf("github %s %s: status %d: %s", e.Method, e.Path, e.StatusCode, body)
}

// FetchPRDiff returns the raw diff of a pull request as a string.
//
// It calls GET /repos/{owner}/{repo}/pulls/{number} with the
// application/vnd.github.v3.diff media type.
func (c *Client) FetchPRDiff(ctx context.Context, owner, repo string, prNumber int) (string, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, prNumber)
	_, data, err := c.do(ctx, http.MethodGet, path, map[string]string{
		"Accept": "application/vnd.github.v3.diff",
	}, nil)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// PostComment creates an issue comment on a pull request.
//
// It calls POST /repos/{owner}/{repo}/issues/{number}/comments with the given
// Markdown body.
func (c *Client) PostComment(ctx context.Context, owner, repo string, prNumber int, body string) error {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, prNumber)
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return fmt.Errorf("encoding comment body: %w", err)
	}
	if _, _, err := c.do(ctx, http.MethodPost, path, nil, payload); err != nil {
		return err
	}
	return nil
}
