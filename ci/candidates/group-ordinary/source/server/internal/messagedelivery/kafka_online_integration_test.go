package messagedelivery

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"go.mongodb.org/mongo-driver/bson"
	"google.golang.org/protobuf/proto"
)

type kafkaOnlineFixtureTransport struct{
	policy string
	calls []OnlineCall
	unknown bool
}
func (t *kafkaOnlineFixtureTransport) PolicyFingerprint()string{return t.policy}
func (t *kafkaOnlineFixtureTransport) Deliver(_ context.Context,call OnlineCall)(OnlineReport,error){
	t.calls=append(t.calls,call)
	if t.unknown{return OnlineReport{DispatchStarted:true},errors.New("synthetic lost gateway response")}
	report:=OnlineReport{DispatchStarted:true}
	for _,uid:=range call.Recipients{
		report.ACKs=append(report.ACKs,OnlineACK{UserID:uid,ResponseSHA256:hash([]byte("synthetic matching gateway ACK:"+uid))})
		report.Routes=append(report.Routes,OnlineRouteResult{UserID:uid,ResponseSHA256:hash([]byte("synthetic matching routing row:"+uid)),OnlineAccepted:uid=="owner"})
	}
	return report,nil
}

func TestKafkaOnlineMongoPolicyWorkerKeepsKnownOfflineRoutingSeparateFromLostResponse(t *testing.T){
	for _,unknown:=range []bool{false,true}{
		t.Run(map[bool]string{false:"known-routing",true:"unknown-routing"}[unknown],func(t *testing.T){
			ctx,_,s,db,_:=kafkaFixture(t,nil);record:=kafkaFixtureRecord(t,8);snapshot,policy:=kafkaPolicyFixture(t,s)
			var message sdkws.MsgData;if proto.Unmarshal(record.Body,&message)!=nil{t.Fatal("fixture")}
			message.Options=msgprocessor.WithOptions(message.Options,msgprocessor.WithOfflinePush(true))
			var err error;record.Body,err=proto.MarshalOptions{Deterministic:true}.Marshal(&message);if err!=nil{t.Fatal(err)}
			r,err:=s.AcceptKafkaRecord(ctx,record,policy,snapshot);if err!=nil{t.Fatal(err)}
			message.Seq=r.Branches[0].Seq
			transport:=&kafkaOnlineFixtureTransport{policy:policy,unknown:unknown}
			err=s.RecoverKafkaOnline(ctx,r.Branches[0].ID,&staleBodyReader{message:&message},discardProjection{},transport)
			if unknown{if !errors.Is(err,ErrOnlineUnconfirmed){t.Fatal("lost response called complete",err)}}else if err!=nil{t.Fatal(err)}
			if len(transport.calls)!=1 || transport.calls[0].PolicySHA256!=policy || !validUUID(transport.calls[0].ClaimID) || transport.calls[0].ClaimRevision<1{t.Fatal("actual worker transport lost frozen policy/claim")}
			var duty deliveryDuty;if err:=db.Collection(Effects).FindOne(ctx,bson.M{"branch_id":r.Branches[0].ID}).Decode(&duty);err!=nil{t.Fatal(err)}
			if !duty.OfflinePending{t.Fatal("online routing was mistaken for completion of offline vendor duty")}
			if unknown{
				if len(duty.OfflineRouting)!=0 || !duty.OnlinePending{t.Fatal("unknown gateway response manufactured an offline/online result")}
			}else{
				if len(duty.OfflineRouting)!=2 || !duty.OfflineRouting[hash([]byte("member"))].Required || duty.OfflineRouting[hash([]byte("owner"))].Required || duty.OnlinePending{t.Fatal("known per-recipient routing did not survive acknowledged page completion")}
			}
			if _,err:=s.readDuty(ctx,r.Branches[0].ID);!errors.Is(err,ErrUnprepared){t.Fatal("ordinary recovery context escaped to the old dispatcher",err)}
		})
	}
}

func TestKafkaOnlineMongoRejectsWrongPolicyAndFinalVisibilityLossBeforeTransport(t *testing.T){
	ctx,_,s,db,_:=kafkaFixture(t,nil);record:=kafkaFixtureRecord(t,9);snapshot,policy:=kafkaPolicyFixture(t,s)
	r,err:=s.AcceptKafkaRecord(ctx,record,policy,snapshot);if err!=nil{t.Fatal(err)}
	var message sdkws.MsgData;if proto.Unmarshal(record.Body,&message)!=nil{t.Fatal("fixture")};message.Seq=r.Branches[0].Seq
	wrong:=&kafkaOnlineFixtureTransport{policy:hash([]byte("foreign runtime config"))}
	if err:=s.RecoverKafkaOnline(ctx,r.Branches[0].ID,&staleBodyReader{message:&message},discardProjection{},wrong);!errors.Is(err,ErrUnprepared) || len(wrong.calls)!=0{t.Fatal("worker borrowed another policy transport",err)}
	transport:=&kafkaOnlineFixtureTransport{policy:policy}
	reader:=&staleBodyReader{message:&message,beforeReply:func()error{
		return s.MutateMessageVisibility(ctx,db,[]table.PendingVisibilityChange{{ConversationID:"sg_g",Below:2}},func(tx context.Context)error{
			_,err:=db.Collection("msg").UpdateOne(tx,bson.M{"doc_id":"sg_g:0"},bson.M{"$set":bson.M{"msgs.0.msg":nil,"msgs.0.global_deleted":true}});return err
		})
	}}
	if err:=s.RecoverKafkaOnline(ctx,r.Branches[0].ID,reader,discardProjection{},transport);err==nil || len(transport.calls)!=0{t.Fatal("old body/callback reached transport after authoritative clear",err)}
}
