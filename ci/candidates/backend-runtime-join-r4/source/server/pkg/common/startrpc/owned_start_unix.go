//go:build darwin || linux

package startrpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/OpenIMSDK/tools/network"
	grpcprometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	kdisc "github.com/openimsdk/open-im-server/v3/pkg/common/discoveryregister"
	"github.com/openimsdk/open-im-server/v3/pkg/common/prommetrics"
	"github.com/openimsdk/open-im-server/v3/pkg/common/rpcmiddleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func StartOwned(port int, name string, metricsPort int, role string, register func(discoveryregistry.SvcDiscoveryRegistry, *grpc.Server) error, options ...grpc.ServerOption) error {
	if register == nil {
		return errOwnedRPC
	}
	return StartOwnedManaged(port, name, metricsPort, role, func(_ context.Context, client discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) (ManagedStop, error) {
		return nil, register(client, server)
	}, options...)
}
func StartOwnedManaged(port int, name string, metricsPort int, role string, register ManagedRegister, options ...grpc.ServerOption) error {
	input, err := loadOwnedRPCInput(role)
	if err != nil {
		return err
	}
	lifetime, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer cancel()
	return startOwnedRPC(lifetime, port, name, metricsPort, input, register, kdisc.NewOwnedDiscoveryRegister, options...)
}

