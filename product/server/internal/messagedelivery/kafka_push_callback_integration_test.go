package messagedelivery

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenIMSDK/protocol/sdkws"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"go.mongodb.org/mongo-driver/bson"
	"google.golang.org/protobuf/proto"
)

// This transport tests the real Mongo admission/claim/decision path with a
// synthetic external outcome. It does not claim an HTTP server/device ran.
type kafkaCallbackFixtureTransport struct{
	store *Store
	policy,outcome string
	callbackCalls,gatewayCalls int
	beforeHTTP func(context.Context,OnlineCall)error
}
func (t *kafkaCallbackFixtureTransport) PolicyFingerprint()string{return t.policy}
func (t *kafkaCallbackFixtureTransport) Deliver(ctx context.Context,call OnlineCall)(OnlineReport,error){
	input,err:=t.store.KafkaPushCallbackInput(ctx,call)
	if err!=nil || input.Message==nil || input.Binding==""{return OnlineReport{},errors.Join(ErrConflict,err)}
	body:=[]byte("synthetic exact frozen callback body")
	dispatch,err:=t.store.BeginKafkaPushCallback(ctx,call,hash(body));if err!=nil{return OnlineReport{},err}
	if dispatch!=nil{
		if t.beforeHTTP!=nil{if err:=t.beforeHTTP(ctx,call);err!=nil{return OnlineReport{},err}}
		if err:=dispatch.BeforeHTTP(ctx,call,body);err!=nil{
			if dispatch.NotDispatched(ctx)!=nil{return OnlineReport{},ErrKafkaPushCallbackUnconfirmed};return OnlineReport{},err
		}
		t.callbackCalls++
		if err:=dispatch.Finish(ctx,t.outcome,hash([]byte("synthetic observed outcome:"+t.outcome)));err!=nil{return OnlineReport{},err}
	}
	if err:=t.store.RequireKafkaPushCallbacks(ctx,call);err!=nil{return OnlineReport{},err}
	t.gatewayCalls++
	report:=OnlineReport{DispatchStarted:true}
	for _,uid:=range call.Recipients{
		report.ACKs=append(report.ACKs,OnlineACK{UserID:uid,ResponseSHA256:hash([]byte("synthetic exact gateway response:"+uid))})
		report.Routes=append(report.Routes,OnlineRouteResult{UserID:uid,ResponseSHA256:hash([]byte("synthetic exact route:"+uid)),OnlineAccepted:true})
	}
	return report,nil
}

func TestKafkaPushCallbackMongoKnownDecisionAndFailedContinueGateActualRecovery(t *testing.T){
	for _,test:=range []struct{name,mode,outcome,state string;admit bool}{
		{"acknowledged","default-stop","acknowledged","acknowledged",true},
		{"unknown-default-stop","default-stop","unknown","unconfirmed",false},
		{"unknown-explicit-stop","explicit-stop","unknown","unconfirmed",false},
		{"unknown-explicit-continue","continue","unknown","continued-unknown",true},
		{"rejected-never-continues","continue","rejected","rejected",false},
	}{t.Run(test.name,func(t *testing.T){
		ctx,_,s,db,_:=kafkaFixture(t,nil);record:=kafkaFixtureRecord(t,31)
		snapshot,_:=kafkaPolicyFixture(t,s);snapshot.BeforeGroupPushBinding=hash([]byte("synthetic owned callback config"));snapshot.BeforeGroupFailureMode=test.mode
		policy,err:=snapshot.Fingerprint();if err!=nil{t.Fatal(err)}
		r,err:=s.AcceptKafkaRecord(ctx,record,policy,snapshot);if err!=nil{t.Fatal(err)}
		var message sdkws.MsgData;if proto.Unmarshal(record.Body,&message)!=nil{t.Fatal("fixture")};message.Seq=r.Branches[0].Seq
		transport:=&kafkaCallbackFixtureTransport{store:s,policy:policy,outcome:test.outcome}
		err=s.RecoverKafkaOnline(ctx,r.Branches[0].ID,&staleBodyReader{message:&message},discardProjection{},transport)
		if (err==nil)!=test.admit || transport.callbackCalls!=1 || transport.gatewayCalls!=map[bool]int{true:1,false:0}[test.admit]{t.Fatal("callback outcome did not gate actual gateway entry",err,transport.callbackCalls,transport.gatewayCalls)}
		summary,err:=s.InspectKafkaPushCallback(ctx,r.Branches[0].ID,"before-group-push")
		if err!=nil || summary.State!=test.state || !validSHA(summary.OutcomeSHA256) || summary.PolicySHA256!=policy{t.Fatal("minimal exact callback receipt absent",err)}
		var raw bson.M
		if err:=db.Collection(Effects).FindOne(ctx,bson.M{"_id":key(s.epoch,"kafka-push-callback",r.Branches[0].ID+"\x00before-group-push")}).Decode(&raw);err!=nil{t.Fatal(err)}
		for _,forbidden:=range []string{"payload","body","users","recipients","message","secret","url"}{if _,ok:=raw[forbidden];ok{t.Fatal("callback child retained sensitive dispatch data")}}
	})}
}

