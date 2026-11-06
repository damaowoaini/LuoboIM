package zookeeper

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-zookeeper/zk"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"
)

// Only the external ZK driver is a fixture. URI parsing, the production owned
// registry/GetConn/Build, resolver ownership, gRPC channel and RPC are real.
// Roots/addresses below are synthetic; none is a product Ready declaration.
type schemeNamespaceZK struct {
	*ownedTestZK
	mu       sync.Mutex
	root     string
	endpoint string
	paths    []string
}

func (f *schemeNamespaceZK) ChildrenW(path string) ([]string, *zk.Stat, <-chan zk.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if path != f.root+"/fixture" {
		return nil, nil, nil, errors.New("unexpected original registry root")
	}
	f.paths = append(f.paths, path)
	return []string{"one"}, &zk.Stat{}, nil, nil
}
func (f *schemeNamespaceZK) Children(path string) ([]string, *zk.Stat, error) {
	rows, state, _, err := f.ChildrenW(path)
	return rows, state, err
}
func (f *schemeNamespaceZK) Get(path string) ([]byte, *zk.Stat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if path != f.root+"/fixture/one" {
		return nil, nil, errors.New("unexpected original endpoint path")
	}
	f.paths = append(f.paths, path)
	return []byte(f.endpoint), &zk.Stat{}, nil
}
func (f *schemeNamespaceZK) dial([]string, time.Duration, Logger) (ownedZKConnection, <-chan zk.Event, error) {
	return f, f.events, nil
}

func TestOwnedDiscoveryResolverSchemeIsIndependentOfNamespaceAndPerDialOwner(t *testing.T) {
	newClient := func(t *testing.T, root, endpoint string) (*ZkClient, *schemeNamespaceZK) {
		t.Helper()
		fixture := &schemeNamespaceZK{ownedTestZK: newOwnedTestZK(), root: "/" + root, endpoint: endpoint}
		client, err := newClientContext(context.Background(), []string{"fixture.invalid:2181"}, root, fixture.dial, WithLogger(ownedTestLogger{}), WithFreq(time.Hour), WithRoundRobin())
		if err != nil {
			t.Fatal("original registry initialization failed")
		}
		t.Cleanup(func() {
			end, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if client.CloseOwned(end) != nil {
				t.Error("original registry close incomplete")
			}
		})
		return client, fixture
	}
	assertRoot := func(t *testing.T, client *ZkClient, fixture *schemeNamespaceZK, root string) {
		t.Helper()
		if client.zkRoot != "/"+root || client.getPath("fixture") != "/"+root+"/fixture" {
			t.Fatal("original namespace path changed")
		}
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		if len(fixture.paths) == 0 {
			t.Fatal("actual owned resolver builder never read its original namespace")
		}
		for _, path := range fixture.paths {
			if path != "/"+root+"/fixture" && path != "/"+root+"/fixture/one" {
				t.Fatal("resolver crossed original namespace")
			}
		}
	}
	t.Run("legal-underscore-and-nested-roots-select-the-real-private-builder", func(t *testing.T) {
		scheme := ""
		for _, root := range []string{"owned-legal", "fullgraph_" + strings.Repeat("a", 32), "tenant_root/nested_scope"} {
			client, fixture := newClient(t, root, "synthetic-endpoint")
			if scheme == "" {
				scheme = client.Scheme()
			} else if client.Scheme() != scheme {
				t.Fatal("namespace still chooses URI scheme")
			}
			parsed, err := url.Parse(client.Scheme() + ":///fixture")
			if err != nil || parsed.Scheme != client.Scheme() || parsed.Path != "/fixture" {
				t.Fatal("owned scheme is not a legal resolver URI")
			}
			conn, err := client.GetConn(context.Background(), "fixture", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithNoProxy(), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
				return nil, errors.New("controlled unserved endpoint")
			}))
			if err != nil {
				t.Fatal("original GetConn failed")
			}
			if conn.CanonicalTarget() != client.Scheme()+":///fixture" {
				t.Fatal("actual gRPC channel selected a fallback resolver")
			}
			waitResolverCount(t, client, "fixture", 1)
			assertRoot(t, client, fixture, root)
		}
	})
	t.Run("same-scheme-independent-owners-bind-original-endpoints-and-close-separately", func(t *testing.T) {
		type peer struct {
			client  *ZkClient
			fixture *schemeNamespaceZK
			conn    *grpc.ClientConn
			root    string
			want    healthpb.HealthCheckResponse_ServingStatus
		}
		var peers []peer
		for at, root := range []string{"fullgraph_" + strings.Repeat("b", 32), "tenant_root/other_scope"} {
			endpoint := []string{"synthetic-endpoint-a", "synthetic-endpoint-b"}[at]
			want := []healthpb.HealthCheckResponse_ServingStatus{healthpb.HealthCheckResponse_SERVING, healthpb.HealthCheckResponse_NOT_SERVING}[at]
			listener := bufconn.Listen(64 << 10)
			server := grpc.NewServer()
			service := health.NewServer()
			service.SetServingStatus("original-binding", want)
			healthpb.RegisterHealthServer(server, service)
			joined := make(chan error, 1)
			go func() { joined <- server.Serve(listener) }()
			t.Cleanup(func() {
				server.Stop()
				_ = listener.Close()
				select {
				case <-joined:
				case <-time.After(2 * time.Second):
					t.Error("real in-memory gRPC fixture did not join")
				}
			})
			client, fixture := newClient(t, root, endpoint)
			conn, err := client.GetConn(context.Background(), "fixture", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithNoProxy(), grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
				if address != endpoint {
					return nil, errors.New("foreign original endpoint refused")
				}
				return listener.DialContext(ctx)
			}))
			if err != nil {
				t.Fatal("actual independently owned channel unavailable")
			}
			peers = append(peers, peer{client, fixture, conn, root, want})
		}
		if peers[0].client == peers[1].client || peers[0].conn == peers[1].conn || peers[0].client.Scheme() != peers[1].client.Scheme() {
			t.Fatal("per-dial registry owners were not kept independent under one scheme")
		}
		check := func(p peer) {
			t.Helper()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			reply, err := healthpb.NewHealthClient(p.conn).Check(ctx, &healthpb.HealthCheckRequest{Service: "original-binding"}, grpc.WaitForReady(true))
			if err != nil || reply.Status != p.want {
				t.Fatal("actual RPC reached a foreign endpoint or no endpoint")
			}
			assertRoot(t, p.client, p.fixture, p.root)
		}
		for _, p := range peers {
			check(p)
		}
		end, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := peers[0].client.CloseOwned(end)
		cancel()
		if err != nil {
			t.Fatal("first original owner close incomplete")
		}
		check(peers[1])
		waitResolverCount(t, peers[1].client, "fixture", 1)
		if peers[1].fixture.closes.Load() != 0 {
			t.Fatal("first registry close retired the other namespace driver")
		}
	})
}
