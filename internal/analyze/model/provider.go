package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Approximate token prices used only to cap a single run. They are not a
// billing source of truth and self-hosted Ollama is free; ADR-004 D4 requires a
// deterministic budget guard, not exact provider pricing.
const (
	inputUSDPerMillionTokens  = 0.15
	outputUSDPerMillionTokens = 0.60
)

const (
	// maxProviderResponseBytes bounds how much of a provider response is read.
	maxProviderResponseBytes = 1 << 20
	// maxRetries is the documented retry budget for transient failures
	// (network errors, HTTP 429 and HTTP 5xx): at most two extra attempts.
	maxRetries = 2
)

// retryBackoff is the documented retry schedule (ADR-004 D4). It is a package
// variable so tests can shrink the delays; production always uses 500ms/1s.
var retryBackoff = []time.Duration{500 * time.Millisecond, time.Second}

// errRedirectsDisabled is returned by CheckRedirect: the client must never
// follow a redirect away from the configured provider endpoint.
var errRedirectsDisabled = errors.New("model: HTTP redirects are disabled")

// tokenUsage mirrors the provider-reported usage counters used for budgeting.
type tokenUsage struct {
	promptTokens     int
	completionTokens int
}

// chatResult is one successfully decoded provider answer.
type chatResult struct {
	content string
	usage   tokenUsage
}

