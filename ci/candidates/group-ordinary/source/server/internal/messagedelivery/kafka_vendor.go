package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type KafkaVendorRequest struct {
	Vendor,Binding,ProviderRequestSHA256,TokenSetSHA256 string
	DeviceSHA256 []string
	Badge int64
	BadgeResetID,BadgeReceiptSHA256 string
	Protocol string `bson:"protocol,omitempty" json:"protocol,omitempty"`
	DeviceRequestSHA256 []string `bson:"device_request_sha256,omitempty" json:"deviceRequestSHA256,omitempty"`
	BadgeLedgerValue int64 `bson:"badge_ledger_value,omitempty" json:"badgeLedgerValue,omitempty"`
}
type KafkaVendorDeviceResult struct {
	DeviceSHA256 string `bson:"device_sha256" json:"deviceSHA256"`
	ProviderReceiptSHA256 string `bson:"provider_receipt_sha256" json:"providerReceiptSHA256"`
	KnownRejection bool `bson:"known_rejection" json:"knownRejection"`
}
type KafkaVendorReport struct {
	DispatchStarted bool
	NoRegisteredToken bool
	Results []KafkaVendorDeviceResult
}

type kafkaVendorReceipt struct {
	ID string `bson:"_id"`
	Kind string `bson:"kind"`
	Epoch string `bson:"epoch"`
	Branch string `bson:"branch_id"`
	PolicySHA string `bson:"policy_sha256"`
	UserSHA string `bson:"user_sha256"`
	PayloadSHA string `bson:"payload_sha256"`
	Vendor string `bson:"vendor"`
	Binding string `bson:"binding"`
	RequestSHA string `bson:"request_sha256"`
	Input KafkaVendorRequest `bson:"input"`
	State string `bson:"state"`
	Revision int64 `bson:"revision"`
	AttemptID string `bson:"attempt_id"`
	InstanceID string `bson:"instance_id"`
	OutcomeSHA string `bson:"outcome_sha256"`
	HistorySHA string `bson:"history_sha256"`
	Results []KafkaVendorDeviceResult `bson:"device_results"`
	Accounted bool `bson:"accounted"`
}

