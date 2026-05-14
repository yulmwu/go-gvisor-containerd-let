package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sandboxd-o/orchestrator/types"
)

type NodeStatus struct {
	OK        bool                `json:"ok"`
	Resources types.NodeResources `json:"resources"`
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
