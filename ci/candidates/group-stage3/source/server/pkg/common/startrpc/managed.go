package startrpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/OpenIMSDK/tools/network"
	grpcprometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	kdisc "github.com/openimsdk/open-im-server/v3/pkg/common/discoveryregister"
	"github.com/openimsdk/open-im-server/v3/pkg/common/prommetrics"
	"github.com/openimsdk/open-im-server/v3/pkg/common/rpcmiddleware"
)

// ManagedStop runs after RPC handlers have drained (or have been force-cancelled).
// Registrars must return it even on partial initialization failure when cleanup
// is still needed. It must honor its deadline and join every owned worker.
type ManagedStop func(context.Context) error
type ManagedRegister func(context.Context, discoveryregistry.SvcDiscoveryRegistry, *grpc.Server) (ManagedStop, error)

// StartManaged is deliberately separate from Start: only the group process opts
// into this lifecycle. No other RPC's existing startup contract changes.
func StartManaged(port int, name string, metricsPort int, register ManagedRegister, options ...grpc.ServerOption) error {
	if register == nil {
		return errors.New("managed registrar required")
	}
	lifetime, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer cancel()
	listener, err := net.Listen("tcp", net.JoinHostPort(network.GetListenIP(config.Config.Rpc.ListenIP), strconv.Itoa(port)))
	if err != nil {
		return err
	}
	defer listener.Close()
	discovery, err := kdisc.NewDiscoveryRegister(config.Config.Envs.Discovery)
	if err != nil {
		return err
	}
	defer discovery.Close()
	discovery.AddOption(rpcmiddleware.GrpcClient(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	registerIP, err := network.GetRpcRegisterIP(config.Config.Rpc.RegisterIP)
	if err != nil {
		return err
	}
	var registry *prometheus.Registry
	var metrics *grpcprometheus.ServerMetrics
	if config.Config.Prometheus.Enable {
		registry, metrics, _ = prommetrics.NewGrpcPromObj(prommetrics.GetGrpcCusMetrics(name))
		options = append(options, rpcmiddleware.GrpcServer(), grpc.StreamInterceptor(metrics.StreamServerInterceptor()), grpc.UnaryInterceptor(metrics.UnaryServerInterceptor()))
	} else {
		options = append(options, rpcmiddleware.GrpcServer())
	}
	server := grpc.NewServer(options...)
	stop, err := register(lifetime, discovery, server)
	if err != nil {
		cancel()
		server.Stop()
		return errors.Join(err, stopManaged(stop))
	}
	if stop == nil {
		cancel()
		server.Stop()
		return errors.New("managed cleanup required")
	}
	if err = discovery.Register(name, registerIP, port, grpc.WithTransportCredentials(insecure.NewCredentials())); err != nil {
		cancel()
		server.Stop()
		return errors.Join(err, stopManaged(stop))
	}
	// Both channels are buffered: a shutdown never leaves a goroutine blocked on
	// reporting an already-finished Serve/ListenAndServe call.
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	var metricsServer *http.Server
	metricsDone := make(chan error, 1)
	if metrics != nil && metricsPort != 0 {
		metrics.InitializeMetrics(server)
		metricsServer = &http.Server{Handler: promhttp.HandlerFor(registry, promhttp.HandlerOpts{}), Addr: fmt.Sprintf("0.0.0.0:%d", metricsPort), ReadHeaderTimeout: 5 * time.Second}
		go func() { metricsDone <- metricsServer.ListenAndServe() }()
	}
	select {
	case <-lifetime.Done():
	case err = <-serveDone:
		if errors.Is(err, grpc.ErrServerStopped) {
			err = nil
		}
	case err = <-metricsDone:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	cancel() // Stop recovery before draining; active commits flush inline as before.
	drainErr := drainManagedServer(server, 30*time.Second)
	if metricsServer != nil {
		ctx, done := context.WithTimeout(context.Background(), 2*time.Second)
		shutdownErr := metricsServer.Shutdown(ctx)
		done()
		if shutdownErr != nil {
			_ = metricsServer.Close()
		}
		err = errors.Join(err, shutdownErr)
	}
	if drainErr != nil {
		// grpc.Stop cancels handler contexts but, with the existing default,
		// does not prove every handler returned. Do not close Mongo/Redis pools
		// under a possibly live handler or falsely claim resource cleanup. The
		// executable receives this terminal error and exits; no restart occurs
		// inside this lifecycle. Recovery has already received cancellation.
		return errors.Join(err, drainErr)
	}
	return errors.Join(err, stopManaged(stop))
}

func stopManaged(stop ManagedStop) error {
	if stop == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return stop(ctx)
}

type managedGRPC interface {
	GracefulStop()
	Stop()
}

func drainManagedServer(server managedGRPC, budget time.Duration) error {
	done := make(chan struct{})
	go func() { server.GracefulStop(); close(done) }()
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		server.Stop() // Cancels RPC contexts; no unbounded deferred GracefulStop.
		return errors.New("managed RPC drain deadline reached")
	}
}
