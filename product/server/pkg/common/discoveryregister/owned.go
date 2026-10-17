package discoveryregister

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/OpenIMSDK/tools/log"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/discoveryregister/kubernetes"
	ownedkeeper "github.com/openimsdk/open-im-server/v3/pkg/common/discoveryregister/ownedzookeeper"
	"google.golang.org/grpc"
)

type OwnedRegistry interface {
	discoveryregistry.SvcDiscoveryRegistry
	CloseOwned(context.Context) error
}

// Only explicit owned runtimes use this constructor. The original factory and
// original external module are unchanged. Full initialization failure returns
// only after attempting the same owned registry's bounded close.
func NewOwnedDiscoveryRegister(ctx context.Context, envType string) (OwnedRegistry, error) {
	if ctx == nil || ctx.Done() == nil {
		return nil, errors.New("owned discovery lifetime required")
	}
	if value := os.Getenv("ENVS_DISCOVERY"); value != "" {
		envType = value
	}
	switch envType {
	case "zookeeper":
		env := func(name, fallback string) string {
			if value, ok := os.LookupEnv(name); ok {
				return value
			}
			return fallback
		}
		addresses := append([]string{}, config.Config.Zookeeper.ZkAddr...)
		address, hasAddress := os.LookupEnv("ZOOKEEPER_ADDRESS")
		port, hasPort := os.LookupEnv("ZOOKEEPER_PORT")
		if hasAddress && hasPort {
			addresses = strings.Split(address, ",")
			for index := range addresses {
				addresses[index] += "\x3a" + port
			}
		}
		client, err := ownedkeeper.NewClientContext(ctx, addresses, env("ZOOKEEPER_SCHEMA", config.Config.Zookeeper.Schema),
			ownedkeeper.WithFreq(time.Hour), ownedkeeper.WithUserNameAndPassword(env("ZOOKEEPER_USERNAME", config.Config.Zookeeper.Username), env("ZOOKEEPER_PASSWORD", config.Config.Zookeeper.Password)),
			ownedkeeper.WithRoundRobin(), ownedkeeper.WithTimeout(10), ownedkeeper.WithLogger(log.NewZkLogger()))
		if err != nil {
			return nil, err
		}
		var names []string
		for _, name := range config.Config.GetServiceNames() {
			if name != "" {
				names = append(names, name)
			}
		}
		// Preserve the already accepted dependency-root startup fix.
		if err := client.CreateRpcRootNodes(names); err != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return nil, errors.Join(err, client.CloseOwned(closeCtx))
		}
		return client, nil
	case "k8s":
		client, err := kubernetes.NewK8sDiscoveryRegister()
		if err != nil {
			return nil, err
		}
		lifetime, cancel := context.WithCancel(ctx)
		return &ownedKubernetes{SvcDiscoveryRegistry: client, lifetime: lifetime, cancel: cancel, connections: make(map[*grpc.ClientConn]struct{}), done: make(chan struct{})}, nil
	default:
		return nil, errors.New("owned discovery type refused")
	}
}

// The existing Kubernetes implementation starts no registry workers. This
// wrapper owns all actual returned client connections and their in-flight dial
// calls. It does not claim a remote Kubernetes deployment or workload drain.
type ownedKubernetes struct {
	discoveryregistry.SvcDiscoveryRegistry
	lifetime    context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	closing     bool
	operations  sync.WaitGroup
	connections map[*grpc.ClientConn]struct{}
	once        sync.Once
	done        chan struct{}
	err         error
}

func (o *ownedKubernetes) begin(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, ownedkeeper.ErrClosed
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closing {
		return nil, nil, ownedkeeper.ErrClosed
	}
	o.operations.Add(1)
	call, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(o.lifetime, cancel)
	return call, func() { stop(); cancel(); o.operations.Done() }, nil
}
func (o *ownedKubernetes) track(connections []*grpc.ClientConn) error {
	o.mu.Lock()
	if o.closing {
		o.mu.Unlock()
		for _, connection := range connections {
			if connection != nil {
				_ = connection.Close()
			}
		}
		return ownedkeeper.ErrClosed
	}
	for _, connection := range connections {
		if connection == nil {
			o.mu.Unlock()
			for _, made := range connections {
				if made != nil {
					_ = made.Close()
				}
			}
			return errors.New("owned discovery returned nil connection")
		}
	}
	for _, connection := range connections {
		o.connections[connection] = struct{}{}
	}
	o.mu.Unlock()
	return nil
}
func (o *ownedKubernetes) GetConn(ctx context.Context, name string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	call, done, err := o.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	connection, err := o.SvcDiscoveryRegistry.GetConn(call, name, opts...)
	if err != nil {
		return nil, err
	}
	if err = o.track([]*grpc.ClientConn{connection}); err != nil {
		return nil, err
	}
	return connection, nil
}
func (o *ownedKubernetes) GetConns(ctx context.Context, name string, opts ...grpc.DialOption) ([]*grpc.ClientConn, error) {
	call, done, err := o.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	connections, err := o.SvcDiscoveryRegistry.GetConns(call, name, opts...)
	if err != nil {
		for _, connection := range connections {
			if connection != nil {
				_ = connection.Close()
			}
		}
		return nil, err
	}
	if err = o.track(connections); err != nil {
		return nil, err
	}
	return connections, nil
}
func (o *ownedKubernetes) CloseConn(connection *grpc.ClientConn) {
	if connection == nil {
		return
	}
	_ = connection.Close()
	o.mu.Lock()
	delete(o.connections, connection)
	o.mu.Unlock()
}
func (o *ownedKubernetes) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = o.CloseOwned(ctx)
}
func (o *ownedKubernetes) CloseOwned(ctx context.Context) error {
	if ctx == nil {
		return ownedkeeper.ErrClosePending
	}
	o.once.Do(func() {
		o.mu.Lock()
		o.closing = true
		connections := make([]*grpc.ClientConn, 0, len(o.connections))
		for connection := range o.connections {
			connections = append(connections, connection)
		}
		o.mu.Unlock()
		o.cancel()
		go func() {
			var result error
			for _, connection := range connections {
				if err := connection.Close(); err != nil && !errors.Is(err, grpc.ErrClientConnClosing) {
					result = errors.Join(result, err)
				}
			}
			o.operations.Wait()
			o.SvcDiscoveryRegistry.Close() // the pinned K8s implementation is synchronous/no-worker
			o.mu.Lock()
			o.connections = make(map[*grpc.ClientConn]struct{})
			o.err = result
			o.mu.Unlock()
			close(o.done)
		}()
	})
	select {
	case <-o.done:
		o.mu.Lock()
		err := o.err
		o.mu.Unlock()
		return err
	case <-ctx.Done():
		return errors.Join(ownedkeeper.ErrClosePending, ctx.Err())
	}
}
