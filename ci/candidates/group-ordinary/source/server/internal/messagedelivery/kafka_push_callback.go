package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

type KafkaPushCallbackInput struct{
	Kind,Binding,FailureMode,OperationID string
	Message *sdkws.MsgData
	Users []string
}

// This is a local, owned read for building the exact callback JSON. It is not
// an API response or a permission for a caller-provided URL/recipient list.
func (s *Store) KafkaPushCallbackInput(ctx context.Context,call OnlineCall)(KafkaPushCallbackInput,error){
	if err:=s.ValidateKafkaOnlineCall(ctx,call);err!=nil{return KafkaPushCallbackInput{},err}
	d,err:=s.readDutyRecord(ctx,call.BranchID);if err!=nil{return KafkaPushCallbackInput{},err}
	policy,err:=s.readKafkaPolicy(ctx,d.PolicySHA);if err!=nil{return KafkaPushCallbackInput{},err}
	kind,binding,mode,err:=kafkaRequiredOnlineCallback(d,policy,call.Message);if err!=nil{return KafkaPushCallbackInput{},err}
	return KafkaPushCallbackInput{Kind:kind,Binding:binding,FailureMode:mode,OperationID:d.SourceOperationID,Message:proto.Clone(call.Message).(*sdkws.MsgData),Users:append([]string(nil),d.Recipients...)},nil
}

type KafkaPushCallbackDispatch struct{ store *Store; row kafkaPushCallbackReceipt }

// BeginKafkaPushCallback returns dispatch authority only after a KNOWN strong
// commit of one stable request hash. Unknown claim ACK / process restart does
// not return a new capability or automatically repeat the original HTTP call.
// The real parent write fences a simultaneous clear, rather than relying on
// snapshot reads of one document followed by an unrelated child insertion.
func (s *Store) BeginKafkaPushCallback(ctx context.Context,call OnlineCall,bodySHA string)(*KafkaPushCallbackDispatch,error){
	if s==nil || ctx==nil || !validSHA(bodySHA) || mongo.SessionFromContext(ctx)!=nil{return nil,ErrInvalid}
	attempt,err:=uuid.NewRandom();if err!=nil{return nil,ErrUnprepared}
	var claim kafkaPushCallbackReceipt
	dispatch:=false
	err=s.transaction(ctx,func(tx context.Context)error{
		dispatch=false
		if err:=s.ValidateKafkaOnlineCall(tx,call);err!=nil{return err}
		d,err:=s.readDutyRecord(tx,call.BranchID);if err!=nil{return err}
		policy,err:=s.readKafkaPolicy(tx,d.PolicySHA);if err!=nil{return err}
		kind,binding,mode,err:=kafkaRequiredOnlineCallback(d,policy,call.Message);if err!=nil{return err}
		if binding==""{return nil}
		proof,err:=kafkaPushCallbackProof(d,kind,binding);if err!=nil{return err}
		id:=key(s.epoch,"kafka-push-callback",d.BranchID+"\x00"+kind)
		var old kafkaPushCallbackReceipt
		err=s.db.Collection(Effects).FindOne(tx,bson.M{"_id":id}).Decode(&old)
		if err==nil{
			if !validKafkaPushCallbackReceipt(old) || old.ID!=id || old.Kind!=kind || old.Epoch!=s.epoch || old.Branch!=d.BranchID || old.PolicySHA!=d.PolicySHA || old.Binding!=binding || old.Mode!=mode || old.RequestSHA!=proof || old.BodySHA!=bodySHA{return ErrConflict}
			switch old.State{
			case "acknowledged","continued-unknown","continued-not-applied":return nil
			case "rejected":return ErrKafkaPushCallbackRejected
			case "pending-confirmed","pending-not-dispatched": // Exact known non-application may restart this SAME request.
			default:return ErrKafkaPushCallbackUnconfirmed
			}
		}else if !errors.Is(err,mongo.ErrNoDocuments){return err}
		if d.CallbackRevision>=math.MaxInt64-1{return ErrConflict}
		filter:=bson.M{"_id":d.ID,"epoch":s.epoch,"revision":d.Revision,"online_claim":call.ClaimID,"hidden":false,"payload_sha256":d.PayloadSHA}
		if d.CallbackRevision==0{filter["$or"]=bson.A{bson.M{"callback_revision":int64(0)},bson.M{"callback_revision":bson.M{"$exists":false}}}}else{filter["callback_revision"]=d.CallbackRevision}
		fenced,err:=s.db.Collection(Effects).UpdateOne(tx,filter,bson.M{"$inc":bson.M{"callback_revision":int64(1)}})
		if err!=nil{return err};if fenced.MatchedCount!=1 || fenced.ModifiedCount!=1{return ErrConflict}
		claim=kafkaPushCallbackReceipt{ID:id,Kind:kind,Epoch:s.epoch,Branch:d.BranchID,PolicySHA:d.PolicySHA,Binding:binding,Mode:mode,RequestSHA:proof,BodySHA:bodySHA,State:"dispatching",Revision:1,AttemptID:attempt.String(),InstanceID:s.instanceID}
		if old.ID!=""{
			claim.Revision=old.Revision+1
			changed,err:=s.db.Collection(Effects).ReplaceOne(tx,bson.M{"_id":id,"state":old.State,"revision":old.Revision,"request_sha256":proof,"body_sha256":bodySHA},claim)
			if err!=nil{return err};if changed.MatchedCount!=1 || changed.ModifiedCount!=1{return ErrConflict}
		}else{if _,err:=s.db.Collection(Effects).InsertOne(tx,claim);err!=nil{return err}}
		dispatch=true;return nil
	})
	if err!=nil{return nil,err}
	if !dispatch{return nil,nil}
	return &KafkaPushCallbackDispatch{store:s,row:claim},nil
}