// estimateCost converts provider usage into USD with the approximate constants
// above.
func estimateCost(usage tokenUsage) float64 {
	return float64(usage.promptTokens)*inputUSDPerMillionTokens/1_000_000 +
		float64(usage.completionTokens)*outputUSDPerMillionTokens/1_000_000
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type openAIRequestBody struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	Temperature    float64         `json:"temperature"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type ollamaRequestBody struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Options  struct {
		Temperature float64 `json:"temperature"`
	} `json:"options"`
}

// chatClient speaks one provider protocol. It is the only type in the package
// that performs network I/O.
type chatClient struct {
	provider           string
	baseURL            string
	modelName          string
	apiKey             string
	httpClient         *http.Client
	dropResponseFormat bool
}

// newChatClient builds a client with redirects disabled and a hard timeout. The
// caller also cancels the request context, so the run is bounded even if a
// provider ignores the client timeout.
func newChatClient(provider, baseURL, modelName, apiKey string, timeout time.Duration) *chatClient {
	return &chatClient{
		provider:  provider,
		baseURL:   baseURL,
		modelName: modelName,
		apiKey:    apiKey,
		httpClient: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return errRedirectsDisabled
			},
		},
	}
}

// endpoint resolves the protocol path from ADR-004 D1.
func (c *chatClient) endpoint() string {
	base := strings.TrimRight(c.baseURL, "/")
	if c.provider == providerOllama {
		return base + "/api/chat"
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/chat/completions"
	}
	return base + "/v1/chat/completions"
}

type retryDirective int

const (
	// retryNo means the attempt result is final.
	retryNo retryDirective = iota
	// retryBackoff means a transient failure that may be retried after backoff.
	retryBackoffRetry
	// retryImmediate means a protocol adjustment (dropping response_format)
	// should be retried at once and does not consume the retry budget.
	retryImmediate
)

// chat performs one logical provider call, applying the retry policy.
func (c *chatClient) chat(ctx context.Context, systemPrompt, userPrompt string) (*chatResult, error) {
	attempts := 0
	for {
		result, directive, err := c.attempt(ctx, systemPrompt, userPrompt)
		switch directive {
		case retryImmediate:
			continue
		case retryBackoffRetry:
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, timeoutError(ctxErr)
			}
			if attempts >= maxRetries {
				return nil, fmt.Errorf("%w（已重试 %d 次）", err, attempts)
			}
			delay := retryBackoff[len(retryBackoff)-1]
			if attempts < len(retryBackoff) {
				delay = retryBackoff[attempts]
			}
			attempts++
			if sleepErr := sleepContext(ctx, delay); sleepErr != nil {
				return nil, timeoutError(sleepErr)
			}
		default:
			return result, err
		}
	}
}

// attempt performs a single HTTP request and classifies the outcome.
func (c *chatClient) attempt(ctx context.Context, systemPrompt, userPrompt string) (*chatResult, retryDirective, error) {
	if err := ctx.Err(); err != nil {
		return nil, retryNo, timeoutError(err)
	}
	body, err := c.requestBody(systemPrompt, userPrompt)
	if err != nil {
		return nil, retryNo, fmt.Errorf("encode model request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, retryNo, fmt.Errorf("build model request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.provider == providerOpenAI && c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		if errors.Is(err, errRedirectsDisabled) {
			return nil, retryNo, err
		}
		return nil, retryBackoffRetry, fmt.Errorf("model request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxProviderResponseBytes))
	if readErr != nil {
		return nil, retryBackoffRetry, fmt.Errorf("read model response: %w", readErr)
	}

	status := resp.StatusCode
	if status >= 200 && status <= 299 {
		result, decodeErr := c.decodeSuccess(raw)
		if decodeErr != nil {
			return nil, retryNo, decodeErr
		}
		return result, retryNo, nil
	}
	// Some OpenAI-compatible servers reject response_format with HTTP 400 even
	// though the parameter is optional; retry once without it (ADR-004 D1).
	if status == http.StatusBadRequest && c.provider == providerOpenAI &&
		!c.dropResponseFormat && responseFormatUnsupported(raw) {
		c.dropResponseFormat = true
		return nil, retryImmediate, nil
	}
	if status == http.StatusTooManyRequests || status >= 500 {
		return nil, retryBackoffRetry, httpStatusError(status, raw)
	}
	return nil, retryNo, httpStatusError(status, raw)
}

// requestBody encodes the provider-specific request. Both protocols use
// temperature 0 so identical evidence yields (as far as the provider allows) a
// deterministic answer.
func (c *chatClient) requestBody(systemPrompt, userPrompt string) ([]byte, error) {
	messages := []chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}
	if c.provider == providerOllama {
		body := ollamaRequestBody{Model: c.modelName, Messages: messages, Stream: false}
		return json.Marshal(body)
	}
	body := openAIRequestBody{Model: c.modelName, Messages: messages, Temperature: 0}
	if !c.dropResponseFormat {
		body.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	return json.Marshal(body)
}

// decodeSuccess extracts the answer content and usage counters. A successful
// HTTP response with an empty or unparseable envelope is a provider contract
// failure (not something a correction round can fix).
func (c *chatClient) decodeSuccess(raw []byte) (*chatResult, error) {
	if c.provider == providerOllama {
		var envelope struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			PromptEvalCount int    `json:"prompt_eval_count"`
			EvalCount       int    `json:"eval_count"`
			Error           string `json:"error"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, fmt.Errorf("decode ollama response: %w", err)
		}
		if strings.TrimSpace(envelope.Error) != "" {
			return nil, fmt.Errorf("ollama returned an error: %s", truncateRunes(envelope.Error, 200))
		}
		return &chatResult{
			content: envelope.Message.Content,
			usage: tokenUsage{
				promptTokens:     envelope.PromptEvalCount,
				completionTokens: envelope.EvalCount,
			},
		}, nil
	}

	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode openai-compatible response: %w", err)
	}
	if len(envelope.Choices) == 0 {
		return nil, errors.New("model response contains no choices")
	}
	return &chatResult{
		content: envelope.Choices[0].Message.Content,
		usage: tokenUsage{
			promptTokens:     envelope.Usage.PromptTokens,
			completionTokens: envelope.Usage.CompletionTokens,
		},
	}, nil
}

// responseFormatUnsupported heuristically detects the documented HTTP 400 case
// where the server does not understand response_format/json_object.
func responseFormatUnsupported(raw []byte) bool {
	lower := strings.ToLower(string(raw))
	for _, fragment := range []string{"response_format", "json_object", "response format"} {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

// httpStatusError builds a bounded, neutralized error for a non-2xx response so
// provider-controlled text can never leak into a report.
func httpStatusError(status int, raw []byte) error {
	snippet := neutralizeText(strings.TrimSpace(string(raw)))
	snippet = strings.ReplaceAll(snippet, "\n", " ")
	snippet = truncateRunes(snippet, 200)
	if snippet == "" {
		return fmt.Errorf("model provider returned HTTP %d", status)
	}
	return fmt.Errorf("model provider returned HTTP %d: %s", status, snippet)
}

// sleepContext waits for d or the context deadline, whichever comes first.
func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// timeoutError tags context/HTTP timeouts so the degraded limitation explains
// the failure clearly.
func timeoutError(err error) error {
	return fmt.Errorf("model request timed out: %w", err)
}
