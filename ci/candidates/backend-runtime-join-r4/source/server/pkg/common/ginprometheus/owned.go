package ginprometheus

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Middleware and collectors only: the caller owns the real metrics listener,
// Serve/Shutdown and join. No global collector mutation, hidden Run or push loop.
func NewOwnedPrometheus(subsystem string, custom []*Metric) (*Prometheus, http.Handler, error) {
	if subsystem == "" {
		return nil, nil, errors.New("owned metrics subsystem required")
	}
	p := &Prometheus{MetricsPath: defaultMetricPath, ReqCntURLLabelMappingFn: func(c *gin.Context) string { return c.FullPath() }}
	registry := prometheus.NewRegistry()
	// Match the default registry's original Go/process telemetry without sharing
	// its mutable registrations or silently starting a second HTTP server.
	if err := registry.Register(collectors.NewGoCollector()); err != nil {
		return nil, nil, err
	}
	if err := registry.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})); err != nil {
		return nil, nil, err
	}
	definitions := append(append([]*Metric{}, custom...), standardMetrics...)
	for _, source := range definitions {
		if source == nil {
			return nil, nil, errors.New("owned metrics definition required")
		}
		definition := *source
		definition.Args = append([]string{}, source.Args...)
		definition.MetricCollector = nil
		metric := NewMetric(&definition, subsystem)
		if metric == nil {
			return nil, nil, errors.New("owned metric type refused")
		}
		if err := registry.Register(metric); err != nil {
			return nil, nil, err
		}
		definition.MetricCollector = metric
		p.MetricsList = append(p.MetricsList, &definition)
		switch source {
		case reqCounter:
			p.reqCnt = metric.(*prometheus.CounterVec)
		case reqDuration:
			p.reqDur = metric.(*prometheus.HistogramVec)
		case resSize:
			p.resSz = metric.(prometheus.Summary)
		case reqSize:
			p.reqSz = metric.(prometheus.Summary)
		}
	}
	return p, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}), nil
}