func kafkaVendorTerminal(state string)bool{
	switch state{case "completed","no-registered-token","suppressed","no-vendor","operator-rejected":return true};return false
}
func validKafkaVendorInput(input KafkaVendorRequest)bool{
	if input.Vendor!="fcm" && input.Vendor!="getui" && input.Vendor!="jpush" || !validSHA(input.Binding) || !validSHA(input.ProviderRequestSHA256) || !validSHA(input.TokenSetSHA256) || len(input.DeviceSHA256)>1000 || input.Badge<0 || input.Badge>2147483647{return false}
	if input.Protocol!="" && (input.Protocol!="fcm-http-v1-device-v1" || input.Vendor!="fcm" || len(input.DeviceRequestSHA256)!=len(input.DeviceSHA256) || input.BadgeLedgerValue<0 || input.BadgeLedgerValue>2147483647){return false}
	if input.Protocol=="" && (len(input.DeviceRequestSHA256)!=0 || input.BadgeLedgerValue!=0){return false}
	for _,proof:=range input.DeviceRequestSHA256{if !validSHA(proof){return false}}
	if input.Vendor=="fcm" && !(input.Protocol=="fcm-http-v1-device-v1" && len(input.DeviceSHA256)==0 && input.Badge==0 && input.BadgeLedgerValue==0 && input.BadgeResetID=="" && input.BadgeReceiptSHA256==""){
		if len(input.BadgeResetID)!=32 || !validSHA(input.BadgeResetID+input.BadgeResetID) || !validSHA(input.BadgeReceiptSHA256){return false}
	}else if input.BadgeResetID!="" || input.BadgeReceiptSHA256!=""{return false}
	for i,h:=range input.DeviceSHA256{if !validSHA(h) || i>0 && h<=input.DeviceSHA256[i-1]{return false}}
	return true
}
func validKafkaVendorReceipt(row kafkaVendorReceipt)bool{
	if row.Kind!="kafka-vendor-v1" && row.Kind!="kafka-vendor-devices-v2" || !validUUID(row.Epoch) || !validUUID(row.Branch) || !validSHA(row.PolicySHA) || !validSHA(row.UserSHA) || !validSHA(row.PayloadSHA) || !validSHA(row.RequestSHA) || !validUUID(row.AttemptID) || !validUUID(row.InstanceID) || row.Revision<1 || row.Revision>=math.MaxInt64 || row.HistorySHA!="" && !validSHA(row.HistorySHA){return false}
	if row.Accounted && !kafkaVendorTerminal(row.State){return false}
	if row.Kind=="kafka-vendor-devices-v2" && (row.Input.Protocol!="fcm-http-v1-device-v1" || row.Vendor!="fcm"){return false}
	if row.Kind=="kafka-vendor-devices-v2"{fingerprint,err:=FCMManifestFingerprint(row.Input);if err!=nil || fingerprint!=row.Input.ProviderRequestSHA256{return false}}
	if row.Kind=="kafka-vendor-v1" && row.Input.Protocol!=""{return false}
	switch row.State{
	case "suppressed","no-vendor":return row.Vendor=="" && row.Binding=="" && validSHA(row.OutcomeSHA) && len(row.Results)==0 && len(row.Input.DeviceSHA256)==0
	case "devices-pending":if row.Kind!="kafka-vendor-devices-v2" || row.OutcomeSHA!="" || len(row.Results)!=0{return false}
	case "dispatching":if row.Kind!="kafka-vendor-v1" || row.OutcomeSHA!=""{return false}
	case "pending-not-dispatched","pending-confirmed","unconfirmed","completed","no-registered-token","operator-rejected":if !validSHA(row.OutcomeSHA){return false}
	default:return false
	}
	if !validKafkaVendorInput(row.Input) || row.Input.Vendor!=row.Vendor || row.Input.Binding!=row.Binding{return false}
	wanted:=map[string]bool{};for _,h:=range row.Input.DeviceSHA256{wanted[h]=true}
	seen:=map[string]bool{}
	for _,result:=range row.Results{
		if !wanted[result.DeviceSHA256] || seen[result.DeviceSHA256] || result.KnownRejection && result.ProviderReceiptSHA256!="" || result.ProviderReceiptSHA256!="" && !validSHA(result.ProviderReceiptSHA256){return false};seen[result.DeviceSHA256]=true
	}
	if row.State=="dispatching" && len(row.Results)!=0{return false}
	if row.State=="no-registered-token" && (len(row.Input.DeviceSHA256)!=0 || len(row.Results)!=0){return false}
	if row.State=="completed"{
		if len(row.Input.DeviceSHA256)==0 || len(row.Results)!=len(row.Input.DeviceSHA256){return false}
		for _,result:=range row.Results{if !result.KnownRejection && !validSHA(result.ProviderReceiptSHA256){return false}}
	}
	return true
}

func (s *Store) readKafkaVendorReceipt(ctx context.Context,d deliveryDuty,user string)(*kafkaVendorReceipt,error){
	if s==nil || ctx==nil || !identity(user){return nil,ErrInvalid}
	id:=key(s.epoch,"kafka-vendor",d.BranchID+"\x00"+hash([]byte(user)))
	var row kafkaVendorReceipt
	if err:=s.db.Collection(Effects).FindOne(ctx,bson.M{"_id":id}).Decode(&row);err!=nil{if errors.Is(err,mongo.ErrNoDocuments){return nil,nil};return nil,err}
	if !validKafkaVendorReceipt(row) || row.ID!=id || row.Epoch!=s.epoch || row.Branch!=d.BranchID || row.PolicySHA!=d.PolicySHA || row.UserSHA!=hash([]byte(user)) || row.PayloadSHA!=d.PayloadSHA{return nil,ErrConflict}
	if row.Vendor!=""{
		policy,err:=s.readKafkaPolicy(ctx,d.PolicySHA);if err!=nil{return nil,err}
		if row.Vendor!=policy.Vendor || row.Binding!=policy.VendorBinding{return nil,ErrConflict}
		body,err:=json.Marshal([]any{d.Epoch,d.BranchID,d.CID,row.UserSHA,d.PayloadSHA,d.PolicySHA,row.Input})
		if err!=nil || hash(body)!=row.RequestSHA{return nil,ErrConflict}
	}
	return &row,nil
}

