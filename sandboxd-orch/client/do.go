package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (c *Client) do(ctx context.Context, method, path string, body any) (map[string]any, error) {
	var out map[string]any
	if err := c.doInto(ctx, method, path, body, &out); err != nil {
		return nil, err
	}

	if out == nil {
		return map[string]any{"ok": true}, nil
	}

	return out, nil
}

func (c *Client) doInto(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}

		return fmt.Errorf("sandboxd %s %s failed: %s", method, path, msg)
	}

	if len(raw) == 0 {
		return nil
	}

	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("decode sandboxd response: %w", err)
	}

	return nil
}
