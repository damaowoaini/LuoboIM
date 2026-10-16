package messagedelivery

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/proto"
)

var ErrKafkaOfflineUnconfirmed=errors.New("ordinary offline obligation unconfirmed; inspect original recipient receipt")
var ErrKafkaOfflineBusy=errors.New("ordinary offline page busy or online routing incomplete")

type KafkaOfflineCall struct {
	BranchID,ConversationID,PolicySHA256,ClaimID string
	ClaimRevision int64
	Message *sdkws.MsgData
	Recipients []string
	DispatchRecipients []string
}
type KafkaOfflineTransport interface {
	PolicyFingerprint()string
	CheckReady(context.Context)error
	DeliverOffline(context.Context,KafkaOfflineCall)error
}

type offlineAttempt struct{store *Store;duty deliveryDuty;claim string;targets []string}

func (s *Store) beginKafkaOffline(ctx context.Context,branch string)(*offlineAttempt,error){
	d,err:=s.readDuty(ctx,branch);if err!=nil{return nil,err}
	if !d.OfflinePending{return nil,nil}
	if !d.Persist || d.Seq<1 || d.Hidden || d.OnlinePending || d.MaxPending || d.SenderReadPending{return nil,ErrKafkaOfflineBusy}
	if d.OfflineClaim!="" && time.Now().Before(d.OfflineLeaseUntil){return nil,ErrKafkaOfflineBusy}
	if d.OfflineAttempts>=math.MaxInt64 || d.OfflineCursor>=d.RecipientCount{return nil,ErrConflict}
	end:=min(d.OfflineCursor+64,d.RecipientCount)
	claim,err:=uuid.NewRandom();if err!=nil{return nil,ErrUnprepared}
	filter:=bson.M{"_id":d.ID,"epoch":s.epoch,"revision":d.Revision,"offline_pending":true,"hidden":false}
	changed,err:=s.db.Collection(Effects).UpdateOne(ctx,filter,bson.M{"$set":bson.M{"offline_claim":claim.String(),"offline_instance":s.instanceID,"offline_lease_until":time.Now().UTC().Add(20*time.Second)},"$inc":bson.M{"revision":int64(1),"offline_attempts":int64(1)}})
	if err!=nil{return nil,ErrKafkaOfflineUnconfirmed};if changed.MatchedCount!=1 || changed.ModifiedCount!=1{return nil,ErrKafkaOfflineBusy}
	d.Revision++;d.OfflineClaim=claim.String();d.OfflineInstance=s.instanceID
	return &offlineAttempt{store:s,duty:d,claim:claim.String(),targets:append([]string(nil),d.Recipients[d.OfflineCursor:end]...)},nil
}