// Only the owned adapter calls this when it has not invoked HTTP.Do at all.
// It is separate from unknown transport results; losing this completion ACK
// still keeps the attempt unknown and never grants a blind resend.
func (d *KafkaPushCallbackDispatch) NotDispatched(parent context.Context)error{
	if d==nil || d.store==nil || parent==nil{return ErrInvalid}
	ctx,cancel:=context.WithTimeout(context.WithoutCancel(parent),5*time.Second);defer cancel()
	changed,err:=d.store.db.Collection(Effects).UpdateOne(ctx,bson.M{"_id":d.row.ID,"epoch":d.row.Epoch,"revision":d.row.Revision,"state":"dispatching","attempt_id":d.row.AttemptID,"instance_id":d.row.InstanceID,"body_sha256":d.row.BodySHA},bson.M{"$set":bson.M{"state":"pending-not-dispatched","outcome_sha256":hash([]byte("owned adapter did not invoke HTTP.Do"))},"$inc":bson.M{"revision":int64(1)}})
	if err!=nil || changed.MatchedCount!=1 || changed.ModifiedCount!=1{return ErrKafkaPushCallbackUnconfirmed};return nil
}

func (d *KafkaPushCallbackDispatch) BeforeHTTP(ctx context.Context,call OnlineCall,body []byte)error{
	if d==nil || d.store==nil || hash(body)!=d.row.BodySHA{return ErrInvalid}
	if err:=d.store.ValidateKafkaOnlineCall(ctx,call);err!=nil{return err}
	var row kafkaPushCallbackReceipt
	if err:=d.store.db.Collection(Effects).FindOne(ctx,bson.M{"_id":d.row.ID}).Decode(&row);err!=nil{return err}
	if row!=d.row || row.State!="dispatching"{return ErrConflict}
	return nil
}

// Outcome is a fixed code, never an error string or response body. A response
// hash proves only the exact observed outcome, not device delivery. Explicit
// failedContinue remains a durable continued-unknown decision; authentication
// or semantic rejection must use rejected and cannot be silently continued.
func (d *KafkaPushCallbackDispatch) Finish(parent context.Context,outcome,outcomeSHA string)error{
	if d==nil || d.store==nil || parent==nil || !validSHA(outcomeSHA){return ErrInvalid}
	state:=""
	switch outcome{case "acknowledged":state="acknowledged";case "rejected":state="rejected";case "unknown":state="unconfirmed";if d.row.Mode=="continue"{state="continued-unknown"};default:return ErrInvalid}
	ctx,cancel:=context.WithTimeout(context.WithoutCancel(parent),5*time.Second);defer cancel()
	changed,err:=d.store.db.Collection(Effects).UpdateOne(ctx,bson.M{"_id":d.row.ID,"epoch":d.row.Epoch,"revision":d.row.Revision,"state":"dispatching","attempt_id":d.row.AttemptID,"instance_id":d.row.InstanceID,"request_sha256":d.row.RequestSHA,"body_sha256":d.row.BodySHA},bson.M{"$set":bson.M{"state":state,"outcome_sha256":outcomeSHA},"$inc":bson.M{"revision":int64(1)}})
	if err!=nil{return ErrKafkaPushCallbackUnconfirmed}
	if changed.MatchedCount!=1 || changed.ModifiedCount!=1{return ErrKafkaPushCallbackUnconfirmed}
	if state=="unconfirmed"{return ErrKafkaPushCallbackUnconfirmed};if state=="rejected"{return ErrKafkaPushCallbackRejected};return nil
}

