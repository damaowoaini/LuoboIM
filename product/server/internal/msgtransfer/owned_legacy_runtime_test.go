//go:build darwin || linux

package msgtransfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/IBM/sarama"
	pbmsg "github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/kafka"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
	"github.com/openimsdk/open-im-server/v3/pkg/common/startrpc"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOwnedLegacyActualStopJoinsClaimsBeforePoolsAndClosesIndependentPartialHandles(t *testing.T) {
	for _, kind := range []string{"ack", "joined-consumer-close-error", "partial-construction"} {
		t.Run(kind, func(t *testing.T) {
			source := legacySource(t)
			runtime := &ownedLegacyRuntime{source: source, cancel: source.cancel, stopDone: make(chan struct{})}
			var mu sync.Mutex
			var order []string
			record := func(name string) { mu.Lock(); order = append(order, name); mu.Unlock() }
			runtime.closeProducers = func(context.Context) error {
				record("producers")
				if kind == "partial-construction" {
					return errors.New("synthetic closed partial producer error")
				}
				return nil
			}
			runtime.closeRedis = func(context.Context) error { record("redis"); return nil }
			runtime.closeMongo = func(context.Context) error { record("mongo"); return nil }
			runtime.closeDiscovery = func(context.Context) error { record("discovery"); return nil }
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			runtime.metricsListener = listener
			runtime.metrics = &http.Server{Handler: http.NewServeMux()}
			t.Cleanup(func() { _ = listener.Close() })
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { source.cancel(); once.Do(func() { close(release) }) })
			var group *legacyGroup
			if kind != "partial-construction" {
				db := &legacyDatabase{insert: func(context.Context, string, []*sdkws.MsgData, int64) error { close(entered); <-release; return nil }}
				h := &ownedLegacyClaim{source: source, mongoHandler: &OnlineHistoryMongoConsumerHandler{msgDatabase: db}}
				run, g := startLegacyClaim(t, source, h)
				group = g
				if kind == "joined-consumer-close-error" {
					g.closeErr = errors.New("synthetic group leave failure after join")
				}
				runtime.consumers = []*kafka.OwnedConsumerRun{run}
				runtime.metricsDone = make(chan error, 1)
				go func() { runtime.metricsDone <- runtime.metrics.Serve(listener) }()
				t.Cleanup(func() {
					once.Do(func() { close(release) })
					source.cancel()
					c, done := context.WithTimeout(context.Background(), 3*time.Second)
					defer done()
					_ = runtime.Stop(c)
				})
				g.messages <- legacyRecord(t, &pbmsgForLegacyStop)
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					t.Fatal("actual original write not entered")
				}
			}
			stopped := make(chan error, 1)
			stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			go func() { stopped <- runtime.Stop(stopCtx) }()
			select {
			case <-source.parent.Done():
			case <-time.After(time.Second):
				t.Fatal("admission/owner not canceled")
			}
			if group != nil {
				mu.Lock()
				premature := len(order) != 0
				mu.Unlock()
				if premature || group.closes.Load() != 0 {
					t.Fatal("source pools/client closed beneath active claim")
				}
				once.Do(func() { close(release) })
			}
			select {
			case err = <-stopped:
			case <-time.After(3 * time.Second):
				t.Fatal("original owner did not finish stop")
			}
			if (err == nil) != (kind == "ack") {
				t.Fatal("close error was hidden or ACK close failed", err)
			}
			mu.Lock()
			actual := append([]string{}, order...)
			mu.Unlock()
			if !reflect.DeepEqual(actual, []string{"producers", "redis", "mongo", "discovery"}) {
				t.Fatal("partial/failed close skipped independent resources", actual)
			}
			if tcp, ok := listener.(*net.TCPListener); ok {
				_ = tcp.SetDeadline(time.Now().Add(time.Second))
			}
			if _, err := listener.Accept(); err == nil {
				t.Fatal("actual metrics listener survived stop")
			}
			if kind == "ack" && !reflect.DeepEqual(runtime.completed, runtime.steps()) {
				t.Fatal("successful stop missing actual completed steps", runtime.completed)
			}
			if kind != "ack" && reflect.DeepEqual(runtime.completed, runtime.steps()) {
				t.Fatal("failed close claimed complete exclusion steps")
			}
			again, cancelAgain := context.WithTimeout(context.Background(), time.Second)
			defer cancelAgain()
			_ = runtime.Stop(again)
			mu.Lock()
			repeated := len(order) != 4
			mu.Unlock()
			if repeated {
				t.Fatal("same original resources closed twice")
			}
		})
	}
}