// The owned worker enters with the exact frozen adapter. It does not recreate
// a message, change source IDs, allocate a second sequence, or use a callback
// response to acquire member/request write authority. Seq0 has a distinct
// non-replay contract and remains outside this positive-sequence worker.
func (s *Store) RecoverKafkaOffline(ctx context.Context,branch string,reader VisibilityReader,transport KafkaOfflineTransport)error{
	if s==nil || !s.membershipRequired || ctx==nil || reader==nil || transport==nil || mongo.SessionFromContext(ctx)!=nil{return ErrInvalid}
	current,cancel:=context.WithTimeout(ctx,15*time.Second);defer cancel()
	if err:=s.CheckKafkaReady(current);err!=nil{return err}
	if err:=transport.CheckReady(current);err!=nil{return err}
	d,err:=s.readDutyRecord(current,branch);if err!=nil{return err}
	if d.ProducerVersion!="ordinary-kafka-v1" || transport.PolicyFingerprint()!=d.PolicySHA{return ErrUnprepared}
	current=context.WithValue(current,kafkaRecoveryKey{},kafkaRecoveryScope{store:s,branch:branch,policy:d.PolicySHA})
	if err:=s.RefreshVisibility(current,branch);err!=nil{return err}
	a,err:=s.beginKafkaOffline(current,branch);if err!=nil || a==nil{return err}
	var message sdkws.MsgData
	if proto.Unmarshal(a.duty.Payload,&message)!=nil || message.Seq!=a.duty.Seq{return a.finish(current,ErrConflict)}
	call:=KafkaOfflineCall{BranchID:branch,ConversationID:a.duty.CID,PolicySHA256:a.duty.PolicySHA,ClaimID:a.claim,ClaimRevision:a.duty.Revision,Message:proto.Clone(&message).(*sdkws.MsgData),Recipients:append([]string(nil),a.targets...)}
	var failures []error
	for _,uid:=range a.targets{
		terminal,err:=s.readKafkaVendorReceipt(current,a.duty,uid)
		if err!=nil{failures=append(failures,err);continue}
		if terminal!=nil && kafkaVendorTerminal(terminal.State){continue}
		if terminal!=nil && (terminal.State=="dispatching" || terminal.State=="unconfirmed"){failures=append(failures,ErrKafkaOfflineUnconfirmed);continue}
		route,known:=a.duty.OfflineRouting[hash([]byte(uid))]
		if !known{failures=append(failures,ErrKafkaOfflineUnconfirmed);continue}
		denied:=false;for _,h:=range a.duty.VisibilityDenied{if h==hash([]byte(uid)){denied=true}}
		if !route.Required || denied{
			if err:=s.recordKafkaOfflineSuppressed(current,call,uid,"frozen-routing-or-visibility",route.ResponseSHA256);err!=nil{failures=append(failures,err)}
			continue
		}
		receive,optionProof,err:=s.readKafkaReceiveOption(current,a.duty,uid)
		if err!=nil{failures=append(failures,err);continue}
		if !receive{
			if err:=s.recordKafkaOfflineSuppressed(current,call,uid,"authoritative-receive-option",optionProof);err!=nil{failures=append(failures,err)}
			continue
		}
		_,_,visible,err:=reader.GetMsgBySeqs(current,uid,a.duty.CID,[]int64{a.duty.Seq})
		if err!=nil{failures=append(failures,err);continue}
		found:=false
		for _,candidate:=range visible{
			if candidate==nil{continue};stored,want:=proto.Clone(candidate).(*sdkws.MsgData),proto.Clone(&message).(*sdkws.MsgData);stored.IsRead=false;want.IsRead=false
			if proto.Equal(stored,want){found=true}
		}
		if !found{
			if err:=s.recordKafkaOfflineSuppressed(current,call,uid,"authoritative-owner-visibility",a.duty.PayloadSHA);err!=nil{failures=append(failures,err)}
			continue
		}
		call.DispatchRecipients=append(call.DispatchRecipients,uid)
	}
	// The adapter resolves each exact recipient receipt BEFORE token reads,
	// badge accounting or vendor dispatch. Unknown rows cannot be retried.
	if len(call.DispatchRecipients)>0{if err:=transport.DeliverOffline(current,call);err!=nil{failures=append(failures,err)}}
	return a.finish(current,errors.Join(failures...))
}

func (s *Store) boundKafkaOfflineDuty(ctx context.Context,call KafkaOfflineCall)(deliveryDuty,error){
	if s==nil || ctx==nil || call.Message==nil || !validUUID(call.ClaimID) || call.ClaimRevision<1 || len(call.Recipients)==0 || len(call.Recipients)>64{return deliveryDuty{},ErrInvalid}
	d,err:=s.readDutyRecord(ctx,call.BranchID);if err!=nil{return deliveryDuty{},err}
	if !s.kafkaRecoveryAuthorized(ctx,d) || d.PolicySHA!=call.PolicySHA256 || d.CID!=call.ConversationID || d.Revision!=call.ClaimRevision || d.OfflineClaim!=call.ClaimID || d.OfflineInstance!=s.instanceID || d.Hidden || !d.OfflinePending || d.OnlinePending{return deliveryDuty{},ErrConflict}
	body,err:=proto.MarshalOptions{Deterministic:true}.Marshal(call.Message);if err!=nil || hash(body)!=d.PayloadSHA{return deliveryDuty{},ErrConflict}
	end:=min(d.OfflineCursor+64,d.RecipientCount)
	if d.OfflineCursor>=end || len(call.Recipients)!=int(end-d.OfflineCursor){return deliveryDuty{},ErrConflict}
	for i,uid:=range call.Recipients{if uid!=d.Recipients[d.OfflineCursor+int64(i)]{return deliveryDuty{},ErrConflict}}
	return d,nil
}

