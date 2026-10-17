//go:build linux

package runtimeacceptance

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type PrivatePlan struct {
	Version           string `json:"version"`
	Scenario          string `json:"scenario"`
	Work              string `json:"work"`
	PlanePath         string `json:"planePath"`
	PlaneSHA256       string `json:"planeSHA256"`
	CredentialsPath   string `json:"credentialsPath"`
	CredentialsSHA256 string `json:"credentialsSHA256"`
	BuildsPath        string `json:"buildsPath"`
	BuildsSHA256      string `json:"buildsSHA256"`
}
type Builds struct {
	Version               string           `json:"version"`
	SourceSHA256          string           `json:"sourceSHA256"`
	Binaries              []BinaryArtifact `json:"binaries"`
	LegacyMessageTransfer BinaryArtifact   `json:"legacyMessageTransfer"`
}
type Bootstrap struct {
	Plan                                  PrivatePlan
	Inputs                                Inputs
	AdminMongo, LegacyMongo, RuntimeMongo *mongo.Client
	AdminRedis, LegacyRedis, RuntimeRedis *redis.Client
	Proxy                                 *RedisACKLoss
	Header                                namespaceimport.Header
	Config                                namespaceimport.Config
	Services                              ServicePlan
	ManagerID                             string
	TokenUserID                           string
	TokenPlatform                         int32
	Credential                            map[string]string
	Ports                                 map[string]int
	Resource                              ResourcePlane
	BusinessPermissions                   BusinessPermissionEvidence
}

