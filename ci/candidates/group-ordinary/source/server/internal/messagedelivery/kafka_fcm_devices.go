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
	"go.mongodb.org/mongo-driver/mongo/options"
)

// This manifest contains ONLY token/request hashes and badge accounting proof.
// It is frozen once per recipient before any alert POST. Raw tokens and body
// remain in the current owned adapter's memory, never in a child receipt.
type KafkaFCMManifest struct { store *Store; row kafkaVendorReceipt }

func FCMManifestFingerprint(input KafkaVendorRequest)(string,error){
	if input.Protocol!="fcm-http-v1-device-v1"{return "",ErrInvalid}
	check:=input;check.ProviderRequestSHA256=hash([]byte("fingerprint input validation"))
	if !validKafkaVendorInput(check){return "",ErrInvalid}
	body,err:=json.Marshal([]any{"fcm-http-v1-device-manifest-v1",input.Binding,input.TokenSetSHA256,input.Badge,input.BadgeLedgerValue,input.BadgeResetID,input.BadgeReceiptSHA256,input.DeviceSHA256,input.DeviceRequestSHA256})
	if err!=nil{return "",ErrInvalid};return hash(body),nil
}

func copyKafkaVendorInput(input KafkaVendorRequest)KafkaVendorRequest{
	input.DeviceSHA256=append([]string(nil),input.DeviceSHA256...)
	input.DeviceRequestSHA256=append([]string(nil),input.DeviceRequestSHA256...)
	return input
}
func (m *KafkaFCMManifest) Input()KafkaVendorRequest{if m==nil{return KafkaVendorRequest{}};return copyKafkaVendorInput(m.row.Input)}
func (m *KafkaFCMManifest) Terminal()bool{return m!=nil && kafkaVendorTerminal(m.row.State)}

// A child ACK may commit while its recipient reconciliation loses the ACK or
// times out. Recovery retries ONLY this local aggregation, never that POST.
func (m *KafkaFCMManifest) Settle(ctx context.Context)error{
	if ctx==nil{return ErrInvalid}
	current,cancel:=context.WithTimeout(ctx,5*time.Second);defer cancel()
	return m.reconcile(current)
}

// Existing first-write intent is read before live token reads/badge accounting.
// A changed token set after restart cannot silently create a fresh manifest.
func (s *Store) ReadKafkaFCMManifest(ctx context.Context,call KafkaOfflineCall,user string)(*KafkaFCMManifest,error){
	if err:=s.ValidateKafkaOfflineCall(ctx,call,user);err!=nil{return nil,err}
	d,err:=s.readDutyRecord(ctx,call.BranchID);if err!=nil{return nil,err}
	row,err:=s.readKafkaVendorReceipt(ctx,d,user);if err!=nil || row==nil{return nil,err}
	if row.Kind!="kafka-vendor-devices-v2"{return nil,ErrConflict}
	return &KafkaFCMManifest{store:s,row:*row},nil
}

func (s *Store) FreezeKafkaFCMManifest(ctx context.Context,call KafkaOfflineCall,user string,input KafkaVendorRequest)(*KafkaFCMManifest,error){
	if s==nil || ctx==nil || mongo.SessionFromContext(ctx)!=nil || !validKafkaVendorInput(input){return nil,ErrInvalid}
	fingerprint,err:=FCMManifestFingerprint(input);if err!=nil || fingerprint!=input.ProviderRequestSHA256{return nil,ErrInvalid}
	input=copyKafkaVendorInput(input)
	id,err:=uuid.NewRandom();if err!=nil{return nil,ErrUnprepared}
	var frozen kafkaVendorReceipt
	err=s.transaction(ctx,func(tx context.Context)error{
		frozen=kafkaVendorReceipt{}
		if err:=s.ValidateKafkaOfflineCall(tx,call,user);err!=nil{return err}
		d,err:=s.readDutyRecord(tx,call.BranchID);if err!=nil{return err}
		policy,err:=s.readKafkaPolicy(tx,d.PolicySHA);if err!=nil{return err}
		if policy.Vendor!="fcm" || policy.VendorBinding!=input.Binding{return ErrConflict}
		// This new primitive does not remove configured offline callbacks.
		// Their frozen result/read-cap layer is required before such a policy
		// can be made ready; a caller must not disable it to obtain a PASS.
		if policy.OfflinePushBinding!=""{return ErrUnprepared}
		old,err:=s.readKafkaVendorReceipt(tx,d,user);if err!=nil{return err}
		if old!=nil{
			left,_:=json.Marshal(old.Input);right,_:=json.Marshal(input)
			if old.Kind!="kafka-vendor-devices-v2" || string(left)!=string(right){return ErrConflict}
			frozen=*old;return nil
		}
		if err:=s.fenceKafkaVendorParent(tx,d,call);err!=nil{return err}
		body,err:=json.Marshal([]any{d.Epoch,d.BranchID,d.CID,hash([]byte(user)),d.PayloadSHA,d.PolicySHA,input});if err!=nil{return ErrInvalid}
		state:="devices-pending";outcome:=""
		if len(input.DeviceSHA256)==0{state="no-registered-token";outcome=hash([]byte("owned token reader proved empty before badge accounting"))}
		frozen=kafkaVendorReceipt{ID:key(s.epoch,"kafka-vendor",d.BranchID+"\x00"+hash([]byte(user))),Kind:"kafka-vendor-devices-v2",Epoch:s.epoch,Branch:d.BranchID,PolicySHA:d.PolicySHA,UserSHA:hash([]byte(user)),PayloadSHA:d.PayloadSHA,Vendor:"fcm",Binding:input.Binding,RequestSHA:hash(body),Input:input,State:state,Revision:1,AttemptID:id.String(),InstanceID:s.instanceID,OutcomeSHA:outcome}
		if !validKafkaVendorReceipt(frozen){return ErrConflict}
		_,err=s.db.Collection(Effects).InsertOne(tx,frozen);return err
	})
	if err!=nil{return nil,err}
	return &KafkaFCMManifest{store:s,row:frozen},nil
}