func (s *Store) ValidateKafkaOfflineCall(ctx context.Context,call KafkaOfflineCall,user string)error{
	if !identity(user){return ErrInvalid}
	d,err:=s.boundKafkaOfflineDuty(ctx,call);if err!=nil{return err}
	wanted:=false;seen:=map[string]bool{};page:=map[string]bool{}
	for _,uid:=range call.Recipients{page[uid]=true}
	for _,uid:=range call.DispatchRecipients{if !page[uid] || seen[uid]{return ErrInvalid};seen[uid]=true;if uid==user{wanted=true}}
	if !wanted{return ErrInvalid}
	for _,h:=range d.VisibilityDenied{if h==hash([]byte(user)){return ErrConflict}}
	denied,err:=s.membershipPage(ctx,d.CID,[]string{user},d.Seq)
	if err!=nil{return err};if denied[user]{return ErrConflict}
	receive,_,err:=s.readKafkaReceiveOption(ctx,d,user);if err!=nil{return err};if !receive{return ErrConflict}
	return nil
}

// Match the existing group offline opt-out contract using an authoritative
// bounded read, never a cached list or a miss caused by a DB error. No row is
// the existing default receive behavior; a known nonzero/missing option row
// suppresses offline delivery. This method grants no conversation writes.
func (s *Store) readKafkaReceiveOption(ctx context.Context,d deliveryDuty,user string)(bool,string,error){
	var message sdkws.MsgData
	if proto.Unmarshal(d.Payload,&message)!=nil{return false,"",ErrConflict}
	if message.SessionType!=constant.SuperGroupChatType && message.SessionType!=constant.GroupChatType{return true,hash([]byte("existing direct-message offline policy")),nil}
	if message.ContentType==constant.SignalingNotification{return false,hash([]byte("existing group signaling offline exclusion")),nil}
	var raw bson.Raw
	err:=s.db.Collection("conversation").FindOne(ctx,bson.M{"conversation_id":d.CID,"owner_user_id":user}).Decode(&raw)
	if errors.Is(err,mongo.ErrNoDocuments){return true,hash([]byte("existing absent conversation receive default")),nil}
	if err!=nil{return false,"",err}
	value,err:=raw.LookupErr("recv_msg_opt")
	if err!=nil{return false,hash(raw),nil}
	opt,ok:=value.Int64OK();if !ok{small,valid:=value.Int32OK();if !valid{return false,"",ErrConflict};opt=int64(small)}
	return opt==int64(constant.ReceiveMessage),hash(raw),nil
}

