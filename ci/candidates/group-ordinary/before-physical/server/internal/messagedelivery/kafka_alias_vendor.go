package messagedelivery

import (
 "context"
 "encoding/json"
 "errors"
 "math"
 "sync/atomic"
 "time"

 "github.com/google/uuid"
 "go.mongodb.org/mongo-driver/bson"
 "go.mongodb.org/mongo-driver/mongo"
)

// Alias routing is not a device manifest. A provider acceptance here means
// the exact original user-alias request was accepted, NOT every device delivered.
// No DeviceSHA field or FCM token-set API is reused to hide that distinction.
type KafkaAliasRequest struct {
 Vendor string `bson:"vendor" json:"vendor"`
 Binding string `bson:"binding" json:"binding"`
 Protocol string `bson:"protocol" json:"protocol"`
 RequestID string `bson:"request_id" json:"requestID"`
 BodySHA256 string `bson:"body_sha256" json:"bodySHA256"`
}
type kafkaAliasReceipt struct {
 ID string `bson:"_id"`
 Kind string `bson:"kind"`
 Epoch string `bson:"epoch"`
 Branch string `bson:"branch_id"`
 PolicySHA string `bson:"policy_sha256"`
 RecipientSHA string `bson:"recipient_sha256"`
 PayloadSHA string `bson:"payload_sha256"`
 RequestSHA string `bson:"request_sha256"`
 Input KafkaAliasRequest `bson:"input"`
 State string `bson:"state"`
 Revision int64 `bson:"revision"`
 AttemptID string `bson:"attempt_id"`
 InstanceID string `bson:"instance_id"`
 OutcomeSHA string `bson:"outcome_sha256"`
 ProviderReceiptSHA string `bson:"provider_receipt_sha256"`
 HistorySHA string `bson:"history_sha256"`
 Accounted bool `bson:"accounted"`
}
func aliasTerminal(state string)bool{return state=="alias-provider-accepted" || state=="alias-provider-ignored" || state=="suppressed" || state=="operator-rejected"}
func validAliasRequest(input KafkaAliasRequest)bool{
 if input.Vendor!="jpush" && input.Vendor!="getui" || !validSHA(input.Binding) || !validSHA(input.BodySHA256) || len(input.RequestID)!=32 || !validSHA(input.RequestID+input.RequestID){return false}
 return input.Vendor=="jpush" && input.Protocol=="jpush-alias-v3" || input.Vendor=="getui" && input.Protocol=="getui-alias-v2"
}
func (s *Store) readKafkaAliasReceipt(ctx context.Context,d deliveryDuty,user string)(*kafkaAliasReceipt,error){
 if s==nil || ctx==nil || !identity(user){return nil,ErrInvalid}
 id:=key(s.epoch,"kafka-alias-vendor",d.BranchID+"\x00"+hash([]byte(user)))
 var row kafkaAliasReceipt
 if err:=s.db.Collection(Effects).FindOne(ctx,bson.M{"_id":id}).Decode(&row);err!=nil{if errors.Is(err,mongo.ErrNoDocuments){return nil,nil};return nil,err}
 if row.ID!=id || row.Kind!="kafka-alias-vendor-v1" || row.Epoch!=s.epoch || row.Branch!=d.BranchID || row.PolicySHA!=d.PolicySHA || row.RecipientSHA!=hash([]byte(user)) || row.PayloadSHA!=d.PayloadSHA || !validAliasRequest(row.Input) || !validUUID(row.AttemptID) || !validUUID(row.InstanceID) || row.Revision<1 || row.Revision>=math.MaxInt64 || row.HistorySHA!="" && !validSHA(row.HistorySHA) || row.Accounted && !aliasTerminal(row.State){return nil,ErrConflict}
 switch row.State{
 case "prepared","dispatching":if row.OutcomeSHA!="" || row.ProviderReceiptSHA!=""{return nil,ErrConflict}
 case "unconfirmed","not-dispatched","pending-confirmed","operator-rejected","suppressed":if !validSHA(row.OutcomeSHA) || row.ProviderReceiptSHA!=""{return nil,ErrConflict}
 case "alias-provider-accepted","alias-provider-ignored":if !validSHA(row.OutcomeSHA) || !validSHA(row.ProviderReceiptSHA){return nil,ErrConflict}
 default:return nil,ErrConflict
 }
 policy,err:=s.readKafkaPolicy(ctx,d.PolicySHA);if err!=nil{return nil,err}
 if policy.Vendor!=row.Input.Vendor || policy.VendorBinding!=row.Input.Binding{return nil,ErrConflict}
 body,err:=json.Marshal([]any{row.Epoch,row.Branch,d.CID,row.RecipientSHA,row.PayloadSHA,row.PolicySHA,row.Input})
 if err!=nil || hash(body)!=row.RequestSHA{return nil,ErrConflict};return &row,nil
}

