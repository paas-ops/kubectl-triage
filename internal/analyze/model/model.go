// Package model adapts optional LLM providers (OpenAI-compatible, Ollama)
// behind the report contract.
//
// The package never talks to anything except the configured provider endpoint:
// evidence is treated as untrusted data, URLs inside evidence are never
// followed, and model output is validated against the report schema and the
// evidence citation closure before it reaches a report (ADR-004).
package model

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/paas-ops/kubectl-triage/internal/bundle"
	"github.com/paas-ops/kubectl-triage/internal/report"
)

// Provider identifiers accepted by Config.Provider (case-insensitive).
const (
	providerOllama = "ollama"
	providerOpenAI = "openai-compatible"
)

// Defaults applied by Enhance when the corresponding Config field is unset.
// ValidateConfig cannot write defaults back because it receives a copy; the CLI
// flag defaults normally provide the same values (ADR-004 D2).
const (
	defaultTimeout     = 60 * time.Second
	defaultBudgetUSD   = 1.0
	defaultMaxRounds   = 3
	defaultOllamaURL   = "http://127.0.0.1:11434"
	defaultOpenAIModel = "gpt-4o-mini"
)

// Config is the frozen model-provider configuration passed from the CLI.
//
// Provider values: "" / "none" (disabled), "openai-compatible", "ollama".
type Config struct {
	Provider  string        // provider kind; empty or "none" disables model mode
	URL       string        // provider base URL (the only network destination)
	ModelName string        // provider model identifier
	APIKey    string        // API key, required for openai-compatible
	Timeout   time.Duration // per-request timeout; 0 => 60s
	BudgetUSD float64       // per-run budget; 0 => 1.00 USD
	MaxRounds int           // max model rounds; 0 => 3
}

// ConfigError marks a user/config problem (CLI maps it to exit code 1) as
// opposed to a runtime failure that degrades to offline rules (exit code 3).
type ConfigError struct {
	Reason string
}

func (e *ConfigError) Error() string { return e.Reason }

// ValidateConfig reports whether Config can be used at all. Callers should run
// it before Analyze so that configuration mistakes fail fast instead of
// silently degrading a run.
//
// It returns *ConfigError for unknown providers, non-http(s) URLs, missing
// openai-compatible URLs/API keys and missing Ollama model names. The API key
// itself is never echoed in the error. An empty or "none" provider means model
// mode is disabled and validates successfully.
func ValidateConfig(cfg Config) error {
	provider, err := canonicalProvider(cfg.Provider)
	if err != nil {
		return err
	}
	if provider == "" {
		return nil
	}
	if _, err := resolveBaseURL(provider, cfg.URL); err != nil {
		return err
	}
	switch provider {
	case providerOpenAI:
		if strings.TrimSpace(cfg.APIKey) == "" {
			return &ConfigError{Reason: "openai-compatible 需要 API 密钥：请设置 KUBECTL_TRIAGE_MODEL_KEY 或 OPENAI_API_KEY"}
		}
	case providerOllama:
		if strings.TrimSpace(cfg.ModelName) == "" {
			return &ConfigError{Reason: "ollama 需要模型名：请用 --model-name 指定（例如 --model-name llama3.1）"}
		}
	}
	return nil
}

