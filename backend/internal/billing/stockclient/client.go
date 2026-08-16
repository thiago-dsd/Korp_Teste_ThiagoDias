// Package stockclient talks to the stock service over HTTP.
package stockclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
	"github.com/thiagodias/korp-invoices/internal/platform/httpx"
	"github.com/thiagodias/korp-invoices/internal/platform/resilience"
)

// maxResponseBody bounds how much the client reads from the stock service.
const maxResponseBody = 4 << 20 // 4 MiB

// ErrStockUnavailable reports that the stock service could not be reached.
// The message is written for the operator using the screen.
var ErrStockUnavailable = apperr.Unavailable("stock_unavailable",
	"The stock service is unavailable. Please try again in a moment.")

// Product is the stock view of a product.
type Product struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	Description string    `json:"description"`
	Balance     int       `json:"balance"`
}

// Client calls the stock service with a timeout, bounded retries and a circuit
// breaker, so a failing dependency degrades into a clear error instead of
// hanging the caller.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
	retry   resilience.RetryPolicy
	breaker *resilience.Breaker
}

// Option customizes a client.
type Option func(*Client)

// WithHTTPClient replaces the underlying HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) { c.http = client }
}

// WithRetryPolicy replaces the retry policy.
func WithRetryPolicy(policy resilience.RetryPolicy) Option {
	return func(c *Client) { c.retry = policy }
}

// WithBreaker replaces the circuit breaker.
func WithBreaker(breaker *resilience.Breaker) Option {
	return func(c *Client) { c.breaker = breaker }
}

// New builds a client for the stock service at baseURL.
func New(baseURL, serviceToken string, options ...Option) *Client {
	client := &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		token:   serviceToken,
		http:    &http.Client{Timeout: 5 * time.Second},
		retry:   resilience.DefaultRetryPolicy(),
		breaker: resilience.NewBreaker(5, 10*time.Second),
	}
	for _, option := range options {
		option(client)
	}
	return client
}

// BreakerState reports the circuit state, used by the health endpoint.
func (c *Client) BreakerState() string { return c.breaker.State() }

type lookupRequest struct {
	ProductIDs []uuid.UUID `json:"product_ids"`
}

type productListResponse struct {
	Items []Product `json:"items"`
}

// Lookup resolves product ids into products. A rejection from the stock
// service (unknown product, invalid request) is returned as is and never
// retried; connection and server errors are retried and, once the attempts run
// out, reported as ErrStockUnavailable.
func (c *Client) Lookup(ctx context.Context, ids []uuid.UUID) ([]Product, error) {
	if len(ids) == 0 {
		return []Product{}, nil
	}

	payload, err := json.Marshal(lookupRequest{ProductIDs: ids})
	if err != nil {
		return nil, fmt.Errorf("encode lookup request: %w", err)
	}

	var products []Product
	err = c.breaker.Do(ctx, func(ctx context.Context) error {
		return resilience.Retry(ctx, c.retry, func(ctx context.Context) error {
			result, err := c.postLookup(ctx, payload)
			if err != nil {
				return err
			}
			products = result
			return nil
		})
	})
	if err != nil {
		return nil, translateFailure(err)
	}
	return products, nil
}

// ListAll reads the whole catalogue, which the assistant needs to match a
// sentence against real products.
func (c *Client) ListAll(ctx context.Context) ([]Product, error) {
	var products []Product

	err := c.breaker.Do(ctx, func(ctx context.Context) error {
		return resilience.Retry(ctx, c.retry, func(ctx context.Context) error {
			result, err := c.getCatalogue(ctx)
			if err != nil {
				return err
			}
			products = result
			return nil
		})
	})
	if err != nil {
		return nil, translateFailure(err)
	}
	return products, nil
}

func (c *Client) getCatalogue(ctx context.Context) ([]Product, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/internal/products", nil)
	if err != nil {
		return nil, resilience.Permanent(fmt.Errorf("build catalogue request: %w", err))
	}
	request.Header.Set(httpx.ServiceTokenHeader, c.token)
	if requestID := httpx.RequestIDFrom(ctx); requestID != "" {
		request.Header.Set(httpx.RequestIDHeader, requestID)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call stock service: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("read stock response: %w", err)
	}

	switch {
	case response.StatusCode == http.StatusOK:
		var parsed productListResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, resilience.Permanent(fmt.Errorf("decode stock response: %w", err))
		}
		return parsed.Items, nil

	case response.StatusCode >= 500 || response.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("stock service returned status %d", response.StatusCode)

	default:
		return nil, resilience.Permanent(rejection(response.StatusCode, body))
	}
}

func (c *Client) postLookup(ctx context.Context, payload []byte) ([]Product, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/internal/products/lookup", bytes.NewReader(payload))
	if err != nil {
		return nil, resilience.Permanent(fmt.Errorf("build lookup request: %w", err))
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(httpx.ServiceTokenHeader, c.token)
	if requestID := httpx.RequestIDFrom(ctx); requestID != "" {
		request.Header.Set(httpx.RequestIDHeader, requestID)
	}

	response, err := c.http.Do(request)
	if err != nil {
		// Connection failures are worth retrying.
		return nil, fmt.Errorf("call stock service: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBody))
		_ = response.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("read stock response: %w", err)
	}

	switch {
	case response.StatusCode == http.StatusOK:
		var parsed productListResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, resilience.Permanent(fmt.Errorf("decode stock response: %w", err))
		}
		return parsed.Items, nil

	case response.StatusCode >= 500 || response.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("stock service returned status %d", response.StatusCode)

	default:
		return nil, resilience.Permanent(rejection(response.StatusCode, body))
	}
}

// rejection turns an error payload from the stock service into a domain error,
// preserving its code and message when they are present.
func rejection(status int, body []byte) error {
	var payload struct {
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Error.Code == "" {
		return apperr.Internal("stock_rejected_request",
			"The stock service rejected the request.").
			WithCause(fmt.Errorf("status %d: %s", status, string(body)))
	}

	kind := apperr.KindInvalid
	switch status {
	case http.StatusNotFound:
		kind = apperr.KindNotFound
	case http.StatusConflict:
		kind = apperr.KindConflict
	case http.StatusUnauthorized, http.StatusForbidden:
		// A rejected token is a configuration problem, not something the user
		// can fix, so it is reported as an internal failure.
		return apperr.Internal("stock_authentication_failed",
			"The stock service rejected this service's credentials.").
			WithCause(fmt.Errorf("status %d", status))
	}

	err := apperr.New(kind, payload.Error.Code, payload.Error.Message)
	if len(payload.Error.Details) > 0 {
		err = err.WithDetails(payload.Error.Details)
	}
	return err
}

// translateFailure lets answers from the stock service through unchanged and
// reports everything else (connection failures, timeouts, open circuit) as
// ErrStockUnavailable, so the user gets an actionable message.
func translateFailure(err error) error {
	appErr := apperr.From(err)
	switch appErr.Kind {
	case apperr.KindInvalid, apperr.KindNotFound, apperr.KindConflict:
		return appErr
	case apperr.KindInternal:
		// Errors the client built from a rejection carry their own code;
		// anything else is an unclassified transport failure.
		if appErr.Code != "internal_error" {
			return appErr
		}
	}
	return ErrStockUnavailable.WithCause(err)
}