type kafkaFCMDevice struct {
	ID string `bson:"_id"`
	Kind string `bson:"kind"`
	Epoch string `bson:"epoch"`
	Branch string `bson:"branch_id"`
	Policy string `bson:"policy_sha256"`
	User string `bson:"user_sha256"`
	Payload string `bson:"payload_sha256"`
	Manifest string `bson:"manifest_sha256"`
	Device string `bson:"device_sha256"`
	Request string `bson:"request_sha256"`
	State string `bson:"state"`
	Revision int64 `bson:"revision"`
	Attempt string `bson:"attempt_id"`
	Instance string `bson:"instance_id"`
	Outcome string `bson:"outcome_sha256"`
	ProviderReceipt string `bson:"provider_receipt_sha256"`
	History string `bson:"history_sha256"`
}

func terminalFCMDevice(state string)bool{return state=="accepted" || state=="unregistered" || state=="operator-rejected"}
func validFCMDevice(row kafkaFCMDevice,parent kafkaVendorReceipt)bool{
	if row.Kind!="kafka-fcm-device-v1" || row.Epoch!=parent.Epoch || row.Branch!=parent.Branch || row.Policy!=parent.PolicySHA || row.User!=parent.UserSHA || row.Payload!=parent.PayloadSHA || row.Manifest!=parent.RequestSHA || !validSHA(row.Device) || !validSHA(row.Request) || row.Revision<1 || row.Revision>=math.MaxInt64 || !validUUID(row.Attempt) || !validUUID(row.Instance) || row.History!="" && !validSHA(row.History){return false}
	if row.ID!=key(parent.Epoch,"kafka-fcm-device",parent.Branch+"\x00"+parent.UserSHA+"\x00"+row.Device){return false}
	bound:=false
	for i,device:=range parent.Input.DeviceSHA256{if device==row.Device && parent.Input.DeviceRequestSHA256[i]==row.Request{bound=true}}
	if !bound{return false}
	if row.State=="dispatching"{return row.Outcome=="" && row.ProviderReceipt==""}
	if !validSHA(row.Outcome){return false}
	switch row.State{
	case "accepted":return validSHA(row.ProviderReceipt)
	case "unregistered","operator-rejected","not-dispatched","not-applied","unconfirmed":return row.ProviderReceipt==""
	default:return false
	}
}

func (m *KafkaFCMManifest) read(ctx context.Context,device string)(*kafkaFCMDevice,error){
	if m==nil || m.store==nil || ctx==nil || !validSHA(device){return nil,ErrInvalid}
	id:=key(m.row.Epoch,"kafka-fcm-device",m.row.Branch+"\x00"+m.row.UserSHA+"\x00"+device)
	var row kafkaFCMDevice
	if err:=m.store.db.Collection(Effects).FindOne(ctx,bson.M{"_id":id}).Decode(&row);err!=nil{if errors.Is(err,mongo.ErrNoDocuments){return nil,nil};return nil,err}
	if !validFCMDevice(row,m.row){return nil,ErrConflict};return &row,nil
}

type KafkaFCMDeviceDispatch struct { manifest *KafkaFCMManifest; row kafkaFCMDevice; permit atomic.Bool }

