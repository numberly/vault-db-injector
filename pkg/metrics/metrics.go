package metrics

import (
	"net/http"
	"time"

	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type ServMetrics interface {
	RunMetrics()
}

type MetricsImpl struct {
	Server *http.Server
	log    logger.Logger
}

func NewMetricsService() *MetricsImpl {
	reg := prometheus.NewRegistry()
	Init(reg)
	return &MetricsImpl{
		Server: &http.Server{
			Addr:              "0.0.0.0:8080",
			Handler:           promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
			ReadHeaderTimeout: 10 * time.Second,
		},
		log: logger.GetLogger(),
	}
}

func (mi *MetricsImpl) RunMetrics() {
	mi.log.Infof("Listening metrics on %s", mi.Server.Addr)
	if err := mi.Server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		mi.log.Errorf("Metrics HTTP server failed: %s", err)
	}
}
