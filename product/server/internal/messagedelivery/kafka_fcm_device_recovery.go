package messagedelivery

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

type KafkaFCMDeviceSummary struct {
	BranchID,RecipientSHA256,DeviceSHA256,PolicySHA256 string
	ManifestSHA256,RequestSHA256,State,OutcomeSHA256,ProviderReceiptSHA256 string
	AttemptID,InstanceID,RecordSHA256 string
	Revision int64
}

func fcmDeviceSummary(row kafkaFCMDevice)KafkaFCMDeviceSummary{
	body,_:=json.Marshal(row)
	return KafkaFCMDeviceSummary{BranchID:row.Branch,RecipientSHA256:row.User,DeviceSHA256:row.Device,PolicySHA256:row.Policy,ManifestSHA256:row.Manifest,RequestSHA256:row.Request,State:row.State,OutcomeSHA256:row.Outcome,ProviderReceiptSHA256:row.ProviderReceipt,AttemptID:row.Attempt,InstanceID:row.Instance,RecordSHA256:hash(body),Revision:row.Revision}
}

func (s *Store) inspectFCMDevice(ctx context.Context,branch,user,device string)(*KafkaFCMManifest,*kafkaFCMDevice,error){
	if s==nil || ctx==nil || !validUUID(branch) || !identity(user) || !validSHA(device){return nil,nil,ErrInvalid}
	if err:=s.CheckKafkaReady(ctx);err!=nil{return nil,nil,err}
	d,err:=s.readDutyRecord(ctx,branch);if err!=nil{return nil,nil,err}
	if d.ProducerVersion!="ordinary-kafka-v1"{return nil,nil,ErrConflict}
	row,err:=s.readKafkaVendorReceipt(ctx,d,user);if err!=nil{return nil,nil,err}
	if row==nil || row.Kind!="kafka-vendor-devices-v2"{return nil,nil,ErrCommitUnconfirmed}
	m:=&KafkaFCMManifest{store:s,row:*row}
	child,err:=m.read(ctx,device);if err!=nil{return nil,nil,err}
	if child==nil{return nil,nil,ErrCommitUnconfirmed}
	return m,child,nil
}

// Not registered on public HTTP/RPC. This contains no token, user ID, URL,
// credential or plaintext and remains inspectable after duty redaction.
func (s *Store) InspectKafkaFCMDevice(ctx context.Context,branch,user,device string)(KafkaFCMDeviceSummary,error){
	if ctx==nil{return KafkaFCMDeviceSummary{},ErrInvalid}
	current,cancel:=context.WithTimeout(ctx,5*time.Second);defer cancel()
	_,row,err:=s.inspectFCMDevice(current,branch,user,device);if err!=nil{return KafkaFCMDeviceSummary{},err}
	return fcmDeviceSummary(*row),nil
}

type KafkaFCMDeviceConfirmation struct {
	Decision,EvidenceSHA256,ProviderReceiptSHA256 string
	Drain KafkaAttemptDrainProof
}

// An unknown device is never retried merely because its lease expired. The
// exact original instance must be drained and independent endpoint evidence
// must name this one device attempt. Other siblings are neither confirmed nor
// resent by this operation. A CLI/proof-file verifier is still required.
func (s *Store) ConfirmKafkaFCMDevice(ctx context.Context,user string,expected KafkaFCMDeviceSummary,confirmation KafkaFCMDeviceConfirmation)error{
	if ctx==nil || !identity(user) || !validSHA(confirmation.EvidenceSHA256) || confirmation.Drain.InstanceID!=expected.InstanceID || !validSHA(confirmation.Drain.EvidenceSHA256){return ErrInvalid}
	current,cancel:=context.WithTimeout(ctx,5*time.Second);defer cancel()
	m,row,err:=s.inspectFCMDevice(current,expected.BranchID,user,expected.DeviceSHA256);if err!=nil{return err}
	actual:=fcmDeviceSummary(*row)
	left,_:=json.Marshal(actual);right,_:=json.Marshal(expected)
	eligible:=row.State=="dispatching" || row.State=="unconfirmed" || confirmation.Decision=="rejected" && (row.State=="not-dispatched" || row.State=="not-applied")
	if !bytes.Equal(left,right) || !eligible || row.Revision>=math.MaxInt64-1{return ErrConflict}
	state:=""
	switch confirmation.Decision{
	case "not-applied":
		if confirmation.ProviderReceiptSHA256!=""{return ErrInvalid};state="not-applied"
	case "rejected":
		if confirmation.ProviderReceiptSHA256!=""{return ErrInvalid};state="operator-rejected"
	case "applied":
		if !validSHA(confirmation.ProviderReceiptSHA256){return ErrInvalid};state="accepted"
	default:return ErrInvalid
	}
	proof,_:=json.Marshal([]any{"independent-device-evidence",actual.RecordSHA256,confirmation})
	changed,err:=s.db.Collection(Effects).UpdateOne(current,bson.M{"_id":row.ID,"revision":row.Revision,"state":row.State,"attempt_id":row.Attempt,"instance_id":row.Instance,"manifest_sha256":row.Manifest,"request_sha256":row.Request},bson.M{"$set":bson.M{"state":state,"outcome_sha256":hash(proof),"provider_receipt_sha256":confirmation.ProviderReceiptSHA256},"$inc":bson.M{"revision":int64(1)}})
	if err!=nil || changed.MatchedCount!=1 || changed.ModifiedCount!=1{return ErrKafkaOfflineUnconfirmed}
	return m.reconcile(current)
}
