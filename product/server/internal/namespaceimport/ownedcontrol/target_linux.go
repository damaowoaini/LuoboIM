//go:build linux

package ownedcontrol

import (
	"context"
	"strconv"
	"strings"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func checkActualMongoTarget(ctx context.Context, client *mongo.Client, h namespaceimport.Header) error {
	if client == nil {
		return ErrOwnedEvidence
	}
	var hello bson.Raw
	if e := client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); e != nil {
		return e
	}
	set, ok := hello.Lookup("setName").StringValueOK()
	primary, pok := hello.Lookup("isWritablePrimary").BooleanOK()
	pid, iok := hello.Lookup("topologyVersion", "processId").ObjectIDOK()
	revision, rok := hello.Lookup("topologyVersion", "counter").Int64OK()
	if !ok || !pok || !primary || !iok || !rok || set != h.ReplicaSet || pid.Hex() != h.MongoProcess || revision != h.MongoTopology {
		return ErrOwnedEvidence
	}
	var marker bson.Raw
	if e := client.Database(h.Database).Collection("luobo_storage_identity").FindOne(ctx, bson.D{{Key: "_id", Value: "message-visibility-v1"}}).Decode(&marker); e != nil {
		return e
	}
	instance, sok := marker.Lookup("instance").StringValueOK()
	tenant, tok := marker.Lookup("tenant").StringValueOK()
	if !sok || !tok || instance != h.StorageInstance || tenant != h.Tenant {
		return ErrOwnedEvidence
	}
	return nil
}
func exactInfo(value, key string) (string, error) {
	found := ""
	seen := false
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, key+":") {
			if seen {
				return "", ErrOwnedEvidence
			}
			seen = true
			found = strings.TrimPrefix(line, key+":")
		}
	}
	if !seen || found == "" {
		return "", ErrOwnedEvidence
	}
	return found, nil
}
func clientDatabase(value string) (int, error) {
	found := -1
	for _, field := range strings.Fields(value) {
		if strings.HasPrefix(field, "db=") {
			if found >= 0 {
				return 0, ErrOwnedEvidence
			}
			number, e := strconv.Atoi(strings.TrimPrefix(field, "db="))
			if e != nil || number < 0 || number > 15 {
				return 0, ErrOwnedEvidence
			}
			found = number
		}
	}
	if found < 0 {
		return 0, ErrOwnedEvidence
	}
	return found, nil
}
func checkActualRedisTarget(ctx context.Context, client redis.UniversalClient, h namespaceimport.Header) error {
	if client == nil {
		return ErrOwnedEvidence
	}
	body, e := client.Info(ctx, "server").Result()
	if e != nil {
		return e
	}
	run, e := exactInfo(body, "run_id")
	if e != nil || run != h.RedisRunID {
		return ErrOwnedEvidence
	}
	body, e = client.Info(ctx, "cluster").Result()
	if e != nil {
		return e
	}
	cluster, e := exactInfo(body, "cluster_enabled")
	if e != nil || cluster != "0" {
		return ErrOwnedEvidence
	}
	info, e := client.Do(ctx, "CLIENT", "INFO").Text()
	if e != nil {
		return e
	}
	db, e := clientDatabase(info)
	if e != nil || db != h.RedisDB {
		return ErrOwnedEvidence
	}
	return nil
}
func (a *OwnedAgent) checkActualTargets(ctx context.Context) error {
	for _, v := range a.mongo {
		if e := checkActualMongoTarget(ctx, v.Admin, a.header); e != nil {
			return e
		}
	}
	for _, v := range a.redis {
		if e := checkActualRedisTarget(ctx, v.Admin, a.header); e != nil {
			return e
		}
	}
	return ctx.Err()
}
