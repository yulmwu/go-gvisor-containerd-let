package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"sandboxd-o/orchestrator/types"
	"sandboxd-o/sandboxd/model"
)

type Client struct {
	httpClient *http.Client
	baseURL    string
}

type NodeStatus struct {
	OK        bool                `json:"ok"`
	Resources types.NodeResources `json:"resources"`
}

func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    strings.TrimRight(baseURL, "/"),
	}
}

func (c *Client) Healthz(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodGet, "/healthz", nil)
	return err
}

func (c *Client) NodeStatus(ctx context.Context) (NodeStatus, error) {
	out, err := c.do(ctx, http.MethodGet, "/v1/node/status", nil)
	if err != nil {
		return NodeStatus{}, err
	}

	b, err := json.Marshal(out)
	if err != nil {
		return NodeStatus{}, err
	}

	var st NodeStatus
	if err := json.Unmarshal(b, &st); err != nil {
		return NodeStatus{}, err
	}

	if !st.OK {
		return NodeStatus{}, fmt.Errorf("node status not ok")
	}

	return st, nil
}

func (c *Client) Reconcile(ctx context.Context) (map[string]any, error) {
	return c.do(ctx, http.MethodPost, "/v1/reconcile", nil)
}

func (c *Client) ListSandboxes(ctx context.Context, cursor string, limit int) (map[string]any, error) {
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}

	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}

	path := "/v1/sandboxes"
	if qs := q.Encode(); qs != "" {
		path += "?" + qs
	}

	return c.do(ctx, http.MethodGet, path, nil)
}

func (c *Client) GetSandbox(ctx context.Context, id string) (map[string]any, error) {
	return c.do(ctx, http.MethodGet, "/v1/sandboxes/"+url.PathEscape(id), nil)
}

func (c *Client) CreateSandbox(ctx context.Context, req model.CreateSandboxRequest) (map[string]any, error) {
	return c.do(ctx, http.MethodPost, "/v1/sandboxes", req)
}

func (c *Client) DeleteSandbox(ctx context.Context, id string) (map[string]any, error) {
	return c.do(ctx, http.MethodDelete, "/v1/sandboxes/"+url.PathEscape(id), nil)
}

func (c *Client) GetContainerLogs(ctx context.Context, sandboxID, containerName, cursor string, limit int) (map[string]any, error) {
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}

	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}

	path := "/v1/sandboxes/" + url.PathEscape(sandboxID) + "/containers/" + url.PathEscape(containerName) + "/logs"
	if qs := q.Encode(); qs != "" {
		path += "?" + qs
	}

	return c.do(ctx, http.MethodGet, path, nil)
}

func (c *Client) do(ctx context.Context, method, path string, body any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}

		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}

		return nil, fmt.Errorf("sandboxd %s %s failed: %s", method, path, msg)
	}

	if len(raw) == 0 {
		return map[string]any{"ok": true}, nil
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode sandboxd response: %w", err)
	}

	return out, nil
}
