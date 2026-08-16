// Package aiclient talks to the language model provider.
//
// The rest of the system depends on the Model interface, never on this
// implementation, so the provider can be replaced without touching any
// business rule.
package aiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
	"github.com/thiagodias/korp-invoices/internal/platform/resilience"
)

// Defaults for the Azure AI Foundry deployment.
const (
	defaultAPIVersion = "2024-10-21"
	defaultTimeout    = 20 * time.Second
	maxResponseBody   = 1 << 20
)

// ErrUnavailable reports that the assistant could not be used. The message is
// written for the person on the screen, who can always type the invoice by hand.
var ErrUnavailable = apperr.Unavailable("ai_unavailable",
	"The assistant is unavailable right now. You can add the products by hand.")

// ErrNotConfigured reports a deployment that was never set up.
var ErrNotConfigured = apperr.Unavailable("ai_not_configured",
	"The assistant is not configured on this environment.")

// Prompt is what the model is asked to do.
type Prompt struct {
	// System frames the task and the rules the answer must follow.
	System string
	// User is the text written by the person, treated as data and never as
	// instructions.
	User string
	// MaxTokens bounds the answer.
	MaxTokens int
}

// Model answers prompts with JSON.
type Model interface {
	// CompleteJSON returns the raw JSON answer of the model.
	CompleteJSON(ctx context.Context, prompt Prompt) ([]byte, error)
	// Name identifies the deployment answering, for logs and for the screen.
	Name() string
}

// Config describes an Azure AI Foundry deployment.
type Config struct {
	Endpoint   string
	APIKey     string
	Deployment string
	APIVersion string
}

// Configured reports whether there is enough to call the provider.
func (c Config) Configured() bool {
	return c.Endpoint != "" && c.APIKey != "" && c.Deployment != ""
}

// Foundry calls a chat deployment on Azure AI Foundry.
type Foundry struct {
	config Config
	http   *http.Client
	retry  resilience.RetryPolicy
}

// NewFoundry builds a client for the given deployment.
func NewFoundry(config Config, options ...Option) *Foundry {
	if config.APIVersion == "" {
		config.APIVersion = defaultAPIVersion
	}

	client := &Foundry{
		config: config,
		http:   &http.Client{Timeout: defaultTimeout},
		retry:  resilience.RetryPolicy{Attempts: 2, BaseDelay: 500 * time.Millisecond, MaxDelay: 2 * time.Second},
	}
	for _, option := range options {
		option(client)
	}
	return client
}

// Option customizes a client.
type Option func(*Foundry)

// WithHTTPClient replaces the underlying HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(f *Foundry) { f.http = client }
}

// WithRetryPolicy replaces the retry policy.
func WithRetryPolicy(policy resilience.RetryPolicy) Option {
	return func(f *Foundry) { f.retry = policy }
}

// Name returns the deployment being called.
func (f *Foundry) Name() string { return f.config.Deployment }

type chatRequest struct {
	Messages       []chatMessage  `json:"messages"`
	Temperature    float64        `json:"temperature"`
	MaxTokens      int            `json:"max_tokens"`
	ResponseFormat responseFormat `json:"response_format"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// CompleteJSON asks the deployment for a JSON answer.
//
// The model is asked for JSON explicitly and the temperature is zero, because
// the answer feeds a form: the same text should produce the same suggestion.
func (f *Foundry) CompleteJSON(ctx context.Context, prompt Prompt) ([]byte, error) {
	if !f.config.Configured() {
		return nil, ErrNotConfigured
	}

	maxTokens := prompt.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 800
	}

	payload, err := json.Marshal(chatRequest{
		Messages: []chatMessage{
			{Role: "system", Content: prompt.System},
			{Role: "user", Content: prompt.User},
		},
		Temperature:    0,
		MaxTokens:      maxTokens,
		ResponseFormat: responseFormat{Type: "json_object"},
	})
	if err != nil {
		return nil, fmt.Errorf("encode model request: %w", err)
	}

	var answer []byte
	err = resilience.Retry(ctx, f.retry, func(ctx context.Context) error {
		content, err := f.call(ctx, payload)
		if err != nil {
			return err
		}
		answer = content
		return nil
	})
	if err != nil {
		return nil, ErrUnavailable.WithCause(err)
	}
	return answer, nil
}

func (f *Foundry) call(ctx context.Context, payload []byte) ([]byte, error) {
	url := fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
		strings.TrimSuffix(f.config.Endpoint, "/"), f.config.Deployment, f.config.APIVersion)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, resilience.Permanent(fmt.Errorf("build model request: %w", err))
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("api-key", f.config.APIKey)

	response, err := f.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call model: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("read model answer: %w", err)
	}

	switch {
	case response.StatusCode == http.StatusOK:
	case response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500:
		// Worth another try: the deployment is busy or briefly unwell.
		return nil, fmt.Errorf("model answered status %d", response.StatusCode)
	default:
		// A rejected request stays rejected; the body may name the reason but
		// it is not shown to the caller.
		return nil, resilience.Permanent(fmt.Errorf("model rejected the request with status %d", response.StatusCode))
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, resilience.Permanent(fmt.Errorf("decode model answer: %w", err))
	}
	if len(parsed.Choices) == 0 {
		return nil, resilience.Permanent(fmt.Errorf("model answered without choices"))
	}
	if reason := parsed.Choices[0].FinishReason; reason != "" && reason != "stop" {
		// A truncated answer is not valid JSON and must not be parsed as one.
		return nil, resilience.Permanent(fmt.Errorf("model stopped early: %s", reason))
	}

	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
		return nil, resilience.Permanent(fmt.Errorf("model answered with an empty message"))
	}
	return []byte(content), nil
}
