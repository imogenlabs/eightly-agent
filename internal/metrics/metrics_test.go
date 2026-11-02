// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"bufio"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// scrape reads /metrics the way Prometheus does and returns each series'
// value, keyed by the series as printed (name plus labels).
func scrape(t *testing.T) map[string]float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	promhttp.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	values := map[string]float64{}
	sc := bufio.NewScanner(rec.Body)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.LastIndexByte(line, ' ')
		v, err := strconv.ParseFloat(line[i+1:], 64)
		if err != nil {
			t.Fatalf("unparsable /metrics line %q", line)
		}
		values[line[:i]] = v
	}
	return values
}

// The agent's /metrics serves the default registry. Every series a dashboard
// or alert reads must be there from the first scrape, not only after the first
// event: an alert on rate(...) over a series that does not exist yet never
// fires.
func TestMetricsAreServedFromTheFirstScrape(t *testing.T) {
	got := scrape(t)
	for _, series := range []string{
		"eightly_agent_heartbeats_sent_total",
		`eightly_agent_heartbeats_failed_total{reason="send_error"}`,
		"eightly_agent_ws_clients_connected",
		`eightly_agent_registration_attempts_total{result="success"}`,
		`eightly_agent_registration_attempts_total{result="error"}`,
		`eightly_agent_registration_attempts_total{result="rejected"}`,
		"eightly_agent_docker_socket_reachable",
	} {
		if _, ok := got[series]; !ok {
			t.Errorf("/metrics is missing the series %s", series)
		}
	}
}

func TestMetricsCount(t *testing.T) {
	const (
		sent     = "eightly_agent_heartbeats_sent_total"
		rejected = `eightly_agent_registration_attempts_total{result="rejected"}`
		socket   = "eightly_agent_docker_socket_reachable"
	)
	before := scrape(t)
	HeartbeatsSent.Inc()
	RegistrationAttempts.WithLabelValues("rejected").Inc()
	DockerSocketReachable.Set(1)
	t.Cleanup(func() { DockerSocketReachable.Set(0) })
	after := scrape(t)

	if after[sent] != before[sent]+1 {
		t.Errorf("%s went %v -> %v, want +1", sent, before[sent], after[sent])
	}
	if after[rejected] != before[rejected]+1 {
		t.Errorf("%s went %v -> %v, want +1", rejected, before[rejected], after[rejected])
	}
	if after[socket] != 1 {
		t.Errorf("%s is %v, want 1", socket, after[socket])
	}
}
