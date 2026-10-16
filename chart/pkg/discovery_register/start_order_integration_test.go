package discovery_register

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/config"
	openkeeper "github.com/OpenIMSDK/tools/discoveryregistry/zookeeper"
	"github.com/OpenIMSDK/tools/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	hp "google.golang.org/grpc/health/grpc_health_v1"
)

func TestLatePeerResolvesAfterFactoryStartsFirst(t *testing.T) {
	address := os.Getenv("LUOBO_ZOOKEEPER_TEST_ADDRESS")
	if address == "" {
		t.Skip("requires explicit loopback ZooKeeper fixture")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("fixture must use loopback")
	}
	if err := log.InitFromConfig("zk-start-order-test", "fixture", 3, false, false, t.TempDir(), 1, 1); err != nil {
		t.Fatal(err)
	}
	saved := config.Config
	defer func() { config.Config = saved }()
	config.Config.RpcRegisterName.OpenImAdminName = "LateService"
	config.Config.Zookeeper.Schema = "luobochartstarttest" + time.Now().Format("150405.000000000")
	config.Config.Zookeeper.ZkAddr = []string{address}
	config.Config.Zookeeper.Username = ""
	config.Config.Zookeeper.Password = ""
	registry, err := NewDiscoveryRegister("zookeeper")
	if err != nil {
		t.Fatal(err)
	}
	client := registry.(*openkeeper.ZkClient)
	defer client.Close()
	defer func() {
		conn := client.GetZkConn()
		names, _, _ := conn.Children(client.GetRootPath())
		for _, name := range names {
			path := client.GetRootPath() + "/" + name
			children, _, _ := conn.Children(path)
			for _, child := range children {
				_ = conn.Delete(path+"/"+child, -1)
			}
			_ = conn.Delete(path, -1)
		}
		_ = conn.Delete(client.GetRootPath(), -1)
	}()
	connection, err := registry.GetConn(context.Background(), "LateService", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	// No server exists when the resolver starts. Registration happens later.
	time.Sleep(50 * time.Millisecond)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	h := health.NewServer()
	h.SetServingStatus("", hp.HealthCheckResponse_SERVING)
	hp.RegisterHealthServer(server, h)
	go server.Serve(listener)
	defer server.Stop()
	if _, err := client.CreateTempNode("LateService", listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	response, err := hp.NewHealthClient(connection).Check(ctx, &hp.HealthCheckRequest{}, grpc.WaitForReady(true))
	if err != nil || response.GetStatus() != hp.HealthCheckResponse_SERVING {
		t.Fatalf("late peer failed: %v", err)
	}
}
