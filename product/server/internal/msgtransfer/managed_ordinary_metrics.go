package msgtransfer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/openimsdk/open-im-server/v3/pkg/common/prommetrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func startManagedOrdinaryMetrics(parent context.Context, port int) (func(context.Context) error, <-chan error, error) {
	if parent == nil || port < 1 || port > 65535 {
		return nil, nil, errors.New("managed ordinary metrics binding invalid")
	}
	registry := prometheus.NewRegistry()
	if err := registry.Register(collectors.NewGoCollector()); err != nil {
		return nil, nil, errors.New("managed ordinary metrics registration failed")
	}
	for _, collector := range prommetrics.GetGrpcCusMetrics("Transfer") {
		if err := registry.Register(collector); err != nil {
			return nil, nil, errors.New("managed ordinary metrics registration failed")
		}
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{Registry: registry}))
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, nil, errors.New("managed ordinary metrics listener unavailable")
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 15 * time.Second, BaseContext: func(net.Listener) context.Context { return parent }}
	done := make(chan struct{})
	failure := make(chan error, 1)
	go func() {
		defer close(done)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			failure <- errors.New("managed ordinary metrics stopped unexpectedly")
		}
	}()
	stop := func(ctx context.Context) error {
		if err := server.Shutdown(ctx); err != nil {
			return ErrManagedOrdinaryShutdownPending
		}
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ErrManagedOrdinaryShutdownPending
		}
	}
	return stop, failure, nil
}
