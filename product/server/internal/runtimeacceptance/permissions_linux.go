//go:build linux

package runtimeacceptance

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/redis/go-redis/v9"
)

// This is evidence from the fresh fixture's real business credentials, never
// a permit for production startup. No user, password, key, topic, raw reply or
// error text is retained in this finite result.
type BusinessPermissionEvidence struct {
	Version    string                          `json:"version"`
	Principals []BusinessPermissionObservation `json:"principals"`
}
type BusinessPermissionObservation struct {
	Role                   string `json:"role"`
	RedisWhoAmI            bool   `json:"redisWhoami"`
	RedisClientInfo        bool   `json:"redisClientInfo"`
	RedisACLGetUserError   string `json:"redisAclGetUserError"`
	RedisConfigGetError    string `json:"redisConfigGetError"`
	RedisForeignKeyError   string `json:"redisForeignKeyError"`
	KafkaOriginalTopics    bool   `json:"kafkaOriginalTopics"`
	KafkaACLDescribeError  int16  `json:"kafkaAclDescribeError"`
	KafkaForeignTopicError int16  `json:"kafkaForeignTopicError"`
	KafkaConnectionClosed  bool   `json:"kafkaConnectionClosed"`
}

func checkBusinessPermissions(ctx context.Context, b *Bootstrap, cluster string, topics []string) (BusinessPermissionEvidence, error) {
	evidence := BusinessPermissionEvidence{Version: "owned-fullgraph-business-permissions-v1", Principals: []BusinessPermissionObservation{}}
	if ctx == nil || ctx.Done() == nil || b == nil || cluster == "" || cluster != b.Resource.KafkaClusterID || len(b.Resource.Nonce) != 32 || strings.Trim(b.Resource.Nonce, "0123456789abcdef") != "" || len(topics) != 3 {
		return evidence, ownedcontrol.ErrOwnedEvidence
	}
	for i, suffix := range []string{"allocation", "persistence", "push"} {
		if topics[i] != "fullgraph_"+b.Resource.Nonce+"_"+suffix {
			return evidence, ownedcontrol.ErrOwnedEvidence
		}
	}
	// These exact names are outside R2's nine permitted Redis keys and three
	// permitted Kafka topics. They are queried read-only and never created.
	foreignKey := "fullgraph_forbidden:" + b.Resource.Nonce
	foreignTopic := "fullgraph_forbidden_" + b.Resource.Nonce
	for _, role := range []string{"legacy", "runtime"} {
		scope, pool := "Legacy", b.LegacyRedis
		if role == "runtime" {
			scope, pool = "Runtime", b.RuntimeRedis
		}
		row, err := checkBusinessPrincipal(ctx, role, pool, b.Credential["redis"+scope+"User"], b.Credential["kafka"+scope+"User"], b.Credential["kafka"+scope+"Password"], cluster, topics, foreignKey, foreignTopic)
		evidence.Principals = append(evidence.Principals, row)
		if err != nil {
			return evidence, err
		}
	}
	if err := ctx.Err(); err != nil {
		return evidence, err
	}
	return evidence, nil
}