// The real page/claim is checked before revealing even a minimal existing
// outcome to a vendor adapter. This is not an unauthenticated status API.
func (s *Store) ResolveKafkaVendor(ctx context.Context,call KafkaOfflineCall,user string)(string,error){
	if err:=s.ValidateKafkaOfflineCall(ctx,call,user);err!=nil{return "",err}
	d,err:=s.readDutyRecord(ctx,call.BranchID);if err!=nil{return "",err}
	row,err:=s.readKafkaVendorReceipt(ctx,d,user);if err!=nil{return "",err}
	if row==nil{return "pending",nil}
	return row.State,nil
}

func (s *Store) recordKafkaOfflineSuppressed(ctx context.Context,call KafkaOfflineCall,user,reason,evidenceSHA string)error{
	if !identity(user) || !validSHA(evidenceSHA) || reason!="frozen-routing-or-visibility" && reason!="authoritative-owner-visibility" && reason!="configured-no-vendor" && reason!="authoritative-receive-option"{return ErrInvalid}
	attempt,err:=uuid.NewRandom();if err!=nil{return ErrUnprepared}
	return s.transaction(ctx,func(tx context.Context)error{
		d,err:=s.boundKafkaOfflineDuty(tx,call);if err!=nil{return err}
		inPage:=false;for _,uid:=range call.Recipients{if uid==user{inPage=true}}
		if !inPage{return ErrInvalid}
		old,err:=s.readKafkaVendorReceipt(tx,d,user);if err!=nil{return err}
		if old!=nil{if kafkaVendorTerminal(old.State){return nil};if old.State!="pending-not-dispatched" && old.State!="pending-confirmed"{return ErrKafkaOfflineUnconfirmed}}
		state:="suppressed"
		if reason=="configured-no-vendor"{policy,err:=s.readKafkaPolicy(tx,d.PolicySHA);if err!=nil{return err};if policy.Vendor!="none"{return ErrConflict};state="no-vendor"}
		body,_:=json.Marshal([]string{d.BranchID,hash([]byte(user)),d.PayloadSHA,d.PolicySHA,reason,evidenceSHA})
		row:=kafkaVendorReceipt{ID:key(s.epoch,"kafka-vendor",d.BranchID+"\x00"+hash([]byte(user))),Kind:"kafka-vendor-v1",Epoch:s.epoch,Branch:d.BranchID,PolicySHA:d.PolicySHA,UserSHA:hash([]byte(user)),PayloadSHA:d.PayloadSHA,RequestSHA:hash(body),State:state,Revision:1,AttemptID:attempt.String(),InstanceID:s.instanceID,OutcomeSHA:evidenceSHA}
		if err:=s.fenceKafkaVendorParent(tx,d,call);err!=nil{return err}
		if old!=nil{
			if old.Revision>=math.MaxInt64-1{return ErrConflict};row.Revision=old.Revision+1
			history,_:=json.Marshal([]string{old.HistorySHA,old.RequestSHA,old.AttemptID,old.InstanceID,old.State,old.OutcomeSHA});row.HistorySHA=hash(history)
			changed,err:=s.db.Collection(Effects).ReplaceOne(tx,bson.M{"_id":old.ID,"revision":old.Revision,"state":old.State,"accounted":false},row)
			if err!=nil{return err};if changed.MatchedCount!=1 || changed.ModifiedCount!=1{return ErrConflict};return nil
		}
		_,err=s.db.Collection(Effects).InsertOne(tx,row);return err
	})
}

// Only the owned adapter can record a configured no-vendor policy; nil SDK
// clients or aggregate failures must not be converted to this terminal state.
func (s *Store) RecordKafkaNoVendor(ctx context.Context,call KafkaOfflineCall,user string)error{
	if err:=s.ValidateKafkaOfflineCall(ctx,call,user);err!=nil{return err}
	return s.recordKafkaOfflineSuppressed(ctx,call,user,"configured-no-vendor",call.PolicySHA256)
}

