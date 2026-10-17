package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	kdisc "github.com/openimsdk/open-im-server/v3/pkg/common/discoveryregister"
	ginProm "github.com/openimsdk/open-im-server/v3/pkg/common/ginprometheus"
	"github.com/openimsdk/open-im-server/v3/pkg/common/prommetrics"
	"github.com/openimsdk/open-im-server/v3/pkg/common/startrpc"
)

var errOwnedAPI = errors.New("owned API lifecycle is unconfirmed")

// Load the original private owner binding before constructing any writer. This
// starts the existing API and unchanged route/auth/request handlers; it is not
// a replacement managed-ordinary runtime and never creates namespace seeds.
func RunOwnedServer(lifetime context.Context, port, metricsPort int) error {
	if lifetime == nil || lifetime.Done() == nil || port < 1 || port > 65535 || metricsPort < 1 || metricsPort > 65535 {
		return errOwnedAPI
	}
	owner, err := startrpc.LoadOwnedServerIdentity("api")
	if err != nil {
		return err
	}
	lifetime, err = owner.NamespaceRuntimeContext(lifetime)
	if err != nil {
		return err
	}
	defer owner.RetireNamespaceRuntime()
	source := &ownedMongoSource{}
	if err := owner.BindSourceObserver(&source.observer); err != nil {
		return err
	}
	return startOwnedAPI(lifetime, port, metricsPort, owner, func(ctx context.Context) (*ownedAPIAssembly, error) {
		bound, err := ownedmongo.Bind(ctx, &source.observer)
		if err != nil {
			return nil, err
		}
		resources, err := assembleOwnedAPI(bound, owner.CheckDatabase, source)
		if resources != nil {
			resources.awaitNamespace = func(ctx context.Context, steps []string) error {
				return owner.AwaitNamespaceRuntimeAdmission(ctx, steps, nil, []string{"exact-logout-recovery", "http-handlers", "http-validator", "sensitive-word-workers", "source-ownership"}, nil)
			}
		}
		return resources, err
	})
}

type apiOwnerIdentity interface {
	Announce([]string) error
	Drain([]string) error
	RecordUnconfirmed(ownedcontrol.SourceObservation) error
}
type ownedAPIAssembly struct {
	router                                                                                 http.Handler
	metrics                                                                                http.Handler
	stopLogout, stopSensitive, closeValidationHTTP, closeRedis, closeMongo, closeDiscovery func(context.Context) error
	source                                                                                 *ownedMongoSource
	activate                                                                               func() error
	awaitNamespace                                                                         func(context.Context, []string) error
	address                                                                                string // actual listener address, set before the ready announcement
}

// The unexported assembly seam exists to test actual HTTP accept/handlers/Serve
// and shutdown at the DB/registry boundary. Production only passes this factory.
func assembleOwnedAPI(parent context.Context, checkDatabase func(string) error, source *ownedMongoSource) (out *ownedAPIAssembly, err error) {
	out = &ownedAPIAssembly{}
	if checkDatabase == nil || source == nil {
		return out, errOwnedAPI
	}
	// Return the partial assembly even on panic so already created clients and
	// actual background workers are closed, never abandoned or reported drained.
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("owned API route initialization failed (%T)", value)
		}
	}()
	registryCtx, cancelRegistry := context.WithCancel(parent)
	registry, err := kdisc.NewOwnedDiscoveryRegister(registryCtx, config.Config.Envs.Discovery)
	if err != nil {
		cancelRegistry()
		return out, err
	}
	out.closeDiscovery = func(ctx context.Context) error { cancelRegistry(); return registry.CloseOwned(ctx) }
	if err := registry.CreateRpcRootNodes(config.Config.GetServiceNames()); err != nil {
		return out, err
	}
	if err := registry.RegisterConf2Registry(constant.OpenIMCommonConfigKey, config.Config.EncodeConfig()); err != nil {
		return out, err
	}
	rdb, err := cache.NewProcessRuntimeRedis(parent)
	if rdb != nil {
		out.closeRedis = func(context.Context) error { return rdb.Close() }
	}
	if err != nil {
		return out, err
	}
	out.source = source
	connection, err := unrelation.NewManagedMongoWithMonitor(parent, out.source.monitor())
	if connection != nil {
		out.closeMongo = func(ctx context.Context) error { return connection.GetDatabase().Client().Disconnect(ctx) }
	}
	if err != nil {
		return out, err
	}
	if connection == nil || rdb == nil {
		return out, errOwnedAPI
	}
	// GetDatabase reads current config. Freeze this one actual handle so the
	// original-bootstrap check and every route/index use the identical database.
	database := connection.GetDatabase()
	if database == nil || database.Name() != config.Config.Mongo.Database {
		return out, errOwnedAPI
	}
	if err := checkDatabase(database.Name()); err != nil {
		return out, err
	}
	scope, err := newOwnedRouteScope(parent, database)
	if err != nil {
		return out, err
	}
	out.stopLogout, out.stopSensitive = scope.stopLogout, scope.stopSensitive
	out.closeValidationHTTP = scope.closeValidationHTTP
	if config.Config.Prometheus.Enable {
		metrics, handler, err := ginProm.NewOwnedPrometheus("app", prommetrics.GetGinCusMetrics("Api"))
		if err != nil {
			return out, err
		}
		scope.metrics = metrics.HandlerFunc()
		out.metrics = handler
	}
	router, err := newGinRouter(registry, rdb, scope)
	if err != nil {
		return out, err
	}
	out.activate = scope.start
	out.router = router
	return out, nil
}