func checkBusinessPrincipal(parent context.Context, role string, pool *redis.Client, redisUser, kafkaUser, kafkaPassword, cluster string, topics []string, foreignKey, foreignTopic string) (row BusinessPermissionObservation, probeErr error) {
	row.Role = role
	if parent == nil || parent.Done() == nil || pool == nil || redisUser == "" || kafkaUser == "" || kafkaPassword == "" {
		return row, ownedcontrol.ErrOwnedEvidence
	}
	ctx, stop := context.WithTimeout(parent, 30*time.Second)
	defer stop()
	if err := ctx.Err(); err != nil {
		return row, err
	}
	who, err := pool.Do(ctx, "ACL", "WHOAMI").Text()
	if err != nil || ctx.Err() != nil || who != redisUser {
		return row, ownedcontrol.ErrOwnedEvidence
	}
	row.RedisWhoAmI = true
	if err = ctx.Err(); err != nil {
		return row, err
	}
	info, err := pool.Do(ctx, "CLIENT", "INFO").Text()
	if err != nil || ctx.Err() != nil || !businessRedisClientInfo(info, redisUser) {
		return row, ownedcontrol.ErrOwnedEvidence
	}
	row.RedisClientInfo = true
	for i, args := range [][]any{{"ACL", "GETUSER", redisUser}, {"CONFIG", "GET", "timeout"}, {"GET", foreignKey}} {
		if err = ctx.Err(); err != nil {
			return row, err
		}
		// A network error, nil reply, missing key or auth failure is not a denial.
		// HasErrorPrefix first requires a genuine Redis protocol Error. The space
		// makes NOPERM an exact code, not a longer code with the same prefix.
		err = pool.Do(ctx, args...).Err()
		if ctx.Err() != nil || !redis.HasErrorPrefix(err, "NOPERM ") {
			return row, ownedcontrol.ErrOwnedEvidence
		}
		switch i {
		case 0:
			row.RedisACLGetUserError = "NOPERM"
		case 1:
			row.RedisConfigGetError = "NOPERM"
		case 2:
			row.RedisForeignKeyError = "NOPERM"
		}
	}
	if err = ctx.Err(); err != nil {
		return row, err
	}
	// A single directly owned broker avoids ClusterAdmin.ListAcls, which in the
	// locked Sarama version ignores response.Err. It also permits synchronous
	// Broker.Close error/join observation instead of Client's async broker closes.
	broker := sarama.NewBroker("127.0.0.1:9092")
	defer func() {
		if err := broker.Close(); err != nil {
			probeErr = errors.Join(probeErr, ownedcontrol.ErrOwnedEvidence)
		} else {
			row.KafkaConnectionClosed = true
		}
		if err := ctx.Err(); err != nil {
			probeErr = errors.Join(probeErr, err)
		}
	}()
	config := freshKafkaConfig(kafkaUser, kafkaPassword)
	// All three request versions below are explicit for the pinned Kafka server;
	// do not leave Open's fire-and-forget ApiVersions goroutine outside our join.
	config.ApiVersionsRequest = false
	if err = broker.Open(config); err != nil {
		return row, ownedcontrol.ErrOwnedEvidence
	}
	connected, err := broker.Connected()
	if err != nil || !connected || ctx.Err() != nil {
		return row, ownedcontrol.ErrOwnedEvidence
	}
	metadata, err := broker.GetMetadata(&sarama.MetadataRequest{Version: 7, Topics: append([]string(nil), topics...), AllowAutoTopicCreation: false})
	if err != nil || ctx.Err() != nil || metadata == nil || metadata.ClusterID == nil || *metadata.ClusterID != cluster || len(metadata.Topics) != len(topics) {
		return row, ownedcontrol.ErrOwnedEvidence
	}
	expected := make(map[string]bool, len(topics))
	for _, topic := range topics {
		expected[topic] = true
	}
	for _, topic := range metadata.Topics {
		if topic == nil || !expected[topic.Name] || topic.Err != sarama.ErrNoError || len(topic.Partitions) != 1 || topic.Partitions[0] == nil || topic.Partitions[0].ID != 0 || topic.Partitions[0].Err != sarama.ErrNoError {
			return row, ownedcontrol.ErrOwnedEvidence
		}
		delete(expected, topic.Name)
	}
	if len(expected) != 0 {
		return row, ownedcontrol.ErrOwnedEvidence
	}
	row.KafkaOriginalTopics = true
	if err = ctx.Err(); err != nil {
		return row, err
	}
	resource, principal, host := "kafka-cluster", "User:"+kafkaUser, "127.0.0.1"
	acls, err := broker.DescribeAcls(&sarama.DescribeAclsRequest{Version: 1, AclFilter: sarama.AclFilter{Version: 1, ResourceType: sarama.AclResourceCluster, ResourceName: &resource, ResourcePatternTypeFilter: sarama.AclPatternLiteral, Principal: &principal, Host: &host, Operation: sarama.AclOperationAny, PermissionType: sarama.AclPermissionAny}})
	if err != nil || ctx.Err() != nil || acls == nil || acls.Err != sarama.ErrClusterAuthorizationFailed || len(acls.ResourceAcls) != 0 {
		return row, ownedcontrol.ErrOwnedEvidence
	}
	row.KafkaACLDescribeError = int16(acls.Err)
	if err = ctx.Err(); err != nil {
		return row, err
	}
	denied, err := broker.GetMetadata(&sarama.MetadataRequest{Version: 7, Topics: []string{foreignTopic}, AllowAutoTopicCreation: false})
	if err != nil || ctx.Err() != nil || denied == nil || denied.ClusterID == nil || *denied.ClusterID != cluster || len(denied.Topics) != 1 {
		return row, ownedcontrol.ErrOwnedEvidence
	}
	topic := denied.Topics[0]
	if topic == nil || topic.Name != foreignTopic || topic.Err != sarama.ErrTopicAuthorizationFailed || len(topic.Partitions) != 0 {
		return row, ownedcontrol.ErrOwnedEvidence
	}
	row.KafkaForeignTopicError = int16(topic.Err)
	if err = ctx.Err(); err != nil {
		return row, err
	}
	return row, nil
}

func businessRedisClientInfo(info, user string) bool {
	if len(info) == 0 || len(info) > 8192 || strings.ContainsRune(info, '\x00') {
		return false
	}
	users, databases := 0, 0
	for _, field := range strings.Fields(info) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "user":
			users++
			if value != user {
				return false
			}
		case "db":
			databases++
			if value != "0" {
				return false
			}
		}
	}
	return users == 1 && databases == 1
}