func (s *Store) fenceKafkaVendorParent(ctx context.Context,d deliveryDuty,call KafkaOfflineCall)error{
	if d.VendorRevision>=math.MaxInt64-1{return ErrConflict}
	filter:=bson.M{"_id":d.ID,"epoch":s.epoch,"revision":d.Revision,"offline_claim":call.ClaimID,"offline_instance":s.instanceID,"hidden":false,"payload_sha256":d.PayloadSHA}
	if d.VendorRevision==0{filter["$or"]=bson.A{bson.M{"vendor_revision":int64(0)},bson.M{"vendor_revision":bson.M{"$exists":false}}}}else{filter["vendor_revision"]=d.VendorRevision}
	changed,err:=s.db.Collection(Effects).UpdateOne(ctx,filter,bson.M{"$inc":bson.M{"vendor_revision":int64(1)}})
	if err!=nil{return err};if changed.MatchedCount!=1 || changed.ModifiedCount!=1{return ErrConflict};return nil
}

type KafkaVendorDispatch struct{store *Store;row kafkaVendorReceipt}

func (s *Store) BeginKafkaVendor(ctx context.Context,call KafkaOfflineCall,user string,input KafkaVendorRequest)(*KafkaVendorDispatch,error){
	if s==nil || ctx==nil || mongo.SessionFromContext(ctx)!=nil || !validKafkaVendorInput(input) || input.Protocol!=""{return nil,ErrInvalid}
	input.DeviceSHA256=append([]string(nil),input.DeviceSHA256...)
	attempt,err:=uuid.NewRandom();if err!=nil{return nil,ErrUnprepared}
	var claimed kafkaVendorReceipt;dispatch:=false
	err=s.transaction(ctx,func(tx context.Context)error{
		dispatch=false
		if err:=s.ValidateKafkaOfflineCall(tx,call,user);err!=nil{return err}
		d,err:=s.readDutyRecord(tx,call.BranchID);if err!=nil{return err}
		policy,err:=s.readKafkaPolicy(tx,d.PolicySHA);if err!=nil{return err}
		if policy.Vendor!=input.Vendor || policy.VendorBinding!=input.Binding{return ErrConflict}
		// Frozen offline callback output/recipient capability is a still-required
		// layer. Enabled policies remain visibly unprepared until it is wired;
		// this is not permission to disable callbacks for runtime acceptance.
		if policy.OfflinePushBinding!=""{return ErrUnprepared}
		body,err:=json.Marshal([]any{d.Epoch,d.BranchID,d.CID,hash([]byte(user)),d.PayloadSHA,d.PolicySHA,input});if err!=nil{return ErrInvalid}
		requestSHA:=hash(body)
		old,err:=s.readKafkaVendorReceipt(tx,d,user);if err!=nil{return err}
		if old!=nil{
			if kafkaVendorTerminal(old.State){return nil}
			if old.State!="pending-not-dispatched" && old.State!="pending-confirmed"{return ErrKafkaOfflineUnconfirmed}
		}
		if err:=s.fenceKafkaVendorParent(tx,d,call);err!=nil{return err}
		claimed=kafkaVendorReceipt{ID:key(s.epoch,"kafka-vendor",d.BranchID+"\x00"+hash([]byte(user))),Kind:"kafka-vendor-v1",Epoch:s.epoch,Branch:d.BranchID,PolicySHA:d.PolicySHA,UserSHA:hash([]byte(user)),PayloadSHA:d.PayloadSHA,Vendor:input.Vendor,Binding:input.Binding,RequestSHA:requestSHA,Input:input,State:"dispatching",Revision:1,AttemptID:attempt.String(),InstanceID:s.instanceID}
		if old!=nil{
			if old.Revision>=math.MaxInt64-1{return ErrConflict}
			claimed.Revision=old.Revision+1
			// A known zero-dispatch/independently confirmed non-application can
			// refreeze current tokens/reset without replaying the old request.
			// The exact previous attempt is retained in a hash chain.
			history,_:=json.Marshal([]string{old.HistorySHA,old.RequestSHA,old.AttemptID,old.InstanceID,old.State,old.OutcomeSHA})
			claimed.HistorySHA=hash(history)
			changed,err:=s.db.Collection(Effects).ReplaceOne(tx,bson.M{"_id":old.ID,"revision":old.Revision,"state":old.State,"request_sha256":old.RequestSHA,"accounted":false},claimed)
			if err!=nil{return err};if changed.MatchedCount!=1 || changed.ModifiedCount!=1{return ErrConflict}
		}else{if _,err:=s.db.Collection(Effects).InsertOne(tx,claimed);err!=nil{return err}}
		dispatch=true;return nil
	})
	if err!=nil{return nil,err};if !dispatch{return nil,nil};return &KafkaVendorDispatch{store:s,row:claimed},nil
}