type KafkaPushCallbackSummary struct{
	BranchID,Kind,State,PolicySHA256,Binding,RequestSHA256,BodySHA256,OutcomeSHA256,AttemptID,InstanceID string
	Revision int64
}
func (s *Store) InspectKafkaPushCallback(ctx context.Context,branch,kind string)(KafkaPushCallbackSummary,error){
	if s==nil || ctx==nil || !validUUID(branch) || kind!="before-group-push" && kind!="online-push"{return KafkaPushCallbackSummary{},ErrInvalid}
	current,cancel:=context.WithTimeout(ctx,5*time.Second);defer cancel()
	if err:=s.CheckKafkaReady(current);err!=nil{return KafkaPushCallbackSummary{},err}
	var row kafkaPushCallbackReceipt;id:=key(s.epoch,"kafka-push-callback",branch+"\x00"+kind)
	if err:=s.db.Collection(Effects).FindOne(current,bson.M{"_id":id}).Decode(&row);err!=nil{if errors.Is(err,mongo.ErrNoDocuments){return KafkaPushCallbackSummary{},ErrCommitUnconfirmed};return KafkaPushCallbackSummary{},err}
	if !validKafkaPushCallbackReceipt(row) || row.ID!=id || row.Kind!=kind || row.Branch!=branch || row.Epoch!=s.epoch{return KafkaPushCallbackSummary{},ErrConflict}
	d,err:=s.readDutyRecord(current,branch);if err!=nil{return KafkaPushCallbackSummary{},err}
	policy,err:=s.readKafkaPolicy(current,d.PolicySHA);if err!=nil{return KafkaPushCallbackSummary{},err}
	binding,mode:=policy.OnlinePushBinding,policy.OnlineFailureMode
	if kind=="before-group-push"{binding,mode=policy.BeforeGroupPushBinding,policy.BeforeGroupFailureMode}
	proof,err:=kafkaPushCallbackProof(d,kind,binding)
	if err!=nil || binding=="" || row.PolicySHA!=d.PolicySHA || row.Binding!=binding || row.Mode!=mode || row.RequestSHA!=proof{return KafkaPushCallbackSummary{},ErrConflict}
	return KafkaPushCallbackSummary{BranchID:branch,Kind:kind,State:row.State,PolicySHA256:row.PolicySHA,Binding:row.Binding,RequestSHA256:row.RequestSHA,BodySHA256:row.BodySHA,OutcomeSHA256:row.OutcomeSHA,AttemptID:row.AttemptID,InstanceID:row.InstanceID,Revision:row.Revision},nil
}

// This operator-only local API is not registered on HTTP/gRPC. Independent
// evidence plus every exact minimal receipt field is mandatory; unknown ACK
// of this CAS is inspected, never automatically retried with a new identity.
func (s *Store) ConfirmKafkaPushCallback(ctx context.Context,expected KafkaPushCallbackSummary,decision,evidenceSHA string,drain ...KafkaAttemptDrainProof)error{
	if ctx==nil || !validSHA(evidenceSHA) || len(drain)!=1 || drain[0].InstanceID!=expected.InstanceID || !validSHA(drain[0].EvidenceSHA256){return ErrInvalid}
	current,cancel:=context.WithTimeout(ctx,5*time.Second);defer cancel()
	actual,err:=s.InspectKafkaPushCallback(current,expected.BranchID,expected.Kind);if err!=nil{return err}
	if actual!=expected || actual.State!="dispatching" && actual.State!="unconfirmed" && actual.State!="continued-unknown"{return ErrConflict}
	state:="";switch decision{case "applied":state="acknowledged";case "not-applied":state="pending-confirmed";case "rejected":state="rejected";default:return ErrInvalid}
	if actual.State=="continued-unknown" && decision=="not-applied"{state="continued-not-applied"}
	id:=key(s.epoch,"kafka-push-callback",actual.BranchID+"\x00"+actual.Kind)
	proof,_:=json.Marshal([]any{"operator-independent-evidence",decision,evidenceSHA,drain[0]})
	changed,err:=s.db.Collection(Effects).UpdateOne(current,bson.M{"_id":id,"epoch":s.epoch,"revision":actual.Revision,"state":actual.State,"attempt_id":actual.AttemptID,"instance_id":actual.InstanceID,"request_sha256":actual.RequestSHA256,"body_sha256":actual.BodySHA256,"policy_sha256":actual.PolicySHA256},bson.M{"$set":bson.M{"state":state,"outcome_sha256":hash(proof)},"$inc":bson.M{"revision":int64(1)}})
	if err!=nil || changed.MatchedCount!=1 || changed.ModifiedCount!=1{return ErrKafkaPushCallbackUnconfirmed};return nil
}
