package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/Manoj-Paramsetti/LocoAI/internal/provider"
)

const okBody = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-4-8",
"content":[{"type":"thinking","thinking":"","signature":"s"},{"type":"text","text":"hel"},{"type":"text","text":"lo"}],
"stop_reason":"end_turn","usage":{"input_tokens":7,"output_tokens":3,
"iterations":[{"type":"message","input_tokens":1,"output_tokens":0},{"type":"fallback_message","input_tokens":7,"output_tokens":3,"model":"claude-opus-4-8"}]}}`

type captured struct {
	body  map[string]any
	beta  string
	calls int
}

func server(t *testing.T, status int, headers map[string]string, body string) (*Provider, *captured) {
	t.Helper()
	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.calls++
		got.beta = r.Header.Get("anthropic-beta")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got.body)
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return New(nil, option.WithBaseURL(srv.URL), option.WithAPIKey("test")), got
}

func TestCompleteWireFormat(t *testing.T) {
	p, got := server(t, 200, nil, okBody)
	resp, err := p.Complete(context.Background(), provider.Request{
		Model: "claude-opus-5-5", System: "sys", Prompt: "hi", MaxTokens: 100, Effort: "medium",
		JSONSchema: map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hello" || resp.StopReason != "end_turn" || resp.ServedModel != "claude-opus-4-8" ||
		!resp.FellBack || resp.InputTokens != 7 || resp.OutputTokens != 3 {
		t.Fatalf("resp = %+v", resp)
	}
	if got.beta != "server-side-fallback-2026-07-01" || got.body["fallbacks"] != "default" {
		t.Fatalf("fallback not sent: beta=%q fallbacks=%v", got.beta, got.body["fallbacks"])
	}
	cfg := got.body["output_config"].(map[string]any)
	format := cfg["format"].(map[string]any)
	if cfg["effort"] != "medium" || format["type"] != "json_schema" {
		t.Fatalf("output_config = %v", cfg)
	}
	for _, banned := range []string{"temperature", "top_p", "thinking", "tool_choice"} {
		if _, ok := got.body[banned]; ok {
			t.Errorf("%s must not be sent", banned)
		}
	}
}

func TestNoFallbackWhereUnsupportedOrDisabled(t *testing.T) {
	p, got := server(t, 200, nil, okBody)
	if _, err := p.Complete(context.Background(), provider.Request{Model: "claude-haiku-5-5", Prompt: "x", MaxTokens: 1}); err != nil {
		t.Fatal(err)
	}
	if _, ok := got.body["fallbacks"]; ok || got.beta != "" {
		t.Fatalf("haiku got fallbacks: %v %q", got.body["fallbacks"], got.beta)
	}
	p.fallbacks = false
	if _, err := p.Complete(context.Background(), provider.Request{Model: "claude-opus-5-5", Prompt: "x", MaxTokens: 1}); err != nil {
		t.Fatal(err)
	}
	if _, ok := got.body["fallbacks"]; ok {
		t.Fatal("disabled fallbacks still sent")
	}
}

func TestRefusalIsAResultNotAnError(t *testing.T) {
	p, _ := server(t, 200, nil, `{"id":"m","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],
"stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber"},"usage":{"input_tokens":1,"output_tokens":0}}`)
	resp, err := p.Complete(context.Background(), provider.Request{Model: "claude-opus-5-5", Prompt: "x", MaxTokens: 1})
	if err != nil || resp.StopReason != "refusal" || resp.Text != "" {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
}

func TestErrorClassification(t *testing.T) {
	body := `{"type":"error","error":{"type":"x","message":"m"}}`
	for _, tt := range []struct {
		status    int
		retryable bool
		fatal     bool
	}{
		{400, false, false}, {401, false, true}, {402, false, true}, {403, false, true}, {404, false, true},
		{408, true, false}, {429, true, false}, {500, true, false}, {529, true, false},
	} {
		p, got := server(t, tt.status, map[string]string{"Retry-After": "12"}, body)
		_, err := p.Complete(context.Background(), provider.Request{Model: "m", Prompt: "x", MaxTokens: 1})
		var retry *provider.RetryableError
		var fatal *provider.FatalError
		if errors.As(err, &retry) != tt.retryable || errors.As(err, &fatal) != tt.fatal {
			t.Errorf("%d classified wrong: %T %v", tt.status, err, err)
		}
		if got.calls != 1 {
			t.Errorf("%d: SDK retried %d times; the runner owns retries", tt.status, got.calls)
		}
		if tt.status == 429 && (!retry.RateLimit || retry.RetryAfter != 12*time.Second) {
			t.Errorf("429 details: %+v", retry)
		}
	}
}

func TestNetworkFailureIsRetryable(t *testing.T) {
	p := New(nil, option.WithBaseURL("http://127.0.0.1:1"), option.WithAPIKey("test"))
	_, err := p.Complete(context.Background(), provider.Request{Model: "m", Prompt: "x", MaxTokens: 1})
	var retry *provider.RetryableError
	if !errors.As(err, &retry) {
		t.Fatalf("err = %T %v", err, err)
	}
}

func TestMissingCredentialsAreFatal(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_CONFIG_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	_, err := New(nil).Complete(context.Background(), provider.Request{Model: "m", Prompt: "x", MaxTokens: 1})
	var fatal *provider.FatalError
	if !errors.As(err, &fatal) {
		t.Fatalf("err = %T %v", err, err)
	}
}