type kafkaOfflineOutcome struct {ID,State string;Revision int64;Accounted,Terminal bool}
// Read-only outcome projection for the EXISTING real page/settlement path.
// An alias never grants member writes or changes the frozen owner roster.
func (s *Store) readKafkaOfflineOutcome(ctx context.Context,d deliveryDuty,user string)(*kafkaOfflineOutcome,error){
 alias,err:=s.readKafkaAliasReceipt(ctx,d,user);if err!=nil{return nil,err}
 vendor,err:=s.readKafkaVendorReceipt(ctx,d,user);if err!=nil{return nil,err}
 if alias!=nil && vendor!=nil{return nil,ErrConflict}
 if alias!=nil{return &kafkaOfflineOutcome{ID:alias.ID,State:alias.State,Revision:alias.Revision,Accounted:alias.Accounted,Terminal:aliasTerminal(alias.State)},nil}
 if vendor!=nil{return &kafkaOfflineOutcome{ID:vendor.ID,State:vendor.State,Revision:vendor.Revision,Accounted:vendor.Accounted,Terminal:kafkaVendorTerminal(vendor.State)},nil}
 return nil,nil
}

type KafkaAliasDispatch struct {store *Store;row kafkaAliasReceipt;physical atomic.Bool}
func (s *Store) BeginKafkaAliasVendor(ctx context.Context,call KafkaOfflineCall,user string,input KafkaAliasRequest)(*KafkaAliasDispatch,error){
 if s==nil || ctx==nil || mongo.SessionFromContext(ctx)!=nil || !validAliasRequest(input){return nil,ErrInvalid}
 attempt,err:=uuid.NewRandom();if err!=nil{return nil,ErrUnprepared}
 var row kafkaAliasReceipt;claimed:=false
 err=s.transaction(ctx,func(tx context.Context)error{
  claimed=false
  if err:=s.ValidateKafkaOfflineCall(tx,call,user);err!=nil{return err}
  duty,err:=s.readDutyRecord(tx,call.BranchID);if err!=nil{return err}
  policy,err:=s.readKafkaPolicy(tx,duty.PolicySHA);if err!=nil{return err}
  if policy.Vendor!=input.Vendor || policy.VendorBinding!=input.Binding{return ErrConflict}
  if policy.OfflinePushBinding!=""{return ErrUnprepared} // Required callback layer; never disable its configuration.
  generic,err:=s.readKafkaVendorReceipt(tx,duty,user);if err!=nil{return err};if generic!=nil{return ErrConflict}
  previous,err:=s.readKafkaAliasReceipt(tx,duty,user);if err!=nil{return err}
  if previous!=nil{if aliasTerminal(previous.State){return nil};if previous.State!="not-dispatched" && previous.State!="pending-confirmed"{return ErrKafkaOfflineUnconfirmed}}
  if err:=s.fenceKafkaVendorParent(tx,duty,call);err!=nil{return err}
  encoded,err:=json.Marshal([]any{duty.Epoch,duty.BranchID,duty.CID,hash([]byte(user)),duty.PayloadSHA,duty.PolicySHA,input});if err!=nil{return ErrInvalid}
  row=kafkaAliasReceipt{ID:key(s.epoch,"kafka-alias-vendor",duty.BranchID+"\x00"+hash([]byte(user))),Kind:"kafka-alias-vendor-v1",Epoch:s.epoch,Branch:duty.BranchID,PolicySHA:duty.PolicySHA,RecipientSHA:hash([]byte(user)),PayloadSHA:duty.PayloadSHA,RequestSHA:hash(encoded),Input:input,State:"prepared",Revision:1,AttemptID:attempt.String(),InstanceID:s.instanceID}
  if previous==nil{if _,err:=s.db.Collection(Effects).InsertOne(tx,row);err!=nil{return err}}else{
   if previous.Revision>=math.MaxInt64-3{return ErrConflict};row.Revision=previous.Revision+1
   history,_:=json.Marshal(previous);row.HistorySHA=hash(history)
   changed,err:=s.db.Collection(Effects).ReplaceOne(tx,bson.M{"_id":previous.ID,"revision":previous.Revision,"state":previous.State,"accounted":false},row)
   if err!=nil{return err};if changed.MatchedCount!=1 || changed.ModifiedCount!=1{return ErrConflict}
  }
  claimed=true;return nil
 })
 if err!=nil{return nil,err};if !claimed{return nil,nil};return &KafkaAliasDispatch{store:s,row:row},nil
}