// Unknown for one device blocks ONLY that frozen device. The recipient-wide
// manifest cannot grant a batch attempt, replace tokens, or retry siblings.
func (m *KafkaFCMManifest) BeginDevice(ctx context.Context,call KafkaOfflineCall,user,device,request string)(*KafkaFCMDeviceDispatch,error){
	if m==nil || m.store==nil || ctx==nil || mongo.SessionFromContext(ctx)!=nil || hash([]byte(user))!=m.row.UserSHA || !validSHA(device) || !validSHA(request){return nil,ErrInvalid}
	attempt,err:=uuid.NewRandom();if err!=nil{return nil,ErrUnprepared}
	var claimed *kafkaFCMDevice
	err=m.store.transaction(ctx,func(tx context.Context)error{
		claimed=nil
		if err:=m.store.ValidateKafkaOfflineCall(tx,call,user);err!=nil{return err}
		d,err:=m.store.readDutyRecord(tx,call.BranchID);if err!=nil{return err}
		parent,err:=m.store.readKafkaVendorReceipt(tx,d,user);if err!=nil{return err}
		if parent==nil || parent.Kind!="kafka-vendor-devices-v2" || parent.RequestSHA!=m.row.RequestSHA{return ErrConflict}
		if kafkaVendorTerminal(parent.State){return nil};if parent.State!="devices-pending"{return ErrConflict}
		bound:=false;for i,hash:=range parent.Input.DeviceSHA256{if hash==device && parent.Input.DeviceRequestSHA256[i]==request{bound=true}}
		if !bound{return ErrConflict}
		old,err:=m.read(tx,device);if err!=nil{return err}
		if old!=nil{
			if terminalFCMDevice(old.State){return nil}
			if old.State!="not-dispatched" && old.State!="not-applied"{return ErrKafkaOfflineUnconfirmed}
		}
		if err:=m.store.fenceKafkaVendorParent(tx,d,call);err!=nil{return err}
		row:=kafkaFCMDevice{ID:key(m.row.Epoch,"kafka-fcm-device",m.row.Branch+"\x00"+m.row.UserSHA+"\x00"+device),Kind:"kafka-fcm-device-v1",Epoch:m.row.Epoch,Branch:m.row.Branch,Policy:m.row.PolicySHA,User:m.row.UserSHA,Payload:m.row.PayloadSHA,Manifest:m.row.RequestSHA,Device:device,Request:request,State:"dispatching",Revision:1,Attempt:attempt.String(),Instance:m.store.instanceID}
		if old!=nil{
			if old.Revision>=math.MaxInt64-2{return ErrConflict};row.Revision=old.Revision+1
			history,_:=json.Marshal(old);row.History=hash(history)
			changed,err:=m.store.db.Collection(Effects).ReplaceOne(tx,bson.M{"_id":old.ID,"revision":old.Revision,"state":old.State,"attempt_id":old.Attempt,"manifest_sha256":m.row.RequestSHA},row)
			if err!=nil{return err};if changed.MatchedCount!=1 || changed.ModifiedCount!=1{return ErrConflict}
		}else{if _,err:=m.store.db.Collection(Effects).InsertOne(tx,row);err!=nil{return err}}
		claimed=&row;return nil
	})
	if err!=nil || claimed==nil{return nil,err}
	return &KafkaFCMDeviceDispatch{manifest:m,row:*claimed},nil
}

// This method is consumed by the owned HTTP v1 physical gate AFTER OAuth and
// token/badge awaits. A permit may cross this final boundary only once.
func (a *KafkaFCMDeviceDispatch) BeforeProvider(ctx context.Context,call KafkaOfflineCall,user,request string)error{
	if a==nil || a.manifest==nil || a.manifest.store==nil || request!=a.row.Request || hash([]byte(user))!=a.row.User{return ErrInvalid}
	if err:=a.manifest.store.ValidateKafkaOfflineCall(ctx,call,user);err!=nil{return err}
	row,err:=a.manifest.read(ctx,a.row.Device);if err!=nil{return err}
	if row==nil || row.Revision!=a.row.Revision || row.State!="dispatching" || row.Attempt!=a.row.Attempt || row.Instance!=a.row.Instance || row.Request!=a.row.Request || !a.permit.CompareAndSwap(false,true){return ErrConflict}
	return nil
}

type KafkaFCMDeviceReport struct {
	DispatchStarted bool
	KnownUnregistered bool
	ProviderReceiptSHA256 string
	OutcomeSHA256 string
}

func (a *KafkaFCMDeviceDispatch) complete(parent context.Context,state,proof,receipt string)error{
	if a==nil || a.manifest==nil || a.manifest.store==nil || parent==nil || !validSHA(proof) || a.row.Revision>=math.MaxInt64-1{return ErrInvalid}
	current,cancel:=context.WithTimeout(context.WithoutCancel(parent),5*time.Second);defer cancel()
	changed,err:=a.manifest.store.db.Collection(Effects).UpdateOne(current,bson.M{"_id":a.row.ID,"epoch":a.row.Epoch,"revision":a.row.Revision,"state":"dispatching","attempt_id":a.row.Attempt,"instance_id":a.row.Instance,"request_sha256":a.row.Request},bson.M{"$set":bson.M{"state":state,"outcome_sha256":proof,"provider_receipt_sha256":receipt},"$inc":bson.M{"revision":int64(1)}})
	if err!=nil || changed.MatchedCount!=1 || changed.ModifiedCount!=1{return ErrKafkaOfflineUnconfirmed}
	if state=="unconfirmed"{return ErrKafkaOfflineUnconfirmed}
	return a.manifest.reconcile(current)
}

