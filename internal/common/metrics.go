package common

import (
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	activeCount atomic.Int64
	totalBytesIn atomic.Int64
	totalBytesOut atomic.Int64

	ActiveSessions = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "np_active_sessions",
		Help: "Total number of active sessions",
	})

	BytesTransferred = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "np_bytes_transferred_total",
		Help: "Total bytes transferred by direction and node",
	}, []string{"direction", "node_id"})

	RelayErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "np_relay_errors_total",
		Help: "Total number of relay errors",
	}, []string{"type"})

	NodeLatencyMs = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "np_node_latency_ms",
		Help: "Last measured latency to outbound node",
	}, []string{"node_name"})

	DNSRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "np_dns_requests_total",
		Help: "Total number of DNS requests",
	}, []string{"upstream", "status"})

	DNSLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "np_dns_latency_ms",
		Help:    "DNS resolution latency in milliseconds",
		Buckets: []float64{10, 50, 100, 250, 500, 1000, 2000},
	}, []string{"upstream"})
)

func IncActiveSessions() {
	activeCount.Add(1)
	ActiveSessions.Set(float64(activeCount.Load()))
}

func DecActiveSessions() {
	activeCount.Add(-1)
	ActiveSessions.Set(float64(activeCount.Load()))
}

func GetActiveSessions() int64 {
	return activeCount.Load()
}

func AddBytesIn(n int64) {
	totalBytesIn.Add(n)
}

func AddBytesOut(n int64) {
	totalBytesOut.Add(n)
}

func GetTotalStats() (in, out int64) {
	return totalBytesIn.Load(), totalBytesOut.Load()
}