// This is called by the final owned RoundTripper after any Getui auth refresh.
// A canceled or ambiguous CAS never grants a physical POST; the local permit
// cannot be used twice even if the caller repeats the same transport request.
func (d *KafkaAliasDispatch) BeforeProvider(ctx context.Context,call KafkaOfflineCall,user,bodySHA string)error{
 if d==nil || d.store==nil || ctx==nil || d.row.RecipientSHA!=hash([]byte(user)) || bodySHA!=d.row.Input.BodySHA256 || !d.physical.CompareAndSwap(false,true){return ErrConflict}
 if err:=d.store.ValidateKafkaOfflineCall(ctx,call,user);err!=nil{return err}
 changed,err:=d.store.db.Collection(Effects).UpdateOne(ctx,bson.M{"_id":d.row.ID,"revision":d.row.Revision,"state":"prepared","attempt_id":d.row.AttemptID,"instance_id":d.row.InstanceID,"request_sha256":d.row.RequestSHA,"accounted":false},bson.M{"$set":bson.M{"state":"dispatching"}})
 if err!=nil || changed.MatchedCount!=1 || changed.ModifiedCount!=1{return ErrKafkaOfflineUnconfirmed};return nil
}
func (d *KafkaAliasDispatch) finish(parent context.Context,state,outcomeSHA,providerSHA string)error{
 if d==nil || d.store==nil || parent==nil || !validSHA(outcomeSHA){return ErrInvalid}
 if aliasTerminal(state) && !validSHA(providerSHA) && state!="operator-rejected" && state!="suppressed"{return ErrInvalid}
 expectedState:="dispatching";if state=="not-dispatched"{expectedState="prepared"}
 ctx,cancel:=context.WithTimeout(context.WithoutCancel(parent),5*time.Second);defer cancel()
 changed,err:=d.store.db.Collection(Effects).UpdateOne(ctx,bson.M{"_id":d.row.ID,"revision":d.row.Revision,"state":expectedState,"attempt_id":d.row.AttemptID,"instance_id":d.row.InstanceID,"request_sha256":d.row.RequestSHA,"accounted":false},bson.M{"$set":bson.M{"state":state,"outcome_sha256":outcomeSHA,"provider_receipt_sha256":providerSHA},"$inc":bson.M{"revision":int64(1)}})
 if err!=nil || changed.MatchedCount!=1 || changed.ModifiedCount!=1{return ErrKafkaOfflineUnconfirmed}
 if state=="unconfirmed"{return ErrKafkaOfflineUnconfirmed};return nil
}
func (d *KafkaAliasDispatch) NotDispatched(ctx context.Context)error{return d.finish(ctx,"not-dispatched",hash([]byte("owned alias transport attests zero physical POST")),"")}
type KafkaAliasReport struct {
 PhysicalAttemptStarted,ProviderAccepted,ProviderIgnored bool
 ProviderReceiptSHA256,OutcomeSHA256 string
}
func (d *KafkaAliasDispatch) Finish(ctx context.Context,report KafkaAliasReport)error{
 if !report.PhysicalAttemptStarted || !validSHA(report.OutcomeSHA256){return ErrInvalid}
 state:="unconfirmed";provider:=""
 if report.ProviderAccepted!=report.ProviderIgnored && validSHA(report.ProviderReceiptSHA256){provider=report.ProviderReceiptSHA256;if report.ProviderAccepted{state="alias-provider-accepted"}else{state="alias-provider-ignored"}}
 proof,_:=json.Marshal([]any{"alias-provider-acceptance-not-device-delivery",state,report})
 return d.finish(ctx,state,hash(proof),provider)
}

// The adapter must inspect the original durable outcome before auth, badge or
// payload preparation. Receipt absence is not a statement about an old POST.
func (s *Store) ResolveKafkaAliasVendor(ctx context.Context,call KafkaOfflineCall,user string)(string,error){
 if err:=s.ValidateKafkaOfflineCall(ctx,call,user);err!=nil{return "",err}
 d,err:=s.readDutyRecord(ctx,call.BranchID);if err!=nil{return "",err}
 generic,err:=s.readKafkaVendorReceipt(ctx,d,user);if err!=nil{return "",err};if generic!=nil{return "",ErrConflict}
 row,err:=s.readKafkaAliasReceipt(ctx,d,user);if err!=nil{return "",err};if row==nil{return "pending",nil};return row.State,nil
}