type ownedAPIAdmission struct {
	next              http.Handler
	mu                sync.Mutex
	enabled, stopping bool
	active            int
	done              chan struct{}
}

func (a *ownedAPIAdmission) enable() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopping || a.enabled || a.next == nil {
		return errOwnedAPI
	}
	a.enabled = true
	return nil
}
func (a *ownedAPIAdmission) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	if !a.enabled || a.stopping {
		a.mu.Unlock()
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	a.active++
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.active--
		if a.stopping && a.active == 0 {
			close(a.done)
		}
		a.mu.Unlock()
	}()
	a.next.ServeHTTP(w, r)
}
func (a *ownedAPIAdmission) stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.stopping {
		a.stopping = true
		a.enabled = false
		if a.active == 0 {
			close(a.done)
		}
	}
}
func (a *ownedAPIAdmission) join(ctx context.Context) error {
	select {
	case <-a.done:
		return nil
	case <-ctx.Done():
		return errors.Join(errOwnedAPI, ctx.Err())
	}
}

type ownedAPIListener struct {
	net.Listener
	once    sync.Once
	started chan struct{}
}

func (l *ownedAPIListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.started) })
	return l.Listener.Accept()
}

func startOwnedAPI(lifetime context.Context, port, metricsPort int, owner apiOwnerIdentity, assemble func(context.Context) (*ownedAPIAssembly, error)) (resultErr error) {
	if lifetime == nil || lifetime.Done() == nil || owner == nil || assemble == nil {
		return errOwnedAPI
	}
	work, cancelWork := context.WithCancel(context.WithoutCancel(lifetime))
	defer cancelWork()
	initialCancel := context.AfterFunc(lifetime, cancelWork)
	defer initialCancel()
	var resources *ownedAPIAssembly
	var server, metrics *http.Server
	var listener, metricsListener *ownedAPIListener
	var gate *ownedAPIAdmission
	var serveDone, metricsDone chan error
	serveTaken, metricsTaken := false, false
	var steps, completed []string
	stopOnce := sync.Once{}
	var stopErr error
	stop := func() error {
		stopOnce.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if gate != nil {
				gate.stop()
				completed = append(completed, "http-admission")
				if err := server.Shutdown(ctx); err != nil {
					stopErr = err
					return
				}
				if err := gate.join(ctx); err != nil {
					stopErr = err
					return
				}
				completed = append(completed, "http-handlers")
			}
			join := func(done chan error, taken bool) error {
				if done == nil || taken {
					return nil
				}
				select {
				case err := <-done:
					if err != nil && !errors.Is(err, http.ErrServerClosed) {
						return err
					}
					return nil
				case <-ctx.Done():
					return errors.Join(errOwnedAPI, ctx.Err())
				}
			}
			if err := join(serveDone, serveTaken); err != nil {
				stopErr = err
				return
			}
			if serveDone != nil {
				completed = append(completed, "http-serve")
			}
			if metrics != nil {
				if err := metrics.Shutdown(ctx); err != nil {
					stopErr = err
					return
				}
				if err := join(metricsDone, metricsTaken); err != nil {
					stopErr = err
					return
				}
				completed = append(completed, "metrics")
			}
			if resources != nil {
				for _, step := range []struct {
					name string
					stop func(context.Context) error
				}{
					{"exact-logout-recovery", resources.stopLogout}, {"sensitive-word-workers", resources.stopSensitive},
					{"http-validator", resources.closeValidationHTTP},
					{"redis", resources.closeRedis}, {"mongo", resources.closeMongo}, {"discovery", resources.closeDiscovery},
				} {
					if step.stop == nil {
						continue
					} // close only the actual partial-start handles
					done := make(chan error, 1)
					go func() { done <- step.stop(ctx) }()
					select {
					case err := <-done:
						if err != nil {
							stopErr = err
							return
						}
					case <-ctx.Done():
						stopErr = errors.Join(errOwnedAPI, ctx.Err())
						return
					}
					completed = append(completed, step.name)
				}
			}
		})
		return stopErr
	}
	observationAttempted := false
	defer func() {
		err := stop()
		resultErr = errors.Join(resultErr, err)
		if err == nil && !observationAttempted && resources != nil && resources.source != nil {
			if observation := resources.source.observation(); observation.SourceOutcomeUnconfirmed {
				observationAttempted = true
				resultErr = errors.Join(resultErr, errOwnedAPI, owner.RecordUnconfirmed(observation))
			}
		}
	}()
	var err error
	resources, err = assemble(work)
	if err != nil {
		return err
	}
	if resources == nil || resources.source == nil || resources.router == nil || resources.stopLogout == nil || resources.stopSensitive == nil || resources.closeValidationHTTP == nil || resources.closeRedis == nil || resources.closeMongo == nil || resources.closeDiscovery == nil {
		return errOwnedAPI
	}
	address := config.Config.Api.ListenIP
	if address == "" {
		address = "0.0.0.0"
	}
	raw, err := net.Listen("tcp", net.JoinHostPort(address, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	listener = &ownedAPIListener{Listener: raw, started: make(chan struct{})}
	resources.address = listener.Addr().String()
	defer listener.Close()
	gate = &ownedAPIAdmission{next: resources.router, done: make(chan struct{})}
	server = &http.Server{Handler: gate, ReadHeaderTimeout: 5 * time.Second}
	if resources.metrics != nil {
		raw, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(metricsPort)))
		if err != nil {
			return err
		}
		metricsListener = &ownedAPIListener{Listener: raw, started: make(chan struct{})}
		defer metricsListener.Close()
		metrics = &http.Server{Handler: resources.metrics, ReadHeaderTimeout: 5 * time.Second}
	}
	// Original handler/router, listener, workers and pool handles now exist,
	// but neither HTTP admission nor background writers have been started.
	preparedSteps := []string{"http-admission", "http-handlers", "http-serve"}
	if metrics != nil {
		preparedSteps = append(preparedSteps, "metrics")
	}
	preparedSteps = append(preparedSteps, "exact-logout-recovery", "sensitive-word-workers", "http-validator", "redis", "mongo", "discovery")
	if resources.awaitNamespace != nil {
		if err = resources.awaitNamespace(work, preparedSteps); err != nil {
			return err
		}
	}
	if resources.activate != nil {
		if err = resources.activate(); err != nil {
			return err
		}
	}
	serveDone = make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	if metrics != nil {
		metricsDone = make(chan error, 1)
		go func() { metricsDone <- metrics.Serve(metricsListener) }()
	}
	select {
	case <-listener.started:
	case err = <-serveDone:
		serveTaken = true
		return errors.Join(errOwnedAPI, err)
	case <-lifetime.Done():
		return errors.Join(errOwnedAPI, lifetime.Err())
	}
	if metrics != nil {
		select {
		case <-metricsListener.started:
		case err = <-metricsDone:
			metricsTaken = true
			return errors.Join(errOwnedAPI, err)
		case <-lifetime.Done():
			return errors.Join(errOwnedAPI, lifetime.Err())
		}
	}
	if !initialCancel() || lifetime.Err() != nil {
		return errOwnedAPI
	}
	steps = []string{"http-admission", "http-handlers", "http-serve"}
	if metrics != nil {
		steps = append(steps, "metrics")
	}
	steps = append(steps, "exact-logout-recovery", "sensitive-word-workers", "http-validator", "redis", "mongo", "discovery")
	if err := owner.Announce(steps); err != nil {
		return err
	}
	if err := gate.enable(); err != nil {
		return err
	}
	select {
	case <-lifetime.Done():
	case err = <-serveDone:
		serveTaken = true
		if err == nil {
			resultErr = errOwnedAPI
		} else if !errors.Is(err, http.ErrServerClosed) {
			resultErr = err
		}
	case err = <-metricsDone:
		metricsTaken = true
		if err == nil {
			resultErr = errOwnedAPI
		} else if !errors.Is(err, http.ErrServerClosed) {
			resultErr = err
		}
	}
	if err := stop(); err != nil {
		return errors.Join(resultErr, err)
	}
	if resultErr != nil {
		return resultErr
	}
	if observation := resources.source.observation(); observation.SourceOutcomeUnconfirmed {
		observationAttempted = true
		return errors.Join(errOwnedAPI, owner.RecordUnconfirmed(observation))
	}
	if err := owner.Drain(completed); err != nil {
		return err
	}
	return nil
}