func TestKafkaPushCallbackMongoUnknownRestartNeedsExactInspectionAndConfirmationBeforeHTTP(t *testing.T){
	ctx,_,s,db,_:=kafkaFixture(t,nil);record:=kafkaFixtureRecord(t,32)
	snapshot,_:=kafkaPolicyFixture(t,s);snapshot.BeforeGroupPushBinding=hash([]byte("synthetic owned callback config"));snapshot.BeforeGroupFailureMode="default-stop"
	policy,err:=snapshot.Fingerprint();if err!=nil{t.Fatal(err)}
	r,err:=s.AcceptKafkaRecord(ctx,record,policy,snapshot);if err!=nil{t.Fatal(err)}
	var message sdkws.MsgData;if proto.Unmarshal(record.Body,&message)!=nil{t.Fatal("fixture")};message.Seq=r.Branches[0].Seq
	first:=&kafkaCallbackFixtureTransport{store:s,policy:policy,outcome:"unknown"}
	if err:=s.RecoverKafkaOnline(ctx,r.Branches[0].ID,&staleBodyReader{message:&message},discardProjection{},first);!errors.Is(err,ErrKafkaPushCallbackUnconfirmed) || first.callbackCalls!=1 || first.gatewayCalls!=0{t.Fatal("first lost response was called applied",err)}
	restarted,err:=NewMembershipBoundStore(db,s.epoch,s.activationSHA);if err!=nil{t.Fatal(err)}
	second:=&kafkaCallbackFixtureTransport{store:restarted,policy:policy,outcome:"acknowledged"}
	if err:=restarted.RecoverKafkaOnline(ctx,r.Branches[0].ID,&staleBodyReader{message:&message},discardProjection{},second);!errors.Is(err,ErrKafkaPushCallbackUnconfirmed) || second.callbackCalls!=0 || second.gatewayCalls!=0{t.Fatal("restart retried an unknown external call",err)}
	expected,err:=restarted.InspectKafkaPushCallback(ctx,r.Branches[0].ID,"before-group-push");if err!=nil{t.Fatal(err)}
	stale:=expected;stale.Revision++
	drain:=KafkaAttemptDrainProof{InstanceID:expected.InstanceID,EvidenceSHA256:hash([]byte("synthetic fixture call returned; no external operation remains"))}
	if err:=restarted.ConfirmKafkaPushCallback(ctx,stale,"not-applied",hash([]byte("independent synthetic endpoint evidence")),drain);!errors.Is(err,ErrConflict){t.Fatal("stale receipt gained resend permission",err)}
	if err:=restarted.ConfirmKafkaPushCallback(ctx,expected,"not-applied",hash([]byte("independent synthetic endpoint evidence")),drain);err!=nil{t.Fatal(err)}
	if err:=restarted.RecoverKafkaOnline(ctx,r.Branches[0].ID,&staleBodyReader{message:&message},discardProjection{},second);err!=nil || second.callbackCalls!=1 || second.gatewayCalls!=1{t.Fatal("exact confirmed non-application could not resume the original request",err)}
	final,err:=restarted.InspectKafkaPushCallback(ctx,r.Branches[0].ID,"before-group-push")
	if err!=nil || final.State!="acknowledged" || final.RequestSHA256!=expected.RequestSHA256 || final.BodySHA256!=expected.BodySHA256 || final.AttemptID==expected.AttemptID{t.Fatal("confirmation changed source request or failed to create a distinct authorized attempt",err)}
}

func TestKafkaPushCallbackMongoClearAfterClaimPreventsHTTPAndKeepsOnlyMinimalReceipt(t *testing.T){
	ctx,_,s,db,_:=kafkaFixture(t,nil);record:=kafkaFixtureRecord(t,33)
	snapshot,_:=kafkaPolicyFixture(t,s);snapshot.BeforeGroupPushBinding=hash([]byte("synthetic owned callback config"));snapshot.BeforeGroupFailureMode="default-stop"
	policy,err:=snapshot.Fingerprint();if err!=nil{t.Fatal(err)}
	r,err:=s.AcceptKafkaRecord(ctx,record,policy,snapshot);if err!=nil{t.Fatal(err)}
	var message sdkws.MsgData;if proto.Unmarshal(record.Body,&message)!=nil{t.Fatal("fixture")};message.Seq=r.Branches[0].Seq
	transport:=&kafkaCallbackFixtureTransport{store:s,policy:policy,outcome:"acknowledged",beforeHTTP:func(current context.Context,_ OnlineCall)error{
		return s.MutateMessageVisibility(current,db,[]table.PendingVisibilityChange{{ConversationID:"sg_g",Below:2}},func(tx context.Context)error{
			changed,err:=db.Collection("msg").UpdateOne(tx,bson.M{"doc_id":"sg_g:0"},bson.M{"$set":bson.M{"msgs.0.msg":nil,"msgs.0.global_deleted":true}})
			if err!=nil{return err};if changed.MatchedCount!=1{return ErrConflict};return nil
		})
	}}
	if err:=s.RecoverKafkaOnline(ctx,r.Branches[0].ID,&staleBodyReader{message:&message},discardProjection{},transport);err==nil || transport.callbackCalls!=0 || transport.gatewayCalls!=0{t.Fatal("clear did not fence the accepted callback claim",err)}
	minimal,err:=s.InspectKafkaPushCallback(ctx,r.Branches[0].ID,"before-group-push")
	if err!=nil || minimal.State!="pending-not-dispatched"{t.Fatal("known zero-dispatch was lost or plaintext was required for inspection",err)}
	var duty deliveryDuty
	if err:=db.Collection(Effects).FindOne(ctx,bson.M{"_id":key(s.epoch,"branch",r.Branches[0].ID)}).Decode(&duty);err!=nil || !duty.Hidden || len(duty.Payload)!=0 || len(duty.Recipients)!=0{t.Fatal("callback claim resurrected the cleared body/roster",err)}
}
