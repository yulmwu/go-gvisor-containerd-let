package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (c *Client) Healthz(ctx context.Context) (map[string]any, error) {
	return c.do(ctx, http.MethodGet, "/healthz", nil)
}

func (c *Client) ListSandboxes(ctx context.Context) (map[string]any, error) {
	return c.do(ctx, http.MethodGet, "/api/v1/sandboxes", nil)
}

func (c *Client) ListNodes(ctx context.Context) (map[string]any, error) {
	return c.do(ctx, http.MethodGet, "/api/v1/nodes", nil)
}

func (c *Client) GetNode(ctx context.Context, name string) (map[string]any, error) {
	return c.do(ctx, http.MethodGet, "/api/v1/nodes/"+url.PathEscape(strings.TrimSpace(name)), nil)
}

type RegisterNodeRequest struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

func (c *Client) RegisterNode(ctx context.Context, req RegisterNodeRequest) (map[string]any, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/nodes/register", req)
}

func (c *Client) DeleteNode(ctx context.Context, name string) (map[string]any, error) {
	return c.DeleteNodeWithForce(ctx, name, false)
}

func (c *Client) DeleteNodeWithForce(ctx context.Context, name string, force bool) (map[string]any, error) {
	path := "/api/v1/nodes/" + url.PathEscape(strings.TrimSpace(name))
	if force {
		path += "?force=true"
	}

	return c.do(ctx, http.MethodDelete, path, nil)
}

func (c *Client) GetSandbox(ctx context.Context, id string) (map[string]any, error) {
	return c.do(ctx, http.MethodGet, "/api/v1/sandboxes/"+url.PathEscape(strings.TrimSpace(id)), nil)
}

func (c *Client) CreateSandbox(ctx context.Context, payload map[string]any) (map[string]any, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/sandboxes", payload)
}

func (c *Client) DeleteSandbox(ctx context.Context, id string) (map[string]any, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/sandboxes/"+url.PathEscape(strings.TrimSpace(id)), nil)
}

func (c *Client) NodeListSandboxes(ctx context.Context, node string, limit int) (map[string]any, error) {
	q := ""
	if limit > 0 {
		q = "?limit=" + strconv.Itoa(limit)
	}
	return c.do(ctx, http.MethodGet, "/api/v1/nodes/"+url.PathEscape(node)+"/sandboxes"+q, nil)
}

func (c *Client) NodeGetSandbox(ctx context.Context, node, id string) (map[string]any, error) {
	return c.do(ctx, http.MethodGet, "/api/v1/nodes/"+url.PathEscape(node)+"/sandboxes/"+url.PathEscape(id), nil)
}

func (c *Client) NodeDeleteSandbox(ctx context.Context, node, id string) (map[string]any, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/nodes/"+url.PathEscape(node)+"/sandboxes/"+url.PathEscape(id), nil)
}

func (c *Client) NodeCreateSandbox(ctx context.Context, node string, payload map[string]any) (map[string]any, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/nodes/"+url.PathEscape(node)+"/sandboxes", payload)
}

func (c *Client) NodeContainerLogs(ctx context.Context, node, sandboxID, container string, limit int) (map[string]any, error) {
	if strings.TrimSpace(container) == "" {
		return nil, fmt.Errorf("container name is required")
	}

	q := ""
	if limit > 0 {
		q = "?limit=" + strconv.Itoa(limit)
	}

	p := "/api/v1/nodes/" + url.PathEscape(node) + "/sandboxes/" + url.PathEscape(sandboxID) + "/containers/" + url.PathEscape(container) + "/logs" + q

	return c.do(ctx, http.MethodGet, p, nil)
}
