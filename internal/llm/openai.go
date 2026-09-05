package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// DefaultOpenAIModel is the default for this project: cheap and fast enough for
// two calls per turn, and the planner's job is classification, not reasoning.
const DefaultOpenAIModel = "gpt-4o-mini"

// OpenAI implements Provider over the Chat Completions API.
type OpenAI struct {
	Key     string
	Model   string
	BaseURL string // override for a proxy or a compatible gateway
	HTTP    *http.Client
}

func (o *OpenAI) Name() string { return "openai/" + o.Model }

func (o *OpenAI) endpoint() string {
	base := o.BaseURL
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	return strings.TrimSuffix(base, "/") + "/chat/completions"
}

type openAIReq struct {
	Model       string      `json:"model"`
	MaxTokens   int         `json:"max_tokens"`
	Temperature float64     `json:"temperature"`
	Messages    []openAIMsg `json:"messages"`
}

type openAIMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func (o *OpenAI) Complete(ctx context.Context, system, user string, maxTokens int) (string, error) {
	body, err := json.Marshal(openAIReq{
		Model:       o.Model,
		MaxTokens:   maxTokens,
		Temperature: 0,
		Messages: []openAIMsg{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+o.Key)

	resp, err := o.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out openAIResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("provider error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("empty completion (http %d)", resp.StatusCode)
	}
	text := strings.TrimSpace(out.Choices[0].Message.Content)
	if text == "" {
		return "", fmt.Errorf("empty completion (finish_reason %q)", out.Choices[0].FinishReason)
	}
	return text, nil
}

func openAIFromEnv() Provider {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return nil
	}
	model := os.Getenv("FINTERMINAL_MODEL")
	if model == "" {
		model = DefaultOpenAIModel
	}
	return &OpenAI{
		Key:     key,
		Model:   model,
		BaseURL: os.Getenv("OPENAI_BASE_URL"),
		HTTP:    &http.Client{Timeout: httpTimeout},
	}
}
