package auth

import (
	"testing"
	"time"
)

func TestRateLimitDelayPrefersHeader(t *testing.T) {
	if got := RateLimitDelay("12", map[string]any{"retry_after": 99}); got != 12*time.Second {
		t.Fatalf("unexpected delay: %s", got)
	}
}

func TestRateLimitDelayReadsNestedMilliseconds(t *testing.T) {
	payload := []byte(`{"error":{"retry_after_ms":1500}}`)
	if got := RateLimitDelay("", payload); got != 1500*time.Millisecond {
		t.Fatalf("unexpected delay: %s", got)
	}
}

func TestRateLimitDelayFallsBackSafely(t *testing.T) {
	if got := RateLimitDelay("invalid", []byte(`{"error":"rate limited"}`)); got != time.Minute {
		t.Fatalf("unexpected fallback: %s", got)
	}
}
