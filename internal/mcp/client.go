// Package mcp is a minimal stdio JSON-RPC client for razorpay-mcp-server.
// It is the primary executor; the local mirror is the fallback, and the status
// line always says which one answered.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

type Client struct {
	cmd      *exec.Cmd
	in       io.WriteCloser
	out      *bufio.Reader
	mu       sync.Mutex
	nextID   int
	ReadOnly bool
	Tools    []string
}

type rpcReq struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Spawn launches the server. readOnly maps to the server's --read-only flag:
// a read session physically cannot mutate, whatever the planner emits.
func Spawn(ctx context.Context, bin, keyID, keySecret string, readOnly bool, toolsets string) (*Client, error) {
	args := []string{"--key", keyID, "--secret", keySecret}
	if readOnly {
		args = append(args, "--read-only")
	}
	if toolsets != "" {
		args = append(args, "--toolsets", toolsets)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", bin, err)
	}
	c := &Client{cmd: cmd, in: in, out: bufio.NewReader(out), ReadOnly: readOnly}

	if _, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "finterminal", "version": "0.1.0"},
	}); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("handshake: %w", err)
	}
	_ = c.notify("notifications/initialized", map[string]any{})

	if raw, err := c.call(ctx, "tools/list", map[string]any{}); err == nil {
		var lr struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if json.Unmarshal(raw, &lr) == nil {
			for _, t := range lr.Tools {
				c.Tools = append(c.Tools, t.Name)
			}
		}
	}
	return c, nil
}

func (c *Client) Close() error {
	if c.in != nil {
		_ = c.in.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
	}
	return nil
}

// Has reports whether the server exposed a tool. Coverage differs between the
// remote and local servers, so this is checked rather than assumed.
func (c *Client) Has(tool string) bool {
	for _, t := range c.Tools {
		if t == tool {
			return true
		}
	}
	return false
}

// CallTool invokes one MCP tool and returns its raw text content.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	raw, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return "", err
	}
	var res struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	var text string
	for _, c := range res.Content {
		if c.Type == "text" {
			text += c.Text
		}
	}
	if res.IsError {
		return text, fmt.Errorf("tool %s failed: %s", name, text)
	}
	return text, nil
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	id := c.nextID
	if err := c.write(rpcReq{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return nil, err
	}
	type result struct {
		raw json.RawMessage
		err error
	}
	ch := make(chan result, 1)
	go func() {
		for {
			line, err := c.out.ReadBytes('\n')
			if err != nil {
				ch <- result{nil, err}
				return
			}
			var resp rpcResp
			if json.Unmarshal(line, &resp) != nil || resp.ID != id {
				continue // notification or unrelated frame
			}
			if resp.Error != nil {
				ch <- result{nil, fmt.Errorf("%s: %s", method, resp.Error.Message)}
				return
			}
			ch <- result{resp.Result, nil}
			return
		}
	}()
	select {
	case r := <-ch:
		return r.raw, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(20 * time.Second):
		return nil, fmt.Errorf("%s: timed out", method)
	}
}

func (c *Client) notify(method string, params any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *Client) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = c.in.Write(append(b, '\n'))
	return err
}
