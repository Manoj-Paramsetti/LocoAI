// Package anthropic adapts the Claude Messages API to provider.Provider.
package anthropic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/anthropics/anthropic-sdk-go/v2/shared/constant"

	"github.com/Manoj-Paramsetti/LocoAI/internal/provider"
)

// Provider calls Claude.
type Provider struct {
	client    sdk.Client
	fallbacks bool
}

// New builds a provider. Credentials come from the environment. fallbacks
// nil means enabled for models that support them.
func New(fallbacks *bool, opts ...option.RequestOption) *Provider {
	// The runner owns retries; SDK retries would multiply with them.
	opts = append([]option.RequestOption{option.WithMaxRetries(0)}, opts...)
	return &Provider{client: sdk.NewClient(opts...), fallbacks: fallbacks == nil || *fallbacks}
}

// Name implements provider.Provider.
func (p *Provider) Name() string { return "anthropic" }

// supportsDefaultFallback lists models accepting `fallbacks: "default"`.
func supportsDefaultFallback(model string) bool {
	switch model {
	case "claude-fable-5-1", "claude-opus-5-5", "claude-opus-5", "claude-sonnet-5-5":
		return true
	}
	return false
}

// Complete implements provider.Provider.
func (p *Provider) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	params := sdk.BetaMessageNewParams{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Messages:  []sdk.BetaMessageParam{sdk.NewBetaUserMessage(sdk.NewBetaTextBlock(req.Prompt))},
	}
	if req.System != "" {
		params.System = []sdk.BetaTextBlockParam{{Text: req.System}}
	}
	if req.Effort != "" {
		params.OutputConfig.Effort = sdk.BetaOutputConfigEffort(req.Effort)
	}
	if req.JSONSchema != nil {
		params.OutputConfig.Format = sdk.BetaJSONOutputFormatParam{Schema: req.JSONSchema}
	}
	if p.fallbacks && supportsDefaultFallback(req.Model) {
		params.Betas = []sdk.AnthropicBeta{sdk.AnthropicBetaServerSideFallback2026_07_01}
		params.Fallbacks = sdk.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()}
	}

	msg, err := p.client.Beta.Messages.New(ctx, params)
	if err != nil {
		return provider.Response{}, classify(err)
	}

	var text strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	resp := provider.Response{
		Text:         text.String(),
		StopReason:   string(msg.StopReason),
		ServedModel:  string(msg.Model),
		InputTokens:  msg.Usage.InputTokens,
		OutputTokens: msg.Usage.OutputTokens,
	}
	for _, it := range msg.Usage.Iterations {
		if it.Type == "fallback_message" {
			resp.FellBack = true
		}
	}
	return resp, nil
}

// classify maps SDK errors onto the runner's retry policy.
func classify(err error) error {
	var apiErr *sdk.Error
	if !errors.As(err, &apiErr) {
		if isTransport(err) {
			return &provider.RetryableError{Err: err}
		}
		// The request never reached the API, for example missing credentials.
		// Every other case would fail the same way.
		return &provider.FatalError{Err: err}
	}
	status := apiErr.StatusCode
	switch {
	case status == http.StatusUnauthorized, status == http.StatusPaymentRequired,
		status == http.StatusForbidden, status == http.StatusNotFound:
		return &provider.FatalError{Err: err}
	case status == http.StatusRequestTimeout, status == http.StatusConflict,
		status == http.StatusTooManyRequests, status >= 500:
		return &provider.RetryableError{
			Err:        err,
			RetryAfter: retryAfter(apiErr),
			RateLimit:  status == http.StatusTooManyRequests,
		}
	}
	return fmt.Errorf("model request rejected: %w", err)
}

// isTransport reports network failures and per-attempt timeouts.
func isTransport(err error) bool {
	var urlErr *url.Error
	var netErr net.Error
	return errors.As(err, &urlErr) || errors.As(err, &netErr) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF)
}

func retryAfter(e *sdk.Error) time.Duration {
	if e.Response == nil {
		return 0
	}
	secs, err := strconv.ParseFloat(e.Response.Header.Get("Retry-After"), 64)
	if err != nil || secs < 0 {
		return 0
	}
	return time.Duration(secs * float64(time.Second))
}