// Real original processor input; the test never calls an alternative storage
// implementation or fabricates a ready/namespace seed.
var pbmsgForLegacyStop = pbmsg.MsgDataToMongoByMQ{ConversationID: "si_synthetic-stop", MsgData: []*sdkws.MsgData{{Seq: 1, ClientMsgID: "synthetic-stop"}}, LastSeq: 1}

func TestOwnedLegacyRequiredHistoryFenceRejectsBeforePrivateBootstrapOrStorage(t *testing.T) {
	t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_V1", "required")
	t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_EPOCH", "")
	t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_ACTIVATION_SHA256", "")
	t.Setenv("LUOBO_OWNED_SERVER_BOOTSTRAP_PATH", "")
	t.Setenv("LUOBO_OWNED_SERVER_BOOTSTRAP_SHA256", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := runOwnedLegacyTransfer(ctx, 1); !errors.Is(err, messagedelivery.ErrUnprepared) {
		t.Fatal("legacy process crossed required-mode hard fence", err)
	}
}

func legacyBootstrap(t *testing.T) (string, string, string) {
	t.Helper()
	previous := config.Config
	t.Cleanup(func() { config.Config = previous })
	config.Config.Mongo.Database = "owned-legacy-fixture"
	config.Config.Prometheus.Enable = true
	t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_V1", "0")
	t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_EPOCH", "")
	t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_ACTIVATION_SHA256", "")
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path, ready, drain := filepath.Join(dir, "bootstrap.json"), filepath.Join(dir, "ready.json"), filepath.Join(dir, "drain.json")
	nonce := strings.Repeat("a", 64)
	t.Setenv(startrpc.OwnedRPCBootstrapEnv, path)
	t.Setenv("LUOBO_MANAGED_OWNER_ANNOUNCEMENT_PATH", ready)
	t.Setenv("LUOBO_MANAGED_OWNER_NONCE", nonce)
	configuration, env, err := startrpc.OwnedRPCConfigurationHashes()
	if err != nil {
		t.Fatal(err)
	}
	hash := func(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
	boot := startrpc.OwnedRPCBootstrap{Version: "owned-server-bootstrap-v1", Role: "msgtransfer-legacy", Mode: "legacy", InstanceID: "11111111-1111-4111-8111-111111111111", DatabaseSHA256: hash([]byte(config.Config.Mongo.Database)), ConfigurationSHA256: configuration, EnvironmentSHA256: env, DrainPath: drain}
	raw, err := json.Marshal(boot)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(startrpc.OwnedRPCBootstrapSHAEnv, hash(raw))
	return ready, drain, nonce
}
func TestOwnedLegacyActualPrivateStartPartialFailureAndUnknownOutcomeKeepOriginalOwner(t *testing.T) {
	for _, kind := range []string{"ack", "partial-constructor", "unknown-work"} {
		t.Run(kind, func(t *testing.T) {
			ready, drain, nonce := legacyBootstrap(t)
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			var mu sync.Mutex
			var order []string
			record := func(value string) { mu.Lock(); order = append(order, value); mu.Unlock() }
			group := &legacyGroup{messages: make(chan *sarama.ConsumerMessage, 1), marks: make(chan *sarama.ConsumerMessage, 1)}
			result, finished := make(chan error, 1), make(chan struct{})
			build := func(ctx context.Context, r *ownedLegacyRuntime, _ int) error {
				if err := r.identity.CheckDatabase(config.Config.Mongo.Database); err != nil {
					return err
				}
				r.closeProducers = func(context.Context) error { record("producers"); return nil }
				r.closeRedis = func(context.Context) error { record("redis"); return nil }
				r.closeMongo = func(context.Context) error { record("mongo"); return nil }
				r.closeDiscovery = func(context.Context) error { record("discovery"); return nil }
				db := &legacyDatabase{insert: func(ctx context.Context, _ string, _ []*sdkws.MsgData, _ int64) error {
					if kind == "unknown-work" {
						ownedsource.Attempt(ctx, ownedsource.KafkaProducer)(true)
						return errors.New("synthetic unconfirmed original queue write")
					}
					return nil
				}}
				handler := &ownedLegacyClaim{source: r.source, mongoHandler: &OnlineHistoryMongoConsumerHandler{msgDatabase: db}}
				run, err := kafka.NewOwnedConsumerRun(ctx, group, []string{"original-owned-topic"}, handler, r.source.Fail)
				if err != nil {
					return err
				}
				handler.run = run
				r.consumers = append(r.consumers, run)
				if kind == "partial-constructor" {
					return errors.New("synthetic next external constructor failure")
				}
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					return err
				}
				r.metricsListener = listener
				r.metrics = &http.Server{Handler: http.NewServeMux()}
				return nil
			}
			go func() { defer close(finished); result <- runOwnedLegacyTransferWithResources(parent, 1, build) }()
			t.Cleanup(func() {
				cancel()
				select {
				case <-finished:
				case <-time.After(3 * time.Second):
					t.Error("real private owner did not finish")
				}
			})
			if kind != "partial-constructor" {
				bound := time.NewTimer(3 * time.Second)
				defer bound.Stop()
				tick := time.NewTicker(time.Millisecond)
				defer tick.Stop()
				for {
					_, _, err := ownedcontrol.ReadServerRuntimeAnnouncement(ready, nonce)
					if err == nil {
						break
					}
					select {
					case <-bound.C:
						t.Fatal("real assignment/start did not announce original owner")
					case <-tick.C:
					}
				}
				if kind == "unknown-work" {
					group.messages <- legacyRecord(t, &pbmsgForLegacyStop)
				} else {
					cancel()
				}
			}
			var actual error
			select {
			case actual = <-result:
			case <-time.After(3 * time.Second):
				t.Fatal("real original stop/join failed")
			}
			if (actual == nil) != (kind == "ack") || group.closes.Load() != 1 {
				t.Fatal("start/partial/unknown ownership outcome differs", actual)
			}
			mu.Lock()
			closed := append([]string{}, order...)
			mu.Unlock()
			if !reflect.DeepEqual(closed, []string{"producers", "redis", "mongo", "discovery"}) {
				t.Fatal("real start failure/stop skipped original resource", closed)
			}
			if kind == "ack" {
				if _, _, err := ownedcontrol.ReadServerRuntimeDrain(drain, nonce); err != nil {
					t.Fatal("actual complete owner lacked drain", err)
				}
			} else {
				if _, err := os.Lstat(drain); !os.IsNotExist(err) {
					t.Fatal("failed or unknown owner wrote canonical exclusion drain")
				}
				if kind == "partial-constructor" {
					if _, err := os.Lstat(ready); !os.IsNotExist(err) {
						t.Fatal("partial external constructor announced ready")
					}
				} else {
					body, err := os.ReadFile(drain + ".unconfirmed")
					if err != nil {
						t.Fatal(err)
					}
					var envelope struct {
						OwnerNonce  string                         `json:"ownerNonce"`
						Observation ownedcontrol.SourceObservation `json:"observation"`
					}
					if json.Unmarshal(body, &envelope) != nil || envelope.OwnerNonce != nonce || !envelope.Observation.IntakeUnconfirmed || !envelope.Observation.External.KafkaProducer.Unknown || envelope.Observation.BootstrapSHA256 == "" || envelope.Observation.PolicySHA256 == "" {
						t.Fatal("actual original unknown lost nonpermit custody")
					}
					select {
					case <-group.marks:
						t.Fatal("unknown original message was marked")
					default:
					}
				}
			}
		})
	}
}