// Called only when the actual owned physical transport proves zero POSTs.
// Cancellation or a local gate rejection is not a rollback of another device.
func (a *KafkaFCMDeviceDispatch) NotDispatched(ctx context.Context,evidenceSHA string)error{
	return a.complete(ctx,"not-dispatched",evidenceSHA,"")
}

func (a *KafkaFCMDeviceDispatch) Finish(ctx context.Context,report KafkaFCMDeviceReport)error{
	if a==nil{return ErrInvalid}
	state:="unconfirmed";receipt:=""
	if report.DispatchStarted && a.permit.Load(){
		if report.KnownUnregistered && report.ProviderReceiptSHA256=="" && validSHA(report.OutcomeSHA256){state="unregistered"}
		if !report.KnownUnregistered && validSHA(report.ProviderReceiptSHA256) && validSHA(report.OutcomeSHA256){state="accepted";receipt=report.ProviderReceiptSHA256}
	}
	proof,_:=json.Marshal([]any{report.DispatchStarted,report.KnownUnregistered,report.ProviderReceiptSHA256,report.OutcomeSHA256})
	return a.complete(ctx,state,hash(proof),receipt)
}

// All sibling receipts must be known before the one recipient becomes done.
// This never increments a message sequence or rewrites a message/body/token.
func (m *KafkaFCMManifest) reconcile(ctx context.Context)error{
	if m==nil || m.store==nil{return ErrInvalid}
	return m.store.transaction(ctx,func(tx context.Context)error{
		var parent kafkaVendorReceipt
		if err:=m.store.db.Collection(Effects).FindOne(tx,bson.M{"_id":m.row.ID}).Decode(&parent);err!=nil{return err}
		if !validKafkaVendorReceipt(parent) || parent.RequestSHA!=m.row.RequestSHA{return ErrConflict}
		if kafkaVendorTerminal(parent.State){return nil};if parent.State!="devices-pending"{return ErrConflict}
		// The frozen manifest already gives every exact child _id. Use the
		// existing _id index; do not scan all historical effect obligations or
		// add an implicit DDL/readiness migration for this aggregation.
		ids:=make([]string,0,len(parent.Input.DeviceSHA256))
		for _,device:=range parent.Input.DeviceSHA256{ids=append(ids,key(parent.Epoch,"kafka-fcm-device",parent.Branch+"\x00"+parent.UserSHA+"\x00"+device))}
		cursor,err:=m.store.db.Collection(Effects).Find(tx,bson.M{"_id":bson.M{"$in":ids}},options.Find().SetLimit(int64(len(ids)+1)))
		if err!=nil{return err}
		rows:=map[string]kafkaFCMDevice{}
		for cursor.Next(tx){var row kafkaFCMDevice;if cursor.Decode(&row)!=nil || !validFCMDevice(row,parent){err=ErrConflict;break};if _,duplicate:=rows[row.Device];duplicate{err=ErrConflict;break};rows[row.Device]=row}
		if err==nil{err=cursor.Err()};closeErr:=cursor.Close(tx);if err!=nil || closeErr!=nil{return errors.Join(err,closeErr)}
		if len(rows)>len(parent.Input.DeviceSHA256){return ErrConflict}
		results:=make([]KafkaVendorDeviceResult,0,len(rows));proofs:=make([]string,0,len(rows))
		for _,device:=range parent.Input.DeviceSHA256{
			row,ok:=rows[device];if !ok || !terminalFCMDevice(row.State){return nil}
			results=append(results,KafkaVendorDeviceResult{DeviceSHA256:device,ProviderReceiptSHA256:row.ProviderReceipt,KnownRejection:row.State!="accepted"})
			proof,_:=json.Marshal(row);proofs=append(proofs,hash(proof))
		}
		if len(results)==0 || parent.Revision>=math.MaxInt64-1{return ErrConflict};body,_:=json.Marshal(proofs)
		changed,err:=m.store.db.Collection(Effects).UpdateOne(tx,bson.M{"_id":parent.ID,"revision":parent.Revision,"state":"devices-pending","request_sha256":parent.RequestSHA,"accounted":false},bson.M{"$set":bson.M{"state":"completed","outcome_sha256":hash(body),"device_results":results},"$inc":bson.M{"revision":int64(1)}})
		if err!=nil{return err};if changed.MatchedCount!=1 || changed.ModifiedCount!=1{return ErrConflict};return nil
	})
}