// Enhance asks the configured provider for a model-assisted report body,
// validates it against the report contract and citation closure, and merges it
// with the offline report. The offline report is always passed in as the
// fallback and must not be mutated on error.
//
// Model output that fails validation triggers a correction round: the validation
// problems are sent back to the provider. Exhausting Config.MaxRounds is a
// runtime failure, so callers degrade to the unchanged offline report.
func Enhance(b *bundle.Bundle, offline *report.Report, cfg Config) (enhanced *report.Report, err error) {
	// Never let untrusted provider/error content reach a report: caller errors
	// become limitation text in the degraded report.
	defer func() {
		if err != nil {
			enhanced = nil
			err = safeError(err, cfg.APIKey)
		}
	}()

	if b == nil {
		return nil, errors.New("model: nil bundle")
	}
	if offline == nil {
		return nil, errors.New("model: nil offline report")
	}
	provider, err := canonicalProvider(cfg.Provider)
	if err != nil {
		return nil, err
	}
	if provider == "" {
		return nil, &ConfigError{Reason: "模型 provider 为 none，不应调用模型增强"}
	}
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	budget := cfg.BudgetUSD
	if budget <= 0 {
		budget = defaultBudgetUSD
	}
	maxRounds := cfg.MaxRounds
	if maxRounds <= 0 {
		maxRounds = defaultMaxRounds
	}

	baseURL, err := resolveBaseURL(provider, cfg.URL)
	if err != nil {
		return nil, err
	}
	modelName := strings.TrimSpace(cfg.ModelName)
	if modelName == "" && provider == providerOpenAI {
		modelName = defaultOpenAIModel
	}

	// One context bounds the whole run, including retries and correction
	// rounds, so a slow provider can never exceed the single-run timeout.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client := newChatClient(provider, baseURL, modelName, cfg.APIKey, timeout)
	userPrompt := buildUserPrompt(b, offline)

	var roundProblems []string
	var spentUSD float64
	for round := 1; round <= maxRounds; round++ {
		prompt := userPrompt
		if round > 1 {
			prompt += correctionPrompt(roundProblems)
		}
		result, callErr := client.chat(ctx, systemPrompt, prompt)
		if callErr != nil {
			return nil, callErr
		}
		spentUSD += estimateCost(result.usage)
		if spentUSD > budget {
			return nil, fmt.Errorf("model budget exceeded: estimated $%.4f exceeds budget $%.2f", spentUSD, budget)
		}
		candidate, problems := buildModelReport(offline, result.content, provider, modelName, round, spentUSD)
		if len(problems) == 0 {
			return candidate, nil
		}
		roundProblems = problems
	}
	return nil, fmt.Errorf("模型输出在 %d 轮内未通过校验：%s", maxRounds, strings.Join(roundProblems, "；"))
}

// canonicalProvider normalizes a provider flag/mode and rejects unknown values.
// An empty result means model mode is disabled.
func canonicalProvider(raw string) (string, error) {
	switch value := strings.ToLower(strings.TrimSpace(raw)); value {
	case "", "none":
		return "", nil
	case providerOllama:
		return providerOllama, nil
	case providerOpenAI:
		return providerOpenAI, nil
	default:
		return "", &ConfigError{Reason: fmt.Sprintf("未知模型 provider %q（仅支持 ollama / openai-compatible / none）", strings.TrimSpace(raw))}
	}
}

// resolveBaseURL validates the configured base URL and fills the documented
// default for Ollama. openai-compatible requires an explicit URL (ADR-004 D2).
// Error messages never echo the URL because it may embed credentials.
func resolveBaseURL(provider, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		if provider == providerOllama {
			return defaultOllamaURL, nil
		}
		return "", &ConfigError{Reason: "openai-compatible 需要显式配置模型 URL（--model-url），例如 https://api.openai.com"}
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", &ConfigError{Reason: "模型 URL 必须是包含协议与主机名的 http(s) 绝对地址"}
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", &ConfigError{Reason: fmt.Sprintf("模型 URL 协议 %q 不受支持，只允许 http/https", parsed.Scheme)}
	}
	if parsed.User != nil {
		return "", &ConfigError{Reason: "模型 URL 不允许内嵌账号密码，请通过环境变量提供密钥"}
	}
	return strings.TrimRight(trimmed, "/"), nil
}

// safeError strips untrusted content (external URLs, commands, instruction-like
// text) and the API key from a runtime error before it becomes report text.
// ConfigError values are ours already and keep their concrete type.
func safeError(err error, apiKey string) error {
	if err == nil {
		return nil
	}
	var configErr *ConfigError
	if errors.As(err, &configErr) {
		return err
	}
	message := neutralizeText(err.Error())
	message = scrubSecret(message, apiKey)
	message = truncateRunes(message, maxErrorMessageRunes)
	if strings.TrimSpace(message) == "" {
		return errors.New("模型调用失败")
	}
	return errors.New(message)
}
