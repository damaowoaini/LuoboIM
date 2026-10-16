package main

import (
	"context"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

// No endpoint on this interface can send a vendor request or mint a token.
// The production constructor checks the exact catalog/epoch before returning.
type aliasBackend interface {
	Inspect(context.Context,string,string)(messagedelivery.KafkaAliasVendorSummary,error)
	Confirm(context.Context,string,messagedelivery.KafkaAliasVendorSummary,messagedelivery.KafkaAliasVendorConfirmation)error
	Close(context.Context)error
}
type aliasBackendFactory func(context.Context,operatorInput)(aliasBackend,error)
type mongoAliasBackend struct {client *mongo.Client;store *messagedelivery.Store}

func openMongoBackend(ctx context.Context,input operatorInput)(aliasBackend,error){
	client,err:=mongo.Connect(ctx,options.Client().ApplyURI(input.URI).SetDirect(false).SetReplicaSet(input.ReplicaSet).SetReadPreference(readpref.Primary()).SetReadConcern(readconcern.Majority()).SetWriteConcern(writeconcern.New(writeconcern.WMajority(),writeconcern.J(true))).SetRetryReads(false).SetRetryWrites(false).SetConnectTimeout(5*time.Second).SetServerSelectionTimeout(5*time.Second).SetSocketTimeout(5*time.Second))
	if err!=nil{return nil,errInput}
	ok:=false
	defer func(){if !ok{end,cancel:=context.WithTimeout(context.Background(),3*time.Second);defer cancel();_ = client.Disconnect(end)}}()
	var hello struct{SetName string `bson:"setName"`;Primary bool `bson:"isWritablePrimary"`;Sessions *int64 `bson:"logicalSessionTimeoutMinutes"`}
	if client.Database("admin").RunCommand(ctx,bson.D{{Key:"hello",Value:1}},options.RunCmd().SetReadPreference(readpref.Primary())).Decode(&hello)!=nil || hello.SetName!=input.ReplicaSet || !hello.Primary || hello.Sessions==nil || *hello.Sessions<=0{return nil,errInput}
	store,err:=messagedelivery.NewMembershipBoundStore(client.Database(input.Database),input.Epoch,input.Activation)
	if err!=nil || store.CheckKafkaReady(ctx)!=nil{return nil,errInput}
	ok=true;return &mongoAliasBackend{client:client,store:store},nil
}
func (b *mongoAliasBackend) Inspect(ctx context.Context,branch,user string)(messagedelivery.KafkaAliasVendorSummary,error){return b.store.InspectKafkaAliasVendor(ctx,branch,user)}
func (b *mongoAliasBackend) Confirm(ctx context.Context,user string,expected messagedelivery.KafkaAliasVendorSummary,proof messagedelivery.KafkaAliasVendorConfirmation)error{return b.store.ConfirmKafkaAliasVendor(ctx,user,expected,proof)}
func (b *mongoAliasBackend) Close(ctx context.Context)error{return b.client.Disconnect(ctx)}
