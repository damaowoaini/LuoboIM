package messagedelivery

import (
 "context"
 "errors"
 "strings"
 "testing"

 "github.com/OpenIMSDK/protocol/sdkws"
 "github.com/google/uuid"
 table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
 "github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
 "go.mongodb.org/mongo-driver/bson"
 "go.mongodb.org/mongo-driver/event"
 "go.mongodb.org/mongo-driver/mongo"
 "google.golang.org/protobuf/proto"
)

type aliasFixtureTransport struct {
 store *Store
 policy string
 binding string
 perform func(context.Context,KafkaOfflineCall,string,*KafkaAliasDispatch)error
 beginCalls int
}
func (a *aliasFixtureTransport) PolicyFingerprint()string{return a.policy}
func (a *aliasFixtureTransport) CheckReady(ctx context.Context)error{return a.store.CheckKafkaReady(ctx)}
func (a *aliasFixtureTransport) DeliverOffline(ctx context.Context,call KafkaOfflineCall)error{
 var failures []error
 for _,user:=range call.DispatchRecipients{
  state,err:=a.store.ResolveKafkaAliasVendor(ctx,call,user);if err!=nil{failures=append(failures,err);continue}
  if aliasTerminal(state){continue}
  if state!="pending" && state!="not-dispatched" && state!="pending-confirmed"{failures=append(failures,ErrKafkaOfflineUnconfirmed);continue}
  a.beginCalls++
  request:=KafkaAliasRequest{Vendor:"getui",Binding:a.binding,Protocol:"getui-alias-v2",RequestID:strings.ReplaceAll(uuid.NewString(),"-",""),BodySHA256:hash([]byte("synthetic exact alias HTTP request"))}
  dispatch,err:=a.store.BeginKafkaAliasVendor(ctx,call,user,request)
  if err!=nil{failures=append(failures,err);continue};if dispatch==nil{continue}
  if err:=a.perform(ctx,call,user,dispatch);err!=nil{failures=append(failures,err)}
 }
 return errors.Join(failures...)
}
func aliasVendorFixture(t *testing.T,offset int64,monitors ...*event.CommandMonitor)(context.Context,*Store,*mongo.Database,string,*sdkws.MsgData,*aliasFixtureTransport){
 t.Helper();var monitor *event.CommandMonitor;if len(monitors)>0{monitor=monitors[0]};ctx,_,store,db,_:=kafkaFixture(t,monitor);record:=kafkaFixtureRecord(t,offset)
 var message sdkws.MsgData;if proto.Unmarshal(record.Body,&message)!=nil{t.Fatal("synthetic source")}
 message.Options=msgprocessor.WithOptions(message.Options,msgprocessor.WithOfflinePush(true));var err error
 record.Body,err=proto.MarshalOptions{Deterministic:true}.Marshal(&message);if err!=nil{t.Fatal(err)}
 policy,_:=kafkaPolicyFixture(t,store);policy.Vendor="getui";policy.VendorBinding=hash([]byte("synthetic owned getui app/channel material"))
 fingerprint,err:=policy.Fingerprint();if err!=nil{t.Fatal(err)}
 receipt,err:=store.AcceptKafkaRecord(ctx,record,fingerprint,policy);if err!=nil || len(receipt.Branches)!=1{t.Fatal("source not accepted",err)}
 branch:=receipt.Branches[0];message.Seq=branch.Seq
 if err:=store.RecoverKafkaOnline(ctx,branch.ID,&staleBodyReader{message:&message},discardProjection{},&kafkaOnlineFixtureTransport{policy:fingerprint});err!=nil{t.Fatal(err)}
 return ctx,store,db,branch.ID,&message,&aliasFixtureTransport{store:store,policy:fingerprint,binding:policy.VendorBinding}
}
func TestKafkaAliasMongoProviderAcceptanceSettlesOriginalRecipientWithoutInventingDevices(t *testing.T){
 ctx,store,db,branch,message,adapter:=aliasVendorFixture(t,71);physical:=0
 adapter.perform=func(current context.Context,call KafkaOfflineCall,user string,dispatch *KafkaAliasDispatch)error{
  if err:=dispatch.BeforeProvider(current,call,user,dispatch.row.Input.BodySHA256);err!=nil{t.Fatal(err)};physical++
  if err:=dispatch.BeforeProvider(current,call,user,dispatch.row.Input.BodySHA256);!errors.Is(err,ErrConflict){t.Fatal("same alias attempt allowed a second physical POST",err)}
  return dispatch.Finish(current,KafkaAliasReport{PhysicalAttemptStarted:true,ProviderAccepted:true,ProviderReceiptSHA256:hash([]byte("synthetic exact provider task/CID receipt")),OutcomeSHA256:hash([]byte("synthetic HTTP200 complete"))})
 }
 if err:=store.RecoverKafkaOffline(ctx,branch,&staleBodyReader{message:message},adapter);err!=nil{t.Fatal("known provider acceptance did not reach real page settlement",err)}
 if physical!=1 || adapter.beginCalls!=1{t.Fatal("recipient was sent more than once")}
 var row bson.M
 if err:=db.Collection(Effects).FindOne(ctx,bson.M{"kind":"kafka-alias-vendor-v1"}).Decode(&row);err!=nil || row["state"]!="alias-provider-accepted" || row["accounted"]!=true{t.Fatal("alias acceptance was not accounted as its own protocol",err)}
 for _,field:=range []string{"device_sha256","device_results","token","tokens","payload","alias","user_id"}{if _,ok:=row[field];ok{t.Fatal("alias receipt invented device proof or retained private content")}}
 var duty deliveryDuty
 if err:=db.Collection(Effects).FindOne(ctx,bson.M{"_id":key(store.epoch,"branch",branch)}).Decode(&duty);err!=nil || duty.OfflinePending || duty.RecipientCount!=2 || duty.OfflineSettled!=2{t.Fatal("existing real offline cursor did not consume alias outcome",err)}
}
func TestKafkaAliasMongoUnknownACKRestartCannotCreateAnotherAliasAttempt(t *testing.T){
 ctx,store,db,branch,message,adapter:=aliasVendorFixture(t,72);physical:=0
 adapter.perform=func(current context.Context,call KafkaOfflineCall,user string,dispatch *KafkaAliasDispatch)error{
  if err:=dispatch.BeforeProvider(current,call,user,dispatch.row.Input.BodySHA256);err!=nil{t.Fatal(err)};physical++
  return dispatch.Finish(current,KafkaAliasReport{PhysicalAttemptStarted:true,OutcomeSHA256:hash([]byte("synthetic lost provider ACK"))})
 }
 if err:=store.RecoverKafkaOffline(ctx,branch,&staleBodyReader{message:message},adapter);!errors.Is(err,ErrKafkaOfflineUnconfirmed){t.Fatal("lost ACK became provider acceptance",err)}
 restarted,err:=NewMembershipBoundStore(db,store.epoch,store.activationSHA);if err!=nil{t.Fatal(err)};adapter.store=restarted
 if err:=restarted.RecoverKafkaOffline(ctx,branch,&staleBodyReader{message:message},adapter);!errors.Is(err,ErrKafkaOfflineUnconfirmed){t.Fatal("restart inferred rollback",err)}
 if physical!=1 || adapter.beginCalls!=1{t.Fatal("unknown original alias POST was retried")}
 summary,err:=restarted.InspectKafkaAliasVendor(ctx,branch,"member");if err!=nil || summary.State!="unconfirmed" || summary.Protocol!="getui-alias-v2"{t.Fatal("original minimal alias status not inspectable",err)}
 proof:=KafkaAliasVendorConfirmation{Decision:"provider-accepted",EvidenceSHA256:hash([]byte("synthetic independent endpoint inspection")),ProviderReceiptSHA256:hash([]byte("synthetic original task acceptance")),Drain:KafkaAttemptDrainProof{InstanceID:summary.InstanceID,EvidenceSHA256:hash([]byte("synthetic original instance joined and pools closed"))}}
 stale:=summary;stale.Revision++
 if err:=restarted.ConfirmKafkaAliasVendor(ctx,"member",stale,proof);!errors.Is(err,ErrConflict){t.Fatal("stale alias summary granted authority",err)}
 if err:=restarted.ConfirmKafkaAliasVendor(ctx,"member",summary,proof);err!=nil{t.Fatal("independent exact original acceptance could not resolve",err)}
 if err:=restarted.RecoverKafkaOffline(ctx,branch,&staleBodyReader{message:message},adapter);err!=nil{t.Fatal("confirmed original alias outcome failed page settlement",err)}
 if physical!=1 || adapter.beginCalls!=1{t.Fatal("operator inspection caused another provider POST")}
 var count int64
 count,err=db.Collection(Effects).CountDocuments(ctx,bson.M{"kind":"kafka-alias-vendor-v1","state":"alias-provider-accepted","accounted":true});if err!=nil || count!=1{t.Fatal("exact confirmed receipt did not survive settlement",err)}
}
func TestKafkaAliasMongoClearBeforePhysicalPOSTErasesDutyAndDoesNotRebuildPayload(t *testing.T){
 ctx,store,db,branch,message,adapter:=aliasVendorFixture(t,73);physical:=0
 adapter.perform=func(current context.Context,call KafkaOfflineCall,user string,dispatch *KafkaAliasDispatch)error{
  if err:=store.MutateMessageVisibility(current,db,[]table.PendingVisibilityChange{{ConversationID:"sg_g",Below:2}},func(tx context.Context)error{
   changed,err:=db.Collection("msg").UpdateOne(tx,bson.M{"doc_id":"sg_g:0"},bson.M{"$set":bson.M{"msgs.0.msg":nil,"msgs.0.global_deleted":true}})
   if err!=nil{return err};if changed.MatchedCount!=1 || changed.ModifiedCount!=1{return ErrConflict};return nil
  });err!=nil{t.Fatal(err)}
  if err:=dispatch.BeforeProvider(current,call,user,dispatch.row.Input.BodySHA256);err==nil{physical++}
  if err:=dispatch.NotDispatched(current);err!=nil{t.Fatal("zero physical invocation evidence not retained",err)}
  return ErrKafkaOfflineUnconfirmed
 }
 if err:=store.RecoverKafkaOffline(ctx,branch,&staleBodyReader{message:message},adapter);err==nil || physical!=0{t.Fatal("cleared private plaintext reached provider",err)}
 var duty deliveryDuty
 if err:=db.Collection(Effects).FindOne(ctx,bson.M{"_id":key(store.epoch,"branch",branch)}).Decode(&duty);err!=nil || !duty.Hidden || len(duty.Payload)!=0 || len(duty.Recipients)!=0{t.Fatal("clear rebuilt sensitive pending duty",err)}
 var receipt kafkaAliasReceipt
 if err:=db.Collection(Effects).FindOne(ctx,bson.M{"kind":"kafka-alias-vendor-v1"}).Decode(&receipt);err!=nil || receipt.State!="not-dispatched"{t.Fatal("minimal old attempt receipt missing after redaction",err)}
}
