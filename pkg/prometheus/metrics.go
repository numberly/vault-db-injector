package prometheus

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gitlab.numberly.in/team-infrastructure/kube-vault-db-injector/pkg/logger"
)

type ServMetrics interface {
	RunMetrics() error
}

type MetricsImpl struct {
	successChan chan<- bool
	Server      *http.Server
	log         logger.Logger
}

func NewService(successChan chan<- bool) *MetricsImpl {
	reg := prometheus.NewRegistry()
	Init(reg)
	return &MetricsImpl{
		successChan: successChan,
		Server: &http.Server{
			Addr:    "0.0.0.0:8080",
			Handler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
		},
		log: logger.GetLogger(),
	}
}

func (mi *MetricsImpl) RunMetrics() error {

	go func() {
		mi.log.Infof("Listening metrics on %s", mi.Server.Addr)
		if err := mi.Server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			mi.log.Errorf("Metrics HTTP server failed: %s", err)
		}
	}()

	mi.successChan <- true
	return nil
}
