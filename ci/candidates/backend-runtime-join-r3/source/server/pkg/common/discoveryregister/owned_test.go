package discoveryregister

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	ownedkeeper "github.com/openimsdk/open-im-server/v3/pkg/common/discoveryregister/ownedzookeeper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

func TestOwnedKubernetesClosesActualReturnedConnectionsAndRejectsLateDials(t *testing.T) {
	old := config.Config
	t.Cleanup(func() { config.Config = old })
	t.Setenv("ENVS_DISCOVERY", "k8s")
	t.Setenv("MY_MSGGATEWAY_REPLICACOUNT", "0")
	t.Setenv("MY_POD_NAME", "owned-fixture-0")
	t.Setenv("MY_POD_NAMESPACE", "owned-fixture")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, err := NewOwnedDiscoveryRegister(ctx, "k8s")
	if err != nil {
		t.Fatal(err)
	}
	closeCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		if err := client.CloseOwned(cleanup); err != nil {
			t.Error(err)
		}
	})
	first, err := client.GetConn(ctx, "passthrough:///127.0.0.1:1", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	rest, err := client.GetConns(ctx, "passthrough:///127.0.0.1:1", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil || len(rest) != 1 {
		t.Fatal("actual Kubernetes factory did not return exact connections", err)
	}
	if err := client.CloseOwned(closeCtx); err != nil {
		t.Fatal(err)
	}
	if first.GetState() != connectivity.Shutdown || rest[0].GetState() != connectivity.Shutdown {
		t.Fatal("actual returned connections survived close")
	}
	if _, err := client.GetConn(ctx, "passthrough:///127.0.0.1:1", grpc.WithTransportCredentials(insecure.NewCredentials())); !errors.Is(err, ownedkeeper.ErrClosed) {
		t.Fatal("closed owner could dial again", err)
	}
}
