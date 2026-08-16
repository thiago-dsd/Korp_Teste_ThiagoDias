package aiclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/thiagodias/korp-invoices/internal/platform/aiclient"
	"github.com/thiagodias/korp-invoices/internal/platform/resilience"
)

// fastRetry keeps the retry behaviour without the waiting.
func fastRetry() resilience.RetryPolicy {
	return resilience.RetryPolicy{Attempts: 2, BaseDelay: time.Microsecond, MaxDelay: time.Microsecond}
}

func newClient(t *testing.T, handler http.HandlerFunc) *aiclient.Foundry {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return aiclient.NewFoundry(aiclient.Config{
		Endpoint:   server.URL,
		APIKey:     "test-key",
		Deployment: "gpt-test",
	}, aiclient.WithRetryPolicy(fastRetry()))
}

func answer(content string) string {
	payload, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{
			{"message": map[string]string{"content": content}, "finish_reason": "stop"},
		},
	})
	return string(payload)
}

func TestCompleteJSONReturnsTheAnswerOfTheModel(t *testing.T) {
	var received map[string]any

	client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)

		if r.Header.Get("api-key") != "test-key" {
			t.Errorf("api-key = %q, want it sent", r.Header.Get("api-key"))
		}
		if r.URL.Path != "/openai/deployments/gpt-test/chat/completions" {
			t.Errorf("path = %q, want the deployment path", r.URL.Path)
		}
		if r.URL.Query().Get("api-version") == "" {
			t.Error("the request carries no api-version")
		}

		_, _ = w.Write([]byte(answer(`{"items":[]}`)))
	})

	result, err := client.CompleteJSON(context.Background(), aiclient.Prompt{
		System: "rules",
		User:   "two bolts",
	})
	if err != nil {
		t.Fatalf("CompleteJSON() returned error: %v", err)
	}
	if string(result) != `{"items":[]}` {
		t.Errorf("answer = %q, want the model content", result)
	}

	// The answer has to be JSON and the same input should give the same output.
	if format, ok := received["response_format"].(map[string]any); !ok || format["type"] != "json_object" {
		t.Errorf("response_format = %v, want json_object", received["response_format"])
	}
	if temperature, ok := received["temperature"].(float64); !ok || temperature != 0 {
		t.Errorf("temperature = %v, want 0", received["temperature"])
	}
}

func TestCompleteJSONRefusesATruncatedAnswer(t *testing.T) {
	client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		payload, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": `{"items":[{"code":"P-1"`}, "finish_reason": "length"},
			},
		})
		_, _ = w.Write(payload)
	})

	// Half a JSON document would parse into a half filled draft, so it is
	// refused instead.
	if _, err := client.CompleteJSON(context.Background(), aiclient.Prompt{User: "x"}); !errors.Is(err, aiclient.ErrUnavailable) {
		t.Errorf("CompleteJSON() error = %v, want ErrUnavailable", err)
	}
}

func TestCompleteJSONRetriesWhenTheDeploymentIsBusy(t *testing.T) {
	calls := 0
	client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(answer(`{"items":[]}`)))
	})

	if _, err := client.CompleteJSON(context.Background(), aiclient.Prompt{User: "x"}); err != nil {
		t.Fatalf("CompleteJSON() returned error: %v", err)
	}
	if calls != 2 {
		t.Errorf("the deployment was called %d times, want 2", calls)
	}
}

func TestCompleteJSONDoesNotRetryARejectedRequest(t *testing.T) {
	calls := 0
	client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
	})

	if _, err := client.CompleteJSON(context.Background(), aiclient.Prompt{User: "x"}); !errors.Is(err, aiclient.ErrUnavailable) {
		t.Errorf("CompleteJSON() error = %v, want ErrUnavailable", err)
	}
	if calls != 1 {
		t.Errorf("the deployment was called %d times, want 1", calls)
	}
}

func TestCompleteJSONReportsAnEmptyOrMalformedAnswer(t *testing.T) {
	tests := map[string]string{
		"no choices":      `{"choices":[]}`,
		"empty content":   answer("   "),
		"not json at all": `<html>error</html>`,
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			})

			if _, err := client.CompleteJSON(context.Background(), aiclient.Prompt{User: "x"}); !errors.Is(err, aiclient.ErrUnavailable) {
				t.Errorf("CompleteJSON() error = %v, want ErrUnavailable", err)
			}
		})
	}
}

func TestCompleteJSONWithoutConfigurationDoesNotCallAnything(t *testing.T) {
	client := aiclient.NewFoundry(aiclient.Config{})

	if _, err := client.CompleteJSON(context.Background(), aiclient.Prompt{User: "x"}); !errors.Is(err, aiclient.ErrNotConfigured) {
		t.Errorf("CompleteJSON() error = %v, want ErrNotConfigured", err)
	}
}

func TestConfigured(t *testing.T) {
	tests := map[string]struct {
		config aiclient.Config
		want   bool
	}{
		"complete":           {aiclient.Config{Endpoint: "https://x", APIKey: "k", Deployment: "d"}, true},
		"missing endpoint":   {aiclient.Config{APIKey: "k", Deployment: "d"}, false},
		"missing key":        {aiclient.Config{Endpoint: "https://x", Deployment: "d"}, false},
		"missing deployment": {aiclient.Config{Endpoint: "https://x", APIKey: "k"}, false},
		"empty":              {aiclient.Config{}, false},
	}

	for name, tc := range tests {
		if got := tc.config.Configured(); got != tc.want {
			t.Errorf("%s: Configured() = %v, want %v", name, got, tc.want)
		}
	}
}

func TestCompleteJSONReportsAnUnreachableDeployment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := server.URL
	server.Close()

	client := aiclient.NewFoundry(aiclient.Config{Endpoint: url, APIKey: "k", Deployment: "d"},
		aiclient.WithRetryPolicy(fastRetry()))

	if _, err := client.CompleteJSON(context.Background(), aiclient.Prompt{User: "x"}); !errors.Is(err, aiclient.ErrUnavailable) {
		t.Errorf("CompleteJSON() error = %v, want ErrUnavailable", err)
	}
}
