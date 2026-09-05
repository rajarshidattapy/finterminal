package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFromEnvPrefersOpenAI(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	p := FromEnv()
	if p == nil {
		t.Fatal("expected a provider")
	}
	if got, want := p.Name(), "openai/"+DefaultOpenAIModel; got != want {
		t.Errorf("provider = %s, want %s", got, want)
	}
}

func TestFromEnvNoKeyIsSupported(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	if p := FromEnv(); p != nil {
		t.Errorf("expected no provider, got %s", p.Name())
	}
}

func TestOpenAICompleteSendsExpectedRequest(t *testing.T) {
	var got struct {
		Model       string  `json:"model"`
		Temperature float64 `json:"temperature"`
		MaxTokens   int     `json:"max_tokens"`
		Messages    []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	var auth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"  hello  "},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	o := &OpenAI{Key: "sk-test", Model: DefaultOpenAIModel, BaseURL: srv.URL, HTTP: srv.Client()}
	out, err := o.Complete(context.Background(), "SYS", "USER", 42)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "hello" {
		t.Errorf("content = %q, want %q", out, "hello")
	}
	if auth != "Bearer sk-test" {
		t.Errorf("authorization = %q", auth)
	}
	if got.Model != DefaultOpenAIModel || got.MaxTokens != 42 {
		t.Errorf("model/max_tokens = %s/%d", got.Model, got.MaxTokens)
	}
	// Temperature zero is not a nicety: the planner has to be reproducible.
	if got.Temperature != 0 {
		t.Errorf("temperature = %v, want 0", got.Temperature)
	}
	if len(got.Messages) != 2 || got.Messages[0].Role != "system" || got.Messages[1].Role != "user" {
		t.Errorf("messages = %+v", got.Messages)
	}
}

func TestOpenAISurfacesProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"message":"rate limit reached","type":"rate_limit_error"}}`)
	}))
	defer srv.Close()

	o := &OpenAI{Key: "sk-test", Model: DefaultOpenAIModel, BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := o.Complete(context.Background(), "s", "u", 10); err == nil {
		t.Fatal("expected an error")
	} else if got := err.Error(); got != "provider error: rate limit reached" {
		t.Errorf("err = %q", got)
	}
}
