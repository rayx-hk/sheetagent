package model

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	eimodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// rateLimiter is a shared token that enforces minimum interval between API calls.
type rateLimiter struct {
	mu          sync.Mutex
	lastCall    time.Time
	minInterval time.Duration
	label       string
}

func (rl *rateLimiter) wait(ctx context.Context) error {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	since := time.Since(rl.lastCall)
	if since < rl.minInterval {
		wait := rl.minInterval - since
		slog.Debug("rate limit wait", "model", rl.label, "wait", wait.Round(time.Millisecond))
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	rl.lastCall = time.Now()
	return nil
}

// RateLimitedModel wraps a ToolCallingChatModel with request-level rate limiting.
// All models derived via WithTools share the same underlying rate limiter.
type RateLimitedModel struct {
	inner   eimodel.ToolCallingChatModel
	limiter *rateLimiter
}

func NewRateLimitedModel(inner eimodel.ToolCallingChatModel, minInterval time.Duration, label string) *RateLimitedModel {
	return &RateLimitedModel{
		inner: inner,
		limiter: &rateLimiter{
			minInterval: minInterval,
			label:       label,
		},
	}
}

func NewSharedRateLimiter(minInterval time.Duration, label string) *rateLimiter {
	return &rateLimiter{minInterval: minInterval, label: label}
}

func NewRateLimitedModelWithLimiter(inner eimodel.ToolCallingChatModel, rl *rateLimiter) *RateLimitedModel {
	return &RateLimitedModel{inner: inner, limiter: rl}
}

func isNonRetryableError(err error) bool {
	s := err.Error()
	if strings.Contains(s, "413") {
		return true
	}
	if strings.Contains(s, "streaming is strongly recommended") {
		return true
	}
	// E015 is a transient proxy internal error — must retry
	if strings.Contains(s, "400 Bad Request") && !strings.Contains(s, "E015") {
		return true
	}
	return false
}

// circuitBreaker tracks consecutive API failures to trigger fast cooldown.
var apiCircuit struct {
	mu              sync.Mutex
	consecutiveFail int
	lastFailTime    time.Time
}

const (
	circuitThreshold = 3  // consecutive failures before circuit opens
	circuitCooldown  = 20 * time.Second
)

func circuitOpen() bool {
	apiCircuit.mu.Lock()
	defer apiCircuit.mu.Unlock()
	if apiCircuit.consecutiveFail >= circuitThreshold {
		if time.Since(apiCircuit.lastFailTime) < circuitCooldown {
			return true
		}
		apiCircuit.consecutiveFail = 0
	}
	return false
}

func circuitRecordSuccess() {
	apiCircuit.mu.Lock()
	apiCircuit.consecutiveFail = 0
	apiCircuit.mu.Unlock()
}

func circuitRecordFailure() {
	apiCircuit.mu.Lock()
	apiCircuit.consecutiveFail++
	apiCircuit.lastFailTime = time.Now()
	apiCircuit.mu.Unlock()
}

func is403or502(err error) bool {
	s := err.Error()
	return strings.Contains(s, "403") || strings.Contains(s, "502") || strings.Contains(s, "503")
}

func doWithRetry[T any](ctx context.Context, op func() (T, error)) (T, error) {
	var lastErr error
	backoff := 3 * time.Second
	maxRetries := 5

	for i := 0; i < maxRetries; i++ {
		if circuitOpen() {
			remaining := circuitCooldown - time.Since(apiCircuit.lastFailTime)
			if remaining > 0 {
				slog.Warn("circuit breaker open, waiting", "cooldown", remaining.Round(time.Millisecond))
				select {
				case <-time.After(remaining):
				case <-ctx.Done():
					var zero T
					return zero, ctx.Err()
				}
			}
		}

		res, err := op()
		if err == nil {
			circuitRecordSuccess()
			return res, nil
		}

		lastErr = err

		if ctx.Err() != nil {
			var zero T
			return zero, ctx.Err()
		}

		if isNonRetryableError(err) {
			slog.Warn("non-retryable API error, skipping retry", "error", err)
			break
		}

		if is403or502(err) {
			circuitRecordFailure()
		}

		if i == maxRetries-1 {
			break
		}

		jitter := time.Duration(time.Now().UnixNano()%2000) * time.Millisecond
		actualBackoff := backoff + jitter

		slog.Warn("API call failed, retrying", "attempt", i+1, "backoff", actualBackoff, "error", err)

		select {
		case <-time.After(actualBackoff):
			backoff = min(backoff*2, 30*time.Second)
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		}
	}
	var zero T
	return zero, fmt.Errorf("failed after %d attempts, last error: %w", maxRetries, lastErr)
}

func (m *RateLimitedModel) Generate(ctx context.Context, input []*schema.Message, opts ...eimodel.Option) (*schema.Message, error) {
	return doWithRetry(ctx, func() (*schema.Message, error) {
		if err := m.limiter.wait(ctx); err != nil {
			return nil, err
		}
		return m.inner.Generate(ctx, input, opts...)
	})
}

func (m *RateLimitedModel) Stream(ctx context.Context, input []*schema.Message, opts ...eimodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return doWithRetry(ctx, func() (*schema.StreamReader[*schema.Message], error) {
		if err := m.limiter.wait(ctx); err != nil {
			return nil, err
		}
		return m.inner.Stream(ctx, input, opts...)
	})
}

func (m *RateLimitedModel) WithTools(tools []*schema.ToolInfo) (eimodel.ToolCallingChatModel, error) {
	newInner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &RateLimitedModel{
		inner:   newInner,
		limiter: m.limiter,
	}, nil
}