func (a *offlineAttempt) finish(parent context.Context,deliveryErr error)error{
	if a==nil || a.store==nil{return ErrInvalid}
	current,cancel:=context.WithTimeout(context.WithoutCancel(parent),5*time.Second);defer cancel()
	pending:=false
	err:=a.store.transaction(current,func(tx context.Context)error{
		pending=false
		d,err:=a.store.readDutyRecord(tx,a.duty.BranchID);if err!=nil{return err}
		if d.Revision!=a.duty.Revision || d.OfflineClaim!=a.claim || d.OfflineInstance!=a.store.instanceID || d.Hidden{return ErrConflict}
		added:=int64(0)
		for _,uid:=range a.targets{
			row,err:=a.store.readKafkaVendorReceipt(tx,d,uid);if err!=nil{return err}
			if row==nil || !kafkaVendorTerminal(row.State){pending=true;continue}
			if row.Accounted{continue}
			changed,err:=a.store.db.Collection(Effects).UpdateOne(tx,bson.M{"_id":row.ID,"revision":row.Revision,"state":row.State,"accounted":false},bson.M{"$set":bson.M{"accounted":true},"$inc":bson.M{"revision":int64(1)}})
			if err!=nil{return err};if changed.MatchedCount!=1 || changed.ModifiedCount!=1{return ErrConflict};added++
		}
		count:=d.OfflineSettled+added
		if count<0 || count>d.RecipientCount{return ErrConflict}
		next:=d.OfflineCursor+int64(len(a.targets));if next==d.RecipientCount{next=0}
		set:=bson.M{"offline_cursor":next,"offline_settled":count}
		unset:=offlineClaimFields()
		if count==d.RecipientCount{
			set["offline_pending"]=false;set["push_pending"]=d.OnlinePending
			if !d.OnlinePending && !d.MaxPending && !d.SenderReadPending{set["state"]="done";unset["payload"]="";unset["recipients"]=""}
		}
		changed,err:=a.store.db.Collection(Effects).UpdateOne(tx,bson.M{"_id":d.ID,"epoch":a.store.epoch,"revision":d.Revision,"offline_claim":a.claim},bson.M{"$set":set,"$unset":unset,"$inc":bson.M{"revision":int64(1)}})
		if err!=nil{return err};if changed.MatchedCount!=1 || changed.ModifiedCount!=1{return ErrConflict};return nil
	})
	if err!=nil{return errors.Join(ErrKafkaOfflineUnconfirmed,err)}
	if pending || deliveryErr!=nil{return errors.Join(ErrKafkaOfflineUnconfirmed,deliveryErr)}
	return nil
}

func offlineClaimFields()bson.M{return bson.M{"offline_claim":"","offline_instance":"","offline_lease_until":""}}

func (s *Store) ListKafkaOfflinePending(ctx context.Context,after string,limit int64)([]KafkaOnlinePending,error){
	if s==nil || ctx==nil || !s.membershipRequired || mongo.SessionFromContext(ctx)!=nil || limit<1 || limit>32 || after!="" && !validSHA(after){return nil,ErrInvalid}
	current,cancel:=context.WithTimeout(ctx,5*time.Second);defer cancel()
	if err:=s.CheckKafkaReady(current);err!=nil{return nil,err}
	filter:=bson.M{"epoch":s.epoch,"producer_version":"ordinary-kafka-v1","offline_pending":true,"online_pending":false,"persist":true,"state":"ready"}
	if after!=""{filter["_id"]=bson.M{"$gt":after}}
	cursor,err:=s.db.Collection(Effects).Find(current,filter,options.Find().SetSort(bson.D{{Key:"_id",Value:1}}).SetLimit(limit).SetProjection(bson.M{"_id":1,"branch_id":1,"policy_sha256":1}))
	if err!=nil{return nil,err}
	var out []KafkaOnlinePending
	for cursor.Next(current){
		var row struct{ID string `bson:"_id"`;Branch string `bson:"branch_id"`;Policy string `bson:"policy_sha256"`}
		if cursor.Decode(&row)!=nil || !validSHA(row.ID) || !validUUID(row.Branch) || !validSHA(row.Policy) || row.ID!=key(s.epoch,"branch",row.Branch){err=ErrConflict;break}
		out=append(out,KafkaOnlinePending{BranchID:row.Branch,PolicySHA256:row.Policy,Cursor:row.ID})
	}
	if err==nil{err=cursor.Err()};closeErr:=cursor.Close(current)
	if err!=nil || closeErr!=nil{return nil,errors.Join(err,closeErr)};return out,nil
}
