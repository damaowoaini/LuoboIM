//go:build darwin || linux

package msgtransfer

import (
	"context"
	"errors"
	"fmt"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/kafka"
	"github.com/openimsdk/open-im-server/v3/pkg/common/prommetrics"
	"github.com/openimsdk/open-im-server/v3/pkg/common/startrpc"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// The old StartTransfer and its hard required-mode fence remain untouched.
// Only a private original server bootstrap selects this actual owned process.
func RunOwnedLegacyTransfer(prometheusPort int) error {
	lifetime, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
	defer cancel()
	return runOwnedLegacyTransfer(lifetime, prometheusPort)
}

type ownedLegacyRuntime struct {
	source                                 *ownedLegacySource
	identity                               *startrpc.OwnedServerIdentity
	cancel                                 context.CancelFunc
	consumers                              []*kafka.OwnedConsumerRun
	closeProducers                         func(context.Context) error
	closeRedis, closeMongo, closeDiscovery func(context.Context) error
	metrics                                *http.Server
	metricsListener                        net.Listener
	metricsDone                            chan error
	metricsTaken                           bool
	stopOnce                               sync.Once
	stopDone                               chan struct{}
	completed                              []string
	closeErr                               error
}

func runOwnedLegacyTransfer(parent context.Context, port int) error {
	return runOwnedLegacyTransferWithResources(parent, port, func(ctx context.Context, r *ownedLegacyRuntime, p int) error { return r.construct(ctx, p) })
}

// The unexported assembly seam changes no production constructor. It permits
// testing the real private bootstrap/start/stop/custody orchestration with only
// external storage/transport constructors substituted; it grants no Ready.
func runOwnedLegacyTransferWithResources(parent context.Context, port int, construct func(context.Context, *ownedLegacyRuntime, int) error) (result error) {
	if err := messagedelivery.CheckLegacyMsgTransferHistoryMode(); err != nil {
		return err
	}
	if parent == nil || parent.Done() == nil || port <= 0 || construct == nil {
		return errOwnedLegacyTransfer
	}
	identity, err := startrpc.LoadOwnedServerIdentity("msgtransfer-legacy")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	observer := &ownedmongo.Observer{}
	ctx, err = ownedmongo.Bind(ctx, observer)
	if err != nil {
		cancel()
		return err
	}
	if err = identity.BindSourceObserver(observer); err != nil {
		cancel()
		return err
	}
	runtime := &ownedLegacyRuntime{identity: identity, cancel: cancel, source: &ownedLegacySource{parent: ctx, cancel: cancel, observer: observer}, stopDone: make(chan struct{})}
	defer func() {
		stopCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		closed := runtime.Stop(stopCtx)
		result = errors.Join(result, closed)
		snapshot := observer.Snapshot()
		if snapshot.SourceOutcomeUnconfirmed || runtime.source.failed.Load() {
			note := ownedcontrol.ObserveMongoSnapshot(snapshot)
			note.IntakeUnconfirmed = runtime.source.failed.Load()
			note.SourceOutcomeUnconfirmed = true
			result = errors.Join(result, errOwnedLegacyTransfer, identity.RecordUnconfirmed(note))
		}
	}()
	if err = construct(ctx, runtime, port); err != nil {
		return err
	}
	for _, consumer := range runtime.consumers {
		if err = consumer.StartBounded(ctx); err != nil {
			return err
		}
	}
	if runtime.metrics != nil {
		runtime.metricsDone = make(chan error, 1)
		go func() { runtime.metricsDone <- runtime.metrics.Serve(runtime.metricsListener) }()
	}
	for _, consumer := range runtime.consumers {
		if err = consumer.Enable(); err != nil {
			return err
		}
	}
	if ctx.Err() != nil || observer.Snapshot().SourceOutcomeUnconfirmed {
		return errOwnedLegacyTransfer
	}
	steps := runtime.steps()
	if err = identity.Announce(steps); err != nil {
		return err
	}
	select {
	case <-parent.Done():
	case <-ctx.Done():
		if runtime.source.failed.Load() {
			result = errOwnedLegacyTransfer
		}
	case err = <-runtime.metricsDone:
		runtime.metricsTaken = true
		if !errors.Is(err, http.ErrServerClosed) {
			result = errOwnedLegacyTransfer
		}
	}
	stopCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	if err = runtime.Stop(stopCtx); err != nil {
		return errors.Join(result, err)
	}
	if runtime.source.Unconfirmed() {
		return errOwnedLegacyTransfer
	}
	for _, consumer := range runtime.consumers {
		if consumer.Failure() != nil {
			return errOwnedLegacyTransfer
		}
	}
	if result != nil {
		return result
	}
	return identity.Drain(runtime.completed)
}
func (r *ownedLegacyRuntime) steps() []string {
	steps := []string{"kafka-admission", "kafka-consumers", "kafka-producers"}
	if r.metrics != nil {
		steps = append(steps, "metrics")
	}
	return append(steps, "redis", "mongo", "discovery")
}
func (r *ownedLegacyRuntime) Stop(ctx context.Context) error {
	r.stopOnce.Do(func() { go func() { defer close(r.stopDone); r.closeErr = r.stopOnceActual(ctx) }() })
	select {
	case <-r.stopDone:
		return r.closeErr
	case <-ctx.Done():
		return errOwnedLegacyTransfer
	}
}
func (r *ownedLegacyRuntime) stopOnceActual(ctx context.Context) error {
	var issues error
	for _, consumer := range r.consumers {
		issues = errors.Join(issues, consumer.StopAdmission(ctx))
	}
	r.cancel()
	r.completed = append(r.completed, "kafka-admission")
	// Stop returns a close error only AFTER the actual Consume/claim loop has
	// joined and group.Close has returned. A deadline instead proves no such
	// join: retain the dependent pools, reject drain, and keep the original owner.
	consumersClosed := true
	for _, consumer := range r.consumers {
		err := consumer.Stop(ctx)
		issues = errors.Join(issues, err)
		if ctx.Err() != nil {
			return errors.Join(issues, ctx.Err())
		}
		consumersClosed = consumersClosed && err == nil
	}
	if consumersClosed {
		r.completed = append(r.completed, "kafka-consumers")
	}
	if r.closeProducers != nil {
		err := r.closeProducers(ctx)
		issues = errors.Join(issues, err)
		if err == nil {
			r.completed = append(r.completed, "kafka-producers")
		}
	}
	// Even an already-joined resource's Close error must not skip the remaining
	// independent closers. Missing/failed steps can never make a canonical drain.
	if r.metrics != nil {
		err := r.metrics.Shutdown(ctx)
		if err != nil {
			issues = errors.Join(issues, err, r.metrics.Close())
		}
		if r.metricsListener != nil {
			_ = r.metricsListener.Close()
		}
		if r.metricsDone != nil && !r.metricsTaken {
			select {
			case serveErr := <-r.metricsDone:
				if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
					issues = errors.Join(issues, errOwnedLegacyTransfer)
				}
			case <-ctx.Done():
				return errors.Join(issues, ctx.Err())
			}
		}
		if err == nil {
			r.completed = append(r.completed, "metrics")
		}
	}
	for _, item := range []struct {
		name  string
		close func(context.Context) error
	}{{"redis", r.closeRedis}, {"mongo", r.closeMongo}, {"discovery", r.closeDiscovery}} {
		if item.close != nil {
			err := item.close(ctx)
			issues = errors.Join(issues, err)
			if err == nil {
				r.completed = append(r.completed, item.name)
			}
		}
	}
	return issues
}
func (r *ownedLegacyRuntime) metricsStart(port int) error {
	if !config.Config.Prometheus.Enable {
		return nil
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(prommetrics.GetGrpcCusMetrics("Transfer")...)
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{Registry: registry}))
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	r.metricsListener = listener
	r.metrics = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return nil
}