func loadPinnedPrivate(path, expected string, max int, out any) error {
	raw, e := namespaceinventory.ReadPrivateDocument(path, max)
	if e != nil || digestBytes(raw) != expected {
		return ownedcontrol.ErrOwnedEvidence
	}
	if namespaceinventory.DecodeStrict(raw, out) != nil {
		return ownedcontrol.ErrOwnedEvidence
	}
	return nil
}
func acceptanceScenario(value string) bool {
	return value == "ack" || value == "redis-unknown" || value == "reader-registered-cancel" || value == "reader-writer-fence-cancel"
}
func ReadPlan(path, expected string) (PrivatePlan, error) {
	var p PrivatePlan
	if loadPinnedPrivate(path, expected, 64<<10, &p) != nil || p.Version != "owned-fullgraph-acceptance-v1" || !acceptanceScenario(p.Scenario) || canonicalDirectory(p.Work) != nil {
		return p, ownedcontrol.ErrOwnedEvidence
	}
	for _, v := range []string{p.PlanePath, p.CredentialsPath, p.BuildsPath} {
		if !filepath.IsAbs(v) || filepath.Clean(v) != v {
			return p, ownedcontrol.ErrOwnedEvidence
		}
	}
	return p, nil
}
func mongoURI(user, password, database string, direct bool) string {
	u := url.URL{Scheme: "mongodb", Host: "127.0.0.1:27017", Path: "/" + database, User: url.UserPassword(user, password)}
	q := url.Values{"authSource": {"admin"}, "retryWrites": {"false"}, "retryReads": {"false"}}
	if direct {
		q.Set("directConnection", "true")
	} else {
		q.Set("replicaSet", "luoboFullgraph")
		q.Set("directConnection", "false")
	}
	u.RawQuery = q.Encode()
	return u.String()
}
func redisClient(user, password string) *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379", Username: user, Password: password, DB: 0, PoolSize: 4, MaxRetries: -1, DialTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, ContextTimeoutEnabled: true})
}
func connectMongo(ctx context.Context, uri string) (*mongo.Client, error) {
	return mongo.Connect(ctx, options.Client().ApplyURI(uri).SetMaxPoolSize(4).SetMinPoolSize(0).SetConnectTimeout(2*time.Second).SetServerSelectionTimeout(2*time.Second).SetSocketTimeout(5*time.Second).SetRetryReads(false).SetRetryWrites(false))
}
func retryRead(ctx context.Context, read func() error) error {
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		if e := read(); e == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
func freshKafkaConfig(user, password string) *sarama.Config {
	c := sarama.NewConfig()
	c.Version = sarama.V2_8_1_0
	c.ClientID = "owned-fullgraph-fresh-bootstrap-v1"
	c.Net.SASL.Enable = true
	c.Net.SASL.Mechanism = sarama.SASLTypePlaintext
	c.Net.SASL.User = user
	c.Net.SASL.Password = password
	c.Net.DialTimeout = 3 * time.Second
	c.Net.ReadTimeout = 3 * time.Second
	c.Net.WriteTimeout = 3 * time.Second
	c.Metadata.Retry.Max = 0
	c.Admin.Retry.Max = 0
	return c
}

// PrepareFresh is an explicit privileged fixture bootstrap, before any owned
// writer exists. It only runs after actual Docker/kernel plane verification.
// Namespace/history/Ready markers are NEVER written here. The real held writer
// must prepare those later, after true joins/drain/credential exclusion.
func PrepareFresh(ctx context.Context, p PrivatePlan) (*Bootstrap, error) {
	if ctx == nil || ctx.Done() == nil || canonicalDirectory(p.Work) != nil {
		return nil, ownedcontrol.ErrOwnedEvidence
	}
	out := &Bootstrap{Plan: p, Ports: map[string]int{"api": 31000, "msggateway": 31002, "push": 31019, "rpc-auth": 31010, "rpc-conversation": 31011, "rpc-encryption": 31012, "rpc-friend": 31013, "rpc-group": 31014, "rpc-msg": 31015, "rpc-rtc": 31016, "rpc-third": 31017, "rpc-user": 31018}}
	if e := loadPinnedPrivate(p.PlanePath, p.PlaneSHA256, 128<<10, &out.Resource); e != nil {
		return out, e
	}
	if e := out.Resource.ValidateLive(ctx); e != nil {
		return out, e
	}
	if e := loadPinnedPrivate(p.CredentialsPath, p.CredentialsSHA256, 64<<10, &out.Credential); e != nil {
		return out, e
	}
	c := out.Credential
	for _, key := range []string{"mongoAdminUser", "mongoAdminPassword", "mongoLegacyUser", "mongoLegacyPassword", "mongoRuntimeUser", "mongoRuntimePassword", "redisAdminUser", "redisAdminPassword", "redisLegacyUser", "redisLegacyPassword", "redisRuntimeUser", "redisRuntimePassword", "kafkaAdminUser", "kafkaAdminPassword", "kafkaLegacyUser", "kafkaLegacyPassword", "kafkaRuntimeUser", "kafkaRuntimePassword", "minioUser", "minioSecret", "serviceSecret", "rtcKey", "rtcSecret"} {
		v := c[key]
		if v == "" || len(v) > 256 || strings.ContainsAny(v, "\x00\r\n \t") {
			return out, ownedcontrol.ErrOwnedEvidence
		}
	}
	var built Builds
	if loadPinnedPrivate(p.BuildsPath, p.BuildsSHA256, 256<<10, &built) != nil || built.Version != "source-bound-fullgraph-builds-v1" || len(built.Binaries) != 14 {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	for _, b := range append(append([]BinaryArtifact(nil), built.Binaries...), built.LegacyMessageTransfer) {
		if b.SourceSHA256 != built.SourceSHA256 {
			return out, ownedcontrol.ErrOwnedEvidence
		}
	}
	// Initial root auth already came from the real Mongo image's fresh initdb.
	initial, e := connectMongo(ctx, mongoURI(c["mongoAdminUser"], c["mongoAdminPassword"], "admin", true))
	if e != nil {
		return out, e
	}
	defer func() {
		end, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = initial.Disconnect(end)
	}()
	if e = retryRead(ctx, func() error { return initial.Database("admin").RunCommand(ctx, bson.D{{Key: "ping", Value: 1}}).Err() }); e != nil {
		return out, e
	}
	// The first write is dispatched only after actual uninitialized-state proof.
	// If its reply is lost, this invocation stops; it does not retry initiation.
	var old bson.Raw
	e = initial.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetGetConfig", Value: 1}}).Decode(&old)
	var command mongo.CommandError
	if !errors.As(e, &command) || command.Code != 94 {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	cfg := bson.D{{Key: "_id", Value: "luoboFullgraph"}, {Key: "members", Value: bson.A{bson.D{{Key: "_id", Value: int32(0)}, {Key: "host", Value: "127.0.0.1:27017"}}}}}
	if e = initial.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetInitiate", Value: cfg}}).Err(); e != nil {
		return out, e
	}
	var hello bson.Raw
	if e = retryRead(ctx, func() error {
		if e := initial.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); e != nil {
			return e
		}
		v, ok := hello.Lookup("isWritablePrimary").BooleanOK()
		if !ok || !v {
			return ownedcontrol.ErrOwnedEvidence
		}
		return nil
	}); e != nil {
		return out, e
	}
	h := namespaceimport.Header{Tenant: "fullgraph-" + out.Resource.Nonce, Database: "fullgraph_" + out.Resource.Nonce, StorageInstance: uuid.NewString(), ReplicaSet: "luoboFullgraph", RedisDB: 0, Epoch: uuid.NewString(), MigrationID: uuid.NewString(), ActivationSHA256: digestValue([]string{"fullgraph-activation-v1", out.Resource.Nonce, built.SourceSHA256})}
	topology := hello.Lookup("topologyVersion").Document()
	process, ok := topology.Lookup("processId").ObjectIDOK()
	count, ok2 := topology.Lookup("counter").Int64OK()
	rs, ok3 := hello.Lookup("setName").StringValueOK()
	if !ok || !ok2 || !ok3 || rs != h.ReplicaSet {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	h.MongoProcess, h.MongoTopology = process.Hex(), count
	out.AdminMongo, e = connectMongo(ctx, mongoURI(c["mongoAdminUser"], c["mongoAdminPassword"], h.Database, false))
	if e != nil {
		return out, e
	}
	db := out.AdminMongo.Database(h.Database)
	names, e := db.ListCollectionNames(ctx, bson.D{})
	if e != nil || len(names) != 0 {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	// Exact explicit fresh metadata, not a claim that any reused CID/history is
	// empty. The actual importer sees and seals these original BSON bytes.
	if e = db.CreateCollection(ctx, "luobo_storage_identity"); e != nil {
		return out, e
	}
	if _, e = db.Collection("luobo_storage_identity").InsertOne(ctx, bson.D{{Key: "_id", Value: "message-visibility-v1"}, {Key: "instance", Value: h.StorageInstance}, {Key: "tenant", Value: h.Tenant}}); e != nil {
		return out, e
	}
	for _, role := range []string{"Legacy", "Runtime"} {
		user, password := c["mongo"+role+"User"], c["mongo"+role+"Password"]
		if e = out.AdminMongo.Database("admin").RunCommand(ctx, bson.D{{Key: "createUser", Value: user}, {Key: "pwd", Value: password}, {Key: "roles", Value: bson.A{bson.D{{Key: "role", Value: "readWrite"}, {Key: "db", Value: h.Database}}, bson.D{{Key: "role", Value: "dbAdmin"}, {Key: "db", Value: h.Database}}}}}).Err(); e != nil {
			return out, e
		}
	}
	out.LegacyMongo, e = connectMongo(ctx, mongoURI(c["mongoLegacyUser"], c["mongoLegacyPassword"], h.Database, false))
	if e != nil {
		return out, e
	}
	out.RuntimeMongo, e = connectMongo(ctx, mongoURI(c["mongoRuntimeUser"], c["mongoRuntimePassword"], h.Database, false))
	if e != nil {
		return out, e
	}
	out.AdminRedis = redisClient(c["redisAdminUser"], c["redisAdminPassword"])
	out.LegacyRedis = redisClient(c["redisLegacyUser"], c["redisLegacyPassword"])
	out.RuntimeRedis = redisClient(c["redisRuntimeUser"], c["redisRuntimePassword"])
	if e = retryRead(ctx, func() error { return out.AdminRedis.Ping(ctx).Err() }); e != nil {
		return out, e
	}
	size, e := out.AdminRedis.DBSize(ctx).Result()
	if e != nil || size != 0 {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	info, e := out.AdminRedis.Info(ctx, "server").Result()
	if e != nil {
		return out, e
	}
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, "run_id:") {
			h.RedisRunID = strings.TrimSpace(strings.TrimPrefix(line, "run_id:"))
		}
	}
	acl, e := out.AdminRedis.Do(ctx, "ACL", "GETUSER", c["redisLegacyUser"]).Result()
	if e != nil {
		return out, e
	}
	var userInfo struct {
		Users []bson.Raw `bson:"users"`
	}
	if e = out.AdminMongo.Database("admin").RunCommand(ctx, bson.D{{Key: "usersInfo", Value: bson.D{{Key: "user", Value: c["mongoLegacyUser"]}, {Key: "db", Value: "admin"}}}}).Decode(&userInfo); e != nil || len(userInfo.Users) != 1 {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	originalUserID := userInfo.Users[0].Lookup("userId")
	if originalUserID.Type == 0 {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	mongoVersion := digestBytes(append([]byte{byte(originalUserID.Type)}, originalUserID.Value...))
	topics := []string{"fullgraph_" + out.Resource.Nonce + "_allocation", "fullgraph_" + out.Resource.Nonce + "_persistence", "fullgraph_" + out.Resource.Nonce + "_push"}
	groups := []string{"fullgraph_" + out.Resource.Nonce + "_online", "fullgraph_" + out.Resource.Nonce + "_offline", "fullgraph_" + out.Resource.Nonce + "_push"}
	client, e := sarama.NewClient([]string{"127.0.0.1:9092"}, freshKafkaConfig(c["kafkaAdminUser"], c["kafkaAdminPassword"]))
	if e != nil {
		return out, e
	}
	defer client.Close()
	admin, e := sarama.NewClusterAdminFromClient(client)
	if e != nil {
		return out, e
	}
	defer admin.Close()
	controller, e := client.Controller()
	if e != nil {
		return out, e
	}
	metadata, e := controller.GetMetadata(&sarama.MetadataRequest{Version: 7, AllowAutoTopicCreation: false})
	if e != nil || metadata.ClusterID == nil || *metadata.ClusterID == "" || *metadata.ClusterID != out.Resource.KafkaClusterID {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	cluster := *metadata.ClusterID
	h.KafkaClusterID = cluster
	existing, e := admin.ListTopics()
	if e != nil {
		return out, e
	}
	for _, topic := range topics {
		if _, exists := existing[topic]; exists {
			return out, ownedcontrol.ErrOwnedEvidence
		}
		retention, cleanup := "86400000", "delete"
		if e = admin.CreateTopic(topic, &sarama.TopicDetail{NumPartitions: 1, ReplicationFactor: 1, ConfigEntries: map[string]*string{"retention.ms": &retention, "cleanup.policy": &cleanup}}, false); e != nil {
			return out, e
		}
	}
	// Only these original nonce-bound topics/groups belong to either business
	// principal. Bootstrap/credential ACL operations remain admin-only; ordinary
	// producers are non-idempotent and have no transactional ID in this fixture.
	for _, user := range []string{c["kafkaLegacyUser"], c["kafkaRuntimeUser"]} {
		for _, topic := range topics {
			for _, operation := range []sarama.AclOperation{sarama.AclOperationRead, sarama.AclOperationWrite, sarama.AclOperationDescribe} {
				if e = admin.CreateACL(sarama.Resource{ResourceType: sarama.AclResourceTopic, ResourceName: topic, ResourcePatternType: sarama.AclPatternLiteral}, sarama.Acl{Principal: "User:" + user, Host: "127.0.0.1", Operation: operation, PermissionType: sarama.AclPermissionAllow}); e != nil {
					return out, e
				}
			}
		}
		for _, group := range groups {
			for _, operation := range []sarama.AclOperation{sarama.AclOperationRead, sarama.AclOperationDescribe} {
				if e = admin.CreateACL(sarama.Resource{ResourceType: sarama.AclResourceGroup, ResourceName: group, ResourcePatternType: sarama.AclPatternLiteral}, sarama.Acl{Principal: "User:" + user, Host: "127.0.0.1", Operation: operation, PermissionType: sarama.AclPermissionAllow}); e != nil {
					return out, e
				}
			}
		}
	}
	out.BusinessPermissions, e = checkBusinessPermissions(ctx, out, cluster, topics)
	if e != nil {
		return out, e
	}
	var parts []namespaceimport.KafkaPartitionIdentity
	for at := 0; at < 2; at++ {
		topic, group := topics[at], groups[at]
		if e = client.RefreshMetadata(topic); e != nil {
			return out, e
		}
		begin, e := client.GetOffset(topic, 0, sarama.OffsetOldest)
		if e != nil || begin != 0 {
			return out, ownedcontrol.ErrOwnedEvidence
		}
		end, e := client.GetOffset(topic, 0, sarama.OffsetNewest)
		if e != nil || end != 0 {
			return out, ownedcontrol.ErrOwnedEvidence
		}
		broker, e := client.Coordinator(group)
		if e != nil {
			return out, e
		}
		fetch := &sarama.OffsetFetchRequest{Version: 7, ConsumerGroup: group, RequireStable: true}
		fetch.AddPartition(topic, 0)
		old, e := broker.FetchOffset(fetch)
		if e != nil || old.Err != sarama.ErrNoError {
			return out, ownedcontrol.ErrOwnedEvidence
		}
		block := old.GetBlock(topic, 0)
		if block == nil || block.Err != sarama.ErrNoError || block.Offset != -1 {
			return out, ownedcontrol.ErrOwnedEvidence
		}
		// This is the one explicit bootstrap offset commit for a proven new empty
		// group/topic. It is never used by the importer or to reset an old group.
		commit := &sarama.OffsetCommitRequest{Version: 7, ConsumerGroup: group, ConsumerGroupGeneration: -1, ConsumerID: ""}
		commit.AddBlock(topic, 0, 0, 0, "owned-fresh-empty-bootstrap-v1")
		reply, e := broker.CommitOffset(commit)
		if e != nil || reply.Errors[topic][0] != sarama.ErrNoError {
			return out, ownedcontrol.ErrOwnedEvidence
		}
		plane := "producer-to-allocation"
		if at == 1 {
			plane = "allocation-to-persistence"
		}
		parts = append(parts, namespaceimport.KafkaPartitionIdentity{Plane: plane, Topic: topic, Group: group, Partition: 0})
	}
	probePublic, probePrivate, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return out, e
	}
	capturePublic, capturePrivate, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return out, e
	}
	_, releasePrivate, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return out, e
	}
	ids := map[string]string{"rpc-msg": uuid.NewString(), "msgtransfer-legacy": uuid.NewString()}
	artifactBy := map[string]BinaryArtifact{}
	for _, a := range built.Binaries {
		artifactBy[a.Role] = a
	}
	artifactBy["msgtransfer-legacy"] = built.LegacyMessageTransfer
	cfgImport := namespaceimport.Config{Header: h, ProbeKeyID: "fresh-owned-agent", ProbePublicKey: probePublic, CaptureKeyID: "fresh-owned-capture", CapturePublicKey: capturePublic, CaptureAgent: "owned-fullgraph-capture-v1", OperationTimeout: 20 * time.Second, PermitLifetime: 25 * time.Second, PageBytes: 256 << 10, PageRows: 64, KafkaPartitions: parts}
	for _, role := range []string{"rpc-msg", "msgtransfer-legacy"} {
		a := artifactBy[role]
		cfgImport.Writers = append(cfgImport.Writers, namespaceimport.WriterIdentity{Deployment: "fresh-acceptance-" + h.MigrationID + "-" + role, Incarnation: ids[role], BinarySHA256: a.SHA256, SourceSHA256: a.SourceSHA256})
	}
	kafkaVersion := digestValue([]string{"actual-private-SASL-source-v1", cluster, c["kafkaLegacyUser"], p.CredentialsSHA256})
	cfgImport.Credentials = []namespaceimport.CredentialIdentity{{Scope: "mongo", CredentialID: c["mongoLegacyUser"], Version: mongoVersion}, {Scope: "redis", CredentialID: c["redisLegacyUser"], Version: digestValue(acl)}, {Scope: "kafka", CredentialID: c["kafkaLegacyUser"], Version: kafkaVersion}}
	cfgImport.Header.DeploymentPolicySHA256 = namespaceimport.DeploymentPolicySHA256(cfgImport)
	h = cfgImport.Header
	out.Header, out.Config = h, cfgImport
	out.ManagerID = "fullgraph_manager_" + out.Resource.Nonce
	out.TokenUserID = "fullgraph_token_" + out.Resource.Nonce
	out.TokenPlatform = 5
	runtimeDirectory, e := out.writeConfiguration("runtime-config", false, "127.0.0.1:6379", topics, groups)
	if e != nil {
		return out, e
	}
	legacyDirectory, e := out.writeConfiguration("legacy-config", true, "127.0.0.1:6379", topics, groups)
	if e != nil {
		return out, e
	}
	key := "FCM_TOKEN:" + out.TokenUserID + ":" + strconv.Itoa(int(out.TokenPlatform))
	out.Proxy, e = NewRedisACKLoss(ctx, "127.0.0.1:16379", "127.0.0.1:6379", key)
	if e != nil {
		return out, e
	}
	pushDirectory, e := out.writeConfiguration("push-config", false, out.Proxy.Address(), topics, groups)
	if e != nil {
		return out, e
	}
	serviceWork := filepath.Join(p.Work, "services")
	if e = os.Mkdir(serviceWork, 0700); e != nil {
		return out, e
	}
	out.Services, e = PrepareServicePlan(ServicePlanInputs{Work: serviceWork, Header: h, Binaries: built.Binaries, LegacyMessageTransfer: built.LegacyMessageTransfer, RuntimeConfigDirectory: runtimeDirectory, RuntimeConfigDirectories: map[string]string{"push": pushDirectory}, LegacyConfigDirectory: legacyDirectory, RTCEnvironment: map[string]string{"LUOBO_RTC_LIVE_URL": "ws://127.0.0.1:47991", "LUOBO_RTC_ALLOW_LOOPBACK": "true", "LUOBO_RTC_API_KEY": c["rtcKey"], "LUOBO_RTC_API_SECRET": c["rtcSecret"]}, BadgeEvidenceSHA256: digestValue([]any{"actual-fresh-redis-empty-v1", h.RedisRunID, h.RedisDB, size}), LegacyInstanceIDs: ids})
	if e != nil {
		return out, e
	}
	if !exactJSON(out.Services.WriterIdentities, cfgImport.Writers) {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	for _, name := range []string{"launcher", "capture-private", "capture-raw-parent", "capture-derived-parent"} {
		if e = os.Mkdir(filepath.Join(p.Work, name), 0700); e != nil {
			return out, e
		}
	}
	connectionPath := filepath.Join(p.Work, "capture-private", "connection.json")
	binding := namespaceinventory.Binding{Tenant: h.Tenant, Database: h.Database, StorageInstance: h.StorageInstance, ReplicaSet: h.ReplicaSet, RedisRunID: h.RedisRunID, RedisDB: 0}
	redisURL := url.URL{Scheme: "redis", Host: "127.0.0.1:6379", Path: "/0", User: url.UserPassword(c["redisAdminUser"], c["redisAdminPassword"])}
	if _, e = writePrivate(connectionPath, namespaceinventory.PrivateConnection{Version: 1, Binding: binding, MongoURI: mongoURI(c["mongoAdminUser"], c["mongoAdminPassword"], h.Database, false), RedisURL: redisURL.String()}); e != nil {
		return out, e
	}
	trustPath := filepath.Join(p.Work, "capture-private", "original-trust.json")
	trustSHA, e := writePrivate(trustPath, struct {
		Kind   string `json:"kind"`
		Target string `json:"target_header_sha256"`
		Epochs []any  `json:"epochs"`
	}{"reviewed-original-history-source-trust-v1", h.SHA256(), []any{}})
	if e != nil {
		return out, e
	}
	live, e := namespaceinventory.NewLiveSource(db, out.AdminRedis, binding, namespaceinventory.DefaultLimits())
	if e != nil {
		return out, e
	}
	launcher, e := ownedcontrol.OpenOwnedLauncher(filepath.Join(p.Work, "launcher"), h.SHA256())
	if e != nil {
		return out, e
	}
	kafka, e := ownedcontrol.NewKafkaProbe([]string{"127.0.0.1:9092"}, cluster, parts, c["kafkaAdminUser"], c["kafkaAdminPassword"])
	if e != nil {
		return out, e
	}
	out.Inputs = Inputs{Work: p.Work, Config: cfgImport, Database: db, Live: live, Launcher: launcher, Kafka: kafka, MongoExclusions: []ownedcontrol.MongoExclusion{{Identity: cfgImport.Credentials[0], Admin: out.AdminMongo, Legacy: out.LegacyMongo, Credential: ownedcontrol.MongoLegacyCredential{AuthDB: "admin", User: c["mongoLegacyUser"], UserIDHash: mongoVersion, AdminUser: c["mongoAdminUser"], AdminAuthDB: "admin"}}}, RedisExclusions: []ownedcontrol.RedisExclusion{{Identity: cfgImport.Credentials[1], Admin: out.AdminRedis, Legacy: out.LegacyRedis, AdminUser: c["redisAdminUser"], LegacyUser: c["redisLegacyUser"]}}, KafkaExclusions: []ownedcontrol.KafkaExclusion{{Identity: cfgImport.Credentials[2], Credential: ownedcontrol.KafkaLegacyCredential{User: c["kafkaLegacyUser"], Password: c["kafkaLegacyPassword"], PrincipalVersion: kafkaVersion}}}, LegacyWriters: out.Services.LegacyWriters, LegacySpecs: out.Services.LegacySpecs, ProbePrivateKey: probePrivate, Capture: ownedcontrol.HistoricalCaptureOptions{ConnectionPath: connectionPath, RawDirectory: filepath.Join(p.Work, "capture-raw-parent", "original"), DerivedDirectory: filepath.Join(p.Work, "capture-derived-parent", "derived"), PolicySHA256: h.DeploymentPolicySHA256, OriginalHistoryTrustPath: trustPath, OriginalHistoryTrustSHA256: trustSHA, CaptureAgent: cfgImport.CaptureAgent, CaptureKeyID: cfgImport.CaptureKeyID, CapturePublicKey: capturePublic, CapturePrivateKey: capturePrivate, PageBytes: cfgImport.PageBytes, PageRows: cfgImport.PageRows}, ReaderPorts: out.Services.ReaderPorts, Readers: out.Services.Readers, Requirements: out.Services.Requirements, Specs: out.Services.Specs, ReleaseKeyID: "fresh-owned-release", ReleasePrivateKey: releasePrivate}
	if p.Scenario == "reader-registered-cancel" {
		out.Inputs.StopAt = "registered-not-serving-before-readiness"
	}
	if p.Scenario == "reader-writer-fence-cancel" {
		out.Inputs.StopAt = "known-readiness-still-keeps-writer-fence"
	}
	return out, nil
}

func (b *Bootstrap) writeConfiguration(name string, legacy bool, redisAddress string, topics, groups []string) (string, error) {
	dir := filepath.Join(b.Plan.Work, name)
	if e := os.Mkdir(dir, 0700); e != nil {
		return "", e
	}
	c := b.Credential
	scope := "Runtime"
	if legacy {
		scope = "Legacy"
	}
	rpcPorts := map[string]any{}
	rpcNames := map[string]any{}
	fields := map[string]string{"rpc-user": "User", "rpc-friend": "Friend", "rpc-msg": "Message", "rpc-group": "Group", "rpc-auth": "Auth", "push": "Push", "rpc-conversation": "Conversation", "rpc-rtc": "Rtc", "rpc-third": "Third", "rpc-encryption": "Encryption", "msggateway": "MessageGateway"}
	for role, field := range fields {
		rpcPorts["openIm"+field+"Port"] = []int{b.Ports[role]}
		nameField := field
		if role == "rpc-msg" {
			nameField = "Msg"
		}
		rpcNames["openIm"+nameField+"Name"] = "fullgraph_" + b.Resource.Nonce + "_" + strings.ReplaceAll(role, "-", "_")
	}
	value := map[string]any{"envs": map[string]any{"discovery": "zookeeper"}, "zookeeper": map[string]any{"schema": "fullgraph_" + b.Resource.Nonce, "address": []string{"127.0.0.1:2181"}, "username": "", "password": ""}, "mongo": map[string]any{"uri": mongoURI(c["mongo"+scope+"User"], c["mongo"+scope+"Password"], b.Header.Database, false), "database": b.Header.Database, "maxPoolSize": 4}, "redis": map[string]any{"clusterMode": false, "address": []string{redisAddress}, "username": c["redis"+scope+"User"], "password": c["redis"+scope+"Password"], "enablePipeline": false}, "kafka": map[string]any{"username": c["kafka"+scope+"User"], "password": c["kafka"+scope+"Password"], "addr": []string{"127.0.0.1:9092"}, "producerAck": "all", "compressType": "none", "latestMsgToRedis": map[string]any{"topic": topics[0]}, "offlineMsgToMongo": map[string]any{"topic": topics[1]}, "msgToPush": map[string]any{"topic": topics[2]}, "consumerGroupID": map[string]any{"msgToRedis": groups[0], "msgToMongo": groups[1], "msgToPush": groups[2], "msgToMySql": "unused_fullgraph_" + b.Resource.Nonce}}, "rpc": map[string]any{"registerIP": "127.0.0.1", "listenIP": "127.0.0.1"}, "rpcPort": rpcPorts, "rpcRegisterName": rpcNames, "api": map[string]any{"openImApiPort": []int{b.Ports["api"]}, "listenIP": "127.0.0.1"}, "longConnSvr": map[string]any{"openImMessageGatewayPort": []int{b.Ports["msggateway"]}, "openImWsPort": []int{31001}, "websocketMaxConnNum": 8, "websocketMaxMsgLen": 1048576, "websocketTimeout": 30, "websocketWriteBufferSize": 4096, "isEncryption": false}, "object": map[string]any{"enable": "minio", "apiURL": "http://127.0.0.1:31000", "minio": map[string]any{"bucket": "fullgraph-" + b.Resource.Nonce, "endpoint": "http://127.0.0.1:9000", "signEndpoint": "http://127.0.0.1:9000", "accessKeyID": c["minioUser"], "secretAccessKey": c["minioSecret"], "publicRead": false}}, "push": map[string]any{"enable": "none", "maxConcurrentWorkers": 1}, "manager": map[string]any{"userID": []string{b.ManagerID}, "nickname": []string{"Synthetic fullgraph owner"}}, "im-admin": map[string]any{"userID": []string{b.ManagerID}, "nickname": []string{"Synthetic fullgraph owner"}}, "secret": c["serviceSecret"], "multiLoginPolicy": 1, "msgCacheTimeout": 60, "retainChatRecords": 365, "chatRecordsClearTime": "23:59", "msgDestructTime": "23:58", "tokenPolicy": map[string]any{"expire": 3600}, "singleMessageHasReadReceiptEnable": true, "groupMessageHasReadReceiptEnable": true, "prometheus": map[string]any{"enable": false}, "log": map[string]any{"storageLocation": dir, "rotationTime": 24, "remainRotationCount": 1, "remainLogLevel": 4, "isStdout": false, "isJson": true, "withStack": false}, "msg": map[string]any{"maxMsgLimit": 100, "maxSearchLimit": 100, "msgChannelTimeout": 10, "handshakeTimeout": 10, "listenIP": "127.0.0.1", "defaultReadTimeout": 10, "defaultWriteTimeout": 10, "readBufferSize": 4096, "writeBufferSize": 4096}}
	if _, e := writePrivate(filepath.Join(dir, "config.yaml"), value); e != nil {
		return "", e
	}
	if _, e := writePrivate(filepath.Join(dir, "notification.yaml"), map[string]any{}); e != nil {
		return "", e
	}
	return dir, nil
}

func (b *Bootstrap) Close(ctx context.Context) error {
	if b == nil || ctx == nil {
		return ownedcontrol.ErrOwnedEvidence
	}
	var e error
	if b.Proxy != nil {
		e = errors.Join(e, b.Proxy.Close(ctx))
	}
	for _, c := range []*redis.Client{b.LegacyRedis, b.RuntimeRedis, b.AdminRedis} {
		if c != nil {
			e = errors.Join(e, c.Close())
		}
	}
	for _, c := range []*mongo.Client{b.LegacyMongo, b.RuntimeMongo, b.AdminMongo} {
		if c != nil {
			e = errors.Join(e, c.Disconnect(ctx))
		}
	}
	return e
}
