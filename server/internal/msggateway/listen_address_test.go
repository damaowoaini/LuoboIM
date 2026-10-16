package msggateway

import (
	"net"
	"testing"
)

func TestWebSocketListenAddressHonorsConfiguredInterface(t *testing.T) {
	for _, test := range []struct {
		host     string
		port     int
		expected string
	}{{"", 10001, ":10001"}, {"127.0.0.1", 10001, "127.0.0.1:10001"}, {"::1", 10001, "[::1]:10001"}, {"0.0.0.0", 10001, "0.0.0.0:10001"}} {
		if actual := webSocketListenAddress(test.host, test.port); actual != test.expected {
			t.Fatal("gateway ignored configured interface")
		}
	}
	listener, err := net.Listen("tcp", webSocketListenAddress("127.0.0.1", 0))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if !listener.Addr().(*net.TCPAddr).IP.IsLoopback() {
		t.Fatal("loopback fixture listener became public")
	}
}
