// Package llm is the single, one-method seam every model provider sits behind.
// Swapping vendors means adding one file here; nothing else in the tree knows
// which model answered.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Provider is the whole contract: text in, text out, no tools.
type Provider interface {
	Name() string
	Complete(ctx context.Context, system, user string, maxTokens int) (string, error)
}

// FromEnv returns a configured provider, or nil when none is available. A nil
// provider is a supported mode: the rules planner and template narrator carry
// the product on their own.
func FromEnv() Provider {
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		model := os.Getenv("FINTERMINAL_MODEL")
		if model == "" {
			model = "claude-sonnet-5"
		}
		return &Anthropic{Key: key, Model: model, HTTP: &http.Client{Timeout: 30 * time.Second}}
	}
	return nil
}

// Anthropic implements Provider over the Messages API.
type Anthropic struct {
	Key   string
	Model string
	HTTP  *http.Client
}

func (a *Anthropic) Name() string { return "anthropic/" + a.Model }

type anthropicReq struct {
	Model       string    `json:"model"`
	MaxTokens   int       `json:"max_tokens"`
	Temperature float64   `json:"temperature"`
	System      string    `json:"system,omitempty"`
	Messages    []anthMsg `json:"messages"`
}

type anthMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (a *Anthropic) Complete(ctx context.Context, system, user string, maxTokens int) (string, error) {
	body, err := json.Marshal(anthropicReq{
		Model:       a.Model,
		MaxTokens:   maxTokens,
		Temperature: 0,
		System:      system,
		Messages:    []anthMsg{{Role: "user", Content: user}},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", a.Key)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := a.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out anthropicResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("provider error: %s", out.Error.Message)
	}
	var sb strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("empty completion (http %d)", resp.StatusCode)
	}
	return sb.String(), nil
}
