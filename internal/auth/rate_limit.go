package auth

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ds2api/internal/config"
)

const defaultAccountCooldown = time.Minute

// MarkRateLimited prevents a managed account from being selected again until
// the upstream retry window expires. Direct caller tokens are never pooled.
func (a *RequestAuth) MarkRateLimited(delay time.Duration) time.Time {
	if a == nil || !a.UseConfigToken || a.AccountID == "" || a.resolver == nil || a.resolver.Pool == nil {
		return time.Time{}
	}
	if delay <= 0 {
		delay = defaultAccountCooldown
	}
	until := a.resolver.Pool.Cooldown(a.AccountID, delay)
	config.Logger.Warn("[rate_limited_account] cooldown applied", "account", a.AccountID, "retry_after", delay, "cooldown_until", until)
	return until
}

// RateLimitDelay accepts the standard Retry-After header and the timeout fields
// observed in JSON rate-limit responses. Unknown or invalid hints fail safe to
// a one-minute cooldown.
func RateLimitDelay(retryAfterHeader string, payload any) time.Duration {
	if delay := parseRetryAfterHeader(retryAfterHeader, time.Now()); delay > 0 {
		return delay
	}
	if delay := findRetryDelay(normalizeRateLimitPayload(payload)); delay > 0 {
		return delay
	}
	return defaultAccountCooldown
}

func parseRetryAfterHeader(raw string, now time.Time) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.ParseFloat(raw, 64); err == nil && seconds > 0 {
		return time.Duration(seconds * float64(time.Second))
	}
	if retryAt, err := http.ParseTime(raw); err == nil && retryAt.After(now) {
		return retryAt.Sub(now)
	}
	return 0
}

func normalizeRateLimitPayload(payload any) any {
	switch value := payload.(type) {
	case []byte:
		var decoded any
		if json.Unmarshal(value, &decoded) == nil {
			return decoded
		}
	case string:
		var decoded any
		if json.Unmarshal([]byte(value), &decoded) == nil {
			return decoded
		}
	}
	return payload
}

func findRetryDelay(value any) time.Duration {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
			switch normalized {
			case "retry_after_ms", "retryafterms", "timeout_ms", "wait_ms":
				if amount := durationNumber(child); amount > 0 {
					return time.Duration(amount * float64(time.Millisecond))
				}
			case "retry_after", "retryafter", "retry_after_seconds", "retryafterseconds", "timeout", "wait_seconds", "wait":
				if delay := durationValue(child); delay > 0 {
					return delay
				}
			}
		}
		for _, child := range typed {
			if delay := findRetryDelay(child); delay > 0 {
				return delay
			}
		}
	case []any:
		for _, child := range typed {
			if delay := findRetryDelay(child); delay > 0 {
				return delay
			}
		}
	}
	return 0
}

func durationValue(value any) time.Duration {
	if raw, ok := value.(string); ok {
		raw = strings.TrimSpace(raw)
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	if amount := durationNumber(value); amount > 0 {
		return time.Duration(amount * float64(time.Second))
	}
	return 0
}

func durationNumber(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	case string:
		parsed, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed
	default:
		return 0
	}
}
