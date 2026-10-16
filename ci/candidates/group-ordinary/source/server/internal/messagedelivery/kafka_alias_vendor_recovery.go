package messagedelivery

import (
 "bytes"
 "context"
 "encoding/json"
 "math"
 "time"

 "go.mongodb.org/mongo-driver/bson"
)

type KafkaAliasVendorSummary struct {
 BranchID,RecipientSHA256,PolicySHA256,Vendor,Protocol,Binding string
 RequestID,RequestSHA256,BodySHA256,PayloadSHA256 string
 State,OutcomeSHA256,ProviderReceiptSHA256 string
 AttemptID,InstanceID,RecordSHA256 string
 Revision int64
}
func kafkaAliasSummary(row kafkaAliasReceipt)KafkaAliasVendorSummary{
 raw,_:=json.Marshal(row)
 return KafkaAliasVendorSummary{BranchID:row.Branch,RecipientSHA256:row.RecipientSHA,PolicySHA256:row.PolicySHA,Vendor:row.Input.Vendor,Protocol:row.Input.Protocol,Binding:row.Input.Binding,RequestID:row.Input.RequestID,RequestSHA256:row.RequestSHA,BodySHA256:row.Input.BodySHA256,PayloadSHA256:row.PayloadSHA,State:row.State,OutcomeSHA256:row.OutcomeSHA,ProviderReceiptSHA256:row.ProviderReceiptSHA,AttemptID:row.AttemptID,InstanceID:row.InstanceID,RecordSHA256:hash(raw),Revision:row.Revision}
}
func (s *Store) InspectKafkaAliasVendor(ctx context.Context,branch,user string)(KafkaAliasVendorSummary,error){
 if s==nil || ctx==nil || !validUUID(branch) || !identity(user){return KafkaAliasVendorSummary{},ErrInvalid}
 current,cancel:=context.WithTimeout(ctx,5*time.Second);defer cancel()
 if err:=s.CheckKafkaReady(current);err!=nil{return KafkaAliasVendorSummary{},err}
 d,err:=s.readDutyRecord(current,branch);if err!=nil{return KafkaAliasVendorSummary{},err}
 if d.ProducerVersion!="ordinary-kafka-v1"{return KafkaAliasVendorSummary{},ErrConflict}
 row,err:=s.readKafkaAliasReceipt(current,d,user);if err!=nil{return KafkaAliasVendorSummary{},err};if row==nil{return KafkaAliasVendorSummary{},ErrCommitUnconfirmed}
 return kafkaAliasSummary(*row),nil
}

type KafkaAliasVendorConfirmation struct {
 Decision string
 EvidenceSHA256 string
 ProviderReceiptSHA256 string
 Drain KafkaAttemptDrainProof
}
// Private operator CAS; never registered on HTTP/RPC. A provider acceptance
// confirms only this original alias request. It does not attest device delivery.
// CLI integration must verify the original managed-instance drain and independent
// endpoint evidence for this exact request/attempt before calling this method.
func (s *Store) ConfirmKafkaAliasVendor(ctx context.Context,user string,expected KafkaAliasVendorSummary,proof KafkaAliasVendorConfirmation)error{
 if s==nil || ctx==nil || !identity(user) || !validSHA(proof.EvidenceSHA256) || proof.Drain.InstanceID!=expected.InstanceID || !validSHA(proof.Drain.EvidenceSHA256){return ErrInvalid}
 current,cancel:=context.WithTimeout(ctx,5*time.Second);defer cancel()
 actual,err:=s.InspectKafkaAliasVendor(current,expected.BranchID,user);if err!=nil{return err}
 left,_:=json.Marshal(actual);right,_:=json.Marshal(expected)
 eligible:=actual.State=="prepared" || actual.State=="dispatching" || actual.State=="unconfirmed" || proof.Decision=="rejected" && actual.State=="not-dispatched"
 if !bytes.Equal(left,right) || !eligible || actual.Revision>=math.MaxInt64-2{return ErrConflict}
 state:=""
 switch proof.Decision{
 case "provider-accepted","provider-ignored":
  if !validSHA(proof.ProviderReceiptSHA256){return ErrInvalid};state="alias-"+proof.Decision
 case "not-applied":
  if proof.ProviderReceiptSHA256!=""{return ErrInvalid};state="pending-confirmed"
 case "rejected":
  if proof.ProviderReceiptSHA256!=""{return ErrInvalid};state="operator-rejected"
 default:return ErrInvalid
 }
 d,err:=s.readDutyRecord(current,expected.BranchID);if err!=nil{return err}
 row,err:=s.readKafkaAliasReceipt(current,d,user);if err!=nil || row==nil{return ErrConflict}
 latest,_:=json.Marshal(kafkaAliasSummary(*row));if !bytes.Equal(latest,right){return ErrConflict}
 encoded,_:=json.Marshal([]any{"operator-independent-alias-provider-evidence",actual.RecordSHA256,proof})
 changed,err:=s.db.Collection(Effects).UpdateOne(current,bson.M{"_id":row.ID,"epoch":s.epoch,"revision":row.Revision,"state":row.State,"attempt_id":row.AttemptID,"instance_id":row.InstanceID,"request_sha256":row.RequestSHA,"accounted":false},bson.M{"$set":bson.M{"state":state,"outcome_sha256":hash(encoded),"provider_receipt_sha256":proof.ProviderReceiptSHA256},"$inc":bson.M{"revision":int64(1)}})
 if err!=nil || changed.MatchedCount!=1 || changed.ModifiedCount!=1{return ErrKafkaOfflineUnconfirmed};return nil
}