func (d *KafkaVendorDispatch) BeforeProvider(ctx context.Context,call KafkaOfflineCall,user,providerRequestSHA string)error{
	if d==nil || d.store==nil || providerRequestSHA!=d.row.Input.ProviderRequestSHA256 || hash([]byte(user))!=d.row.UserSHA{return ErrInvalid}
	if err:=d.store.ValidateKafkaOfflineCall(ctx,call,user);err!=nil{return err}
	var current kafkaVendorReceipt
	if err:=d.store.db.Collection(Effects).FindOne(ctx,bson.M{"_id":d.row.ID}).Decode(&current);err!=nil{return err}
	if !validKafkaVendorReceipt(current) || current.Revision!=d.row.Revision || current.State!="dispatching" || current.AttemptID!=d.row.AttemptID || current.InstanceID!=d.row.InstanceID || current.RequestSHA!=d.row.RequestSHA{return ErrConflict}
	return nil
}

func (d *KafkaVendorDispatch) complete(parent context.Context,state,proof string,results []KafkaVendorDeviceResult)error{
	if d==nil || d.store==nil || parent==nil || !validSHA(proof){return ErrInvalid}
	current,cancel:=context.WithTimeout(context.WithoutCancel(parent),5*time.Second);defer cancel()
	changed,err:=d.store.db.Collection(Effects).UpdateOne(current,bson.M{"_id":d.row.ID,"epoch":d.row.Epoch,"revision":d.row.Revision,"state":"dispatching","attempt_id":d.row.AttemptID,"instance_id":d.row.InstanceID,"request_sha256":d.row.RequestSHA,"accounted":false},bson.M{"$set":bson.M{"state":state,"outcome_sha256":proof,"device_results":results},"$inc":bson.M{"revision":int64(1)}})
	if err!=nil || changed.MatchedCount!=1 || changed.ModifiedCount!=1{return ErrKafkaOfflineUnconfirmed}
	if state=="unconfirmed"{return ErrKafkaOfflineUnconfirmed};return nil
}

func (d *KafkaVendorDispatch) NotDispatched(ctx context.Context)error{return d.complete(ctx,"pending-not-dispatched",hash([]byte("owned provider adapter confirms zero invocation")),nil)}

func (d *KafkaVendorDispatch) Finish(ctx context.Context,report KafkaVendorReport)error{
	if d==nil || d.store==nil{return ErrInvalid}
	wanted:=map[string]bool{};for _,device:=range d.row.Input.DeviceSHA256{wanted[device]=true}
	seen:=map[string]bool{};known:=make([]KafkaVendorDeviceResult,0,len(report.Results));valid:=true
	for _,result:=range report.Results{
		if !wanted[result.DeviceSHA256] || seen[result.DeviceSHA256] || result.KnownRejection && result.ProviderReceiptSHA256!="" || result.ProviderReceiptSHA256!="" && !validSHA(result.ProviderReceiptSHA256){valid=false;break}
		seen[result.DeviceSHA256]=true
		if result.KnownRejection || result.ProviderReceiptSHA256!=""{known=append(known,result)}
	}
	state:="unconfirmed"
	if valid && report.NoRegisteredToken && !report.DispatchStarted && len(wanted)==0 && len(report.Results)==0{state="no-registered-token"}
	if valid && !report.NoRegisteredToken && report.DispatchStarted && len(wanted)>0 && len(known)==len(wanted){state="completed"}
	if !valid{known=nil}
	proof,_:=json.Marshal([]any{report.DispatchStarted,report.NoRegisteredToken,valid,known})
	return d.complete(ctx,state,hash(proof),known)
}
