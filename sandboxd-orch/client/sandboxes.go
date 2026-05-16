package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"sandboxd-o/sandboxd-let/model"
)

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
