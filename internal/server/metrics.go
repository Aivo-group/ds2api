package server

import (
	"fmt"
	"net/http"
	"strings"

	"ds2api/internal/account"
	"ds2api/internal/config"
)

func accountMetricsHandler(pool *account.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		stats := pool.Stats()
		var body strings.Builder
		body.WriteString("# HELP ds2api_accounts Current managed DeepSeek accounts by lifecycle state.\n")
		body.WriteString("# TYPE ds2api_accounts gauge\n")
		writeMetric(&body, `ds2api_accounts{state="total"}`, stats.Total)
		writeMetric(&body, `ds2api_accounts{state="healthy"}`, stats.Healthy)
		writeMetric(&body, `ds2api_accounts{state="available"}`, stats.Available)
		writeMetric(&body, `ds2api_accounts{state="cooldown"}`, stats.Cooldown)
		writeMetric(&body, `ds2api_accounts{state="quarantined"}`, stats.Quarantined)
		body.WriteString("# HELP ds2api_account_inflight Current requests holding managed account slots.\n")
		body.WriteString("# TYPE ds2api_account_inflight gauge\n")
		writeMetric(&body, "ds2api_account_inflight", stats.InUse)
		body.WriteString("# HELP ds2api_account_waiters Current requests waiting for an account slot.\n")
		body.WriteString("# TYPE ds2api_account_waiters gauge\n")
		writeMetric(&body, "ds2api_account_waiters", stats.Waiting)
		body.WriteString("# HELP ds2api_account_rate_limit_events_total Managed-account cooldown events.\n")
		body.WriteString("# TYPE ds2api_account_rate_limit_events_total counter\n")
		writeMetric(&body, "ds2api_account_rate_limit_events_total", stats.RateLimitEventsTotal)
		body.WriteString("# HELP ds2api_account_banned_removals_total Confirmed banned accounts removed from the pool.\n")
		body.WriteString("# TYPE ds2api_account_banned_removals_total counter\n")
		writeMetric(&body, "ds2api_account_banned_removals_total", stats.BannedRemovalsTotal)

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(body.String())); err != nil {
			config.Logger.Warn("[metrics] response write failed", "error", err)
		}
	}
}

func writeMetric(builder *strings.Builder, name string, value any) {
	_, _ = fmt.Fprintf(builder, "%s %v\n", name, value)
}
