// SPDX-License-Identifier: Apache-2.0

package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	HeartbeatsSent = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "eightly_agent_heartbeats_sent_total",
		Help: "Total heartbeats sent to EOS server",
	})
	HeartbeatsFailed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "eightly_agent_heartbeats_failed_total",
		Help: "Total heartbeat failures",
	}, []string{"reason"})
	WSClientsConnected = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "eightly_agent_ws_clients_connected",
		Help: "Number of active WebSocket console sessions",
	})
	RegistrationAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "eightly_agent_registration_attempts_total",
		Help: "Total registration attempts",
	}, []string{"result"})
	DockerSocketReachable = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "eightly_agent_docker_socket_reachable",
		Help: "Whether the Docker socket is reachable (1=yes, 0=no)",
	})
)

func init() {
	prometheus.MustRegister(
		HeartbeatsSent,
		HeartbeatsFailed,
		WSClientsConnected,
		RegistrationAttempts,
		DockerSocketReachable,
	)

	// Pre-initialize CounterVec label combos so they appear in /metrics
	// output immediately (counters start at 0).
	HeartbeatsFailed.WithLabelValues("send_error")
	RegistrationAttempts.WithLabelValues("success")
	RegistrationAttempts.WithLabelValues("error")
	RegistrationAttempts.WithLabelValues("rejected")
}