// The private factory parameter is used only to test the actual lifecycle at
// its discovery boundary. Production always supplies the owned real factory.
func startOwnedRPC(lifetime context.Context, port int, name string, metricsPort int, input ownedRPCInput, register ManagedRegister, newRegistry func(context.Context, string) (kdisc.OwnedRegistry, error), options ...grpc.ServerOption) (resultErr error) {
	if lifetime == nil || lifetime.Done() == nil || register == nil || newRegistry == nil {
		return errOwnedRPC
	}
	scope := &ownedRPCScope{role: input.boot.Role, databaseSHA256: input.boot.DatabaseSHA256, workers: newOwnedRPCWork(), resources: make(map[string]ownedResource), closeDone: make(chan struct{})}
	defer scope.workers.cancel()
	stopInitializationCancel := context.AfterFunc(lifetime, scope.workers.cancel)
	defer stopInitializationCancel()
	listener, err := net.Listen("tcp", net.JoinHostPort(network.GetListenIP(config.Config.Rpc.ListenIP), strconv.Itoa(port)))
	if err != nil {
		return err
	}
	defer listener.Close()
	registryCtx, cancelRegistry := context.WithCancel(context.Background())
	defer cancelRegistry()
	discovery, err := newRegistry(registryCtx, config.Config.Envs.Discovery)
	if err != nil {
		return err
	}
	discovery.AddOption(rpcmiddleware.GrpcClient(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	registerIP, err := network.GetRpcRegisterIP(config.Config.Rpc.RegisterIP)
	if err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return errors.Join(err, discovery.CloseOwned(ctx))
	}
	var metricsRegistry *prometheus.Registry
	var metrics *grpcprometheus.ServerMetrics
	if config.Config.Prometheus.Enable {
		metricsRegistry, metrics, _ = prommetrics.NewGrpcPromObj(prommetrics.GetGrpcCusMetrics(name))
		options = append(options, rpcmiddleware.GrpcServer(), grpc.StreamInterceptor(metrics.StreamServerInterceptor()), grpc.UnaryInterceptor(metrics.UnaryServerInterceptor()))
	} else {
		options = append(options, rpcmiddleware.GrpcServer())
	}
	server := grpc.NewServer(options...)
	ownedRPCScopes.Store(server, scope)
	defer ownedRPCScopes.Delete(server)
	var metricsServer *http.Server
	var metricsListener net.Listener
	var serveDone, metricsDone chan error
	serveTaken, metricsTaken := false, false
	ready := false
	var stopOnce sync.Once
	var stopErr error
	var completed []string
	stop := func() error {
		stopOnce.Do(func() {
			// No caller marks a timeout as success. An unjoined handler retains every
			// shared business pool until executable exit; no canonical drain is made.
			if ready {
				if err := drainManagedServer(server, 30*time.Second); err != nil {
					stopErr = err
					return
				}
			} else {
				server.Stop()
			}
			completed = append(completed, "rpc-handlers")
			joinCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if serveDone != nil && !serveTaken {
				select {
				case err := <-serveDone:
					if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
						stopErr = err
						return
					}
				case <-joinCtx.Done():
					stopErr = errOwnedRPC
					return
				}
			}
			if serveDone != nil {
				completed = append(completed, "rpc-serve")
			}
			if metricsServer != nil {
				if err := metricsServer.Shutdown(joinCtx); err != nil {
					_ = metricsServer.Close()
					stopErr = err
					return
				}
				if !metricsTaken {
					select {
					case err := <-metricsDone:
						if err != nil && !errors.Is(err, http.ErrServerClosed) {
							stopErr = err
							return
						}
					case <-joinCtx.Done():
						stopErr = errOwnedRPC
						return
					}
				}
				completed = append(completed, "metrics")
			}
			if err := scope.workers.stop(joinCtx); err != nil {
				stopErr = err
				return
			}
			completed = append(completed, "business-workers")
			names, err := scope.closeResources(joinCtx)
			if err != nil {
				stopErr = err
				return
			}
			completed = append(completed, names...)
			if err := discovery.CloseOwned(joinCtx); err != nil {
				stopErr = err
				return
			}
			completed = append(completed, "discovery")
		})
		return stopErr
	}
	defer func() { resultErr = errors.Join(resultErr, stop()) }()
	managedStop, err := register(scope.workers.ctx, discovery, server)
	if managedStop != nil {
		if trackErr := OwnRPCResource(server, "group-runtime", managedStop); trackErr != nil {
			return errors.Join(err, trackErr)
		}
	}
	if err != nil {
		return err
	}
	if len(server.GetServiceInfo()) == 0 || scope.seal(config.Config.Mongo.Database) != nil {
		return errOwnedRPC
	}
	runtimeConfigSHA, environmentSHA, err := OwnedRPCConfigurationHashes()
	if err != nil || environmentSHA != input.environmentSHA {
		return errOwnedRPC
	}
	policySHA, err := input.policySHA(runtimeConfigSHA)
	if err != nil {
		return err
	}
	if err = discovery.Register(name, registerIP, port, grpc.WithTransportCredentials(insecure.NewCredentials())); err != nil {
		return err
	}
	if metrics != nil && metricsPort != 0 {
		metrics.InitializeMetrics(server)
		metricsListener, err = net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", metricsPort))
		if err != nil {
			return err
		}
		defer metricsListener.Close()
		metricsServer = &http.Server{Handler: promhttp.HandlerFor(metricsRegistry, promhttp.HandlerOpts{}), ReadHeaderTimeout: 5 * time.Second}
	}
	if !stopInitializationCancel() || lifetime.Err() != nil {
		return errOwnedRPC
	}
	serveDone = make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	if metricsServer != nil {
		metricsDone = make(chan error, 1)
		go func() { metricsDone <- metricsServer.Serve(metricsListener) }()
	}
	ready = true
	steps := []string{"rpc-handlers", "rpc-serve"}
	if metricsServer != nil {
		steps = append(steps, "metrics")
	}
	steps = append(steps, "business-workers")
	steps = append(steps, ownedResourceNames(input.boot.Role)...)
	steps = append(steps, "discovery")
	announcement := ownedcontrol.ServerRuntimeAnnouncement{Version: "owned-server-owner-ready-v1", Role: input.boot.Role, Mode: input.boot.Mode, InstanceID: input.boot.InstanceID, DatabaseSHA256: input.boot.DatabaseSHA256, Epoch: input.boot.Epoch, ActivationSHA256: input.boot.ActivationSHA256, BootstrapSHA256: input.rawSHA, PolicySHA256: policySHA, DrainPath: input.boot.DrainPath, OwnedStopStepNames: steps}
	if _, err := ownedcontrol.WriteServerRuntimeAnnouncement(input.path, input.nonce, announcement); err != nil {
		return err
	}
	select {
	case <-lifetime.Done():
	case err = <-serveDone:
		serveTaken = true
		if !errors.Is(err, grpc.ErrServerStopped) {
			resultErr = err
		}
	case err = <-metricsDone:
		metricsTaken = true
		if !errors.Is(err, http.ErrServerClosed) {
			resultErr = err
		}
	}
	if err := stop(); err != nil {
		return errors.Join(resultErr, err)
	}
	currentConfig, currentEnv, err := OwnedRPCConfigurationHashes()
	if err != nil || currentConfig != runtimeConfigSHA || currentEnv != input.environmentSHA {
		return errors.Join(resultErr, errOwnedRPC)
	}
	if currentPolicy, err := input.policySHA(runtimeConfigSHA); err != nil || currentPolicy != policySHA {
		return errors.Join(resultErr, errOwnedRPC)
	}
	if !sameOwnedSteps(steps, completed) {
		return errors.Join(resultErr, errOwnedRPC)
	}
	drain := ownedcontrol.ServerRuntimeDrain{Version: "owned-server-drained-v1", Role: input.boot.Role, Mode: input.boot.Mode, InstanceID: input.boot.InstanceID, DatabaseSHA256: input.boot.DatabaseSHA256, Epoch: input.boot.Epoch, ActivationSHA256: input.boot.ActivationSHA256, BootstrapSHA256: input.rawSHA, PolicySHA256: policySHA, JoinedAndClosed: completed, SourceOutcomeUnconfirmed: false}
	_, err = ownedcontrol.WriteServerRuntimeDrain(input.boot.DrainPath, input.nonce, drain)
	return errors.Join(resultErr, err)
}
func sameOwnedSteps(expected, actual []string) bool {
	if len(expected) != len(actual) {
		return false
	}
	for i := range expected {
		if expected[i] != actual[i] {
			return false
		}
	}
	return true
}
