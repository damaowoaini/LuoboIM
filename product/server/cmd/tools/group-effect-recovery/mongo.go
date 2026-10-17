package main

import (
	"context"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

func connectMongo(ctx context.Context, input operatorInput) (connection, error) {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(input.URI).SetDirect(false).SetRetryReads(false).SetRetryWrites(false).SetReadPreference(readpref.Primary()).SetServerSelectionTimeout(2*time.Second).SetConnectTimeout(2*time.Second))
	if err != nil {
		return connection{}, groupeffects.ErrUnavailable
	}
	c := connection{close: client.Disconnect}
	var hello struct {
		SetName  string `bson:"setName"`
		Writable bool   `bson:"isWritablePrimary"`
		Sessions *int64 `bson:"logicalSessionTimeoutMinutes"`
	}
	if client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello) != nil || hello.SetName != input.ReplicaSet || !hello.Writable || hello.Sessions == nil {
		return c, groupeffects.ErrUnavailable
	}
	s, err := groupeffects.NewStore(client.Database(input.Database))
	if err != nil {
		return c, err
	}
	if err := s.CheckReady(ctx); err != nil {
		return c, err
	}
	c.store = s
	return c, nil
}
