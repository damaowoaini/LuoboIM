package messagedelivery

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/sdkws"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

type fcmManifestFixtureTransport struct {
	store *Store
	policy string
	input KafkaVendorRequest
	perform func(context.Context,KafkaOfflineCall,string,*KafkaFCMManifest)error
}
func (f *fcmManifestFixtureTransport) PolicyFingerprint()string{return f.policy}
func (f *fcmManifestFixtureTransport) CheckReady(ctx context.Context)error{return f.store.CheckKafkaReady(ctx)}
func (f *fcmManifestFixtureTransport) DeliverOffline(ctx context.Context,call KafkaOfflineCall)error{
	var failures []error
	for _,user:=range call.DispatchRecipients{
		manifest,err:=f.store.ReadKafkaFCMManifest(ctx,call,user);if err!=nil{failures=append(failures,err);continue}
		if manifest==nil{manifest,err=f.store.FreezeKafkaFCMManifest(ctx,call,user,f.input);if err!=nil{failures=append(failures,err);continue}}
		if err:=f.perform(ctx,call,user,manifest);err!=nil{failures=append(failures,err)}
	}
	return errors.Join(failures...)
}

func fcmManifestFixture(t *testing.T,offset int64)(context.Context,*Store,*mongo.Database,string,*sdkws.MsgData,KafkaVendorRequest,string){
	t.Helper();ctx,_,s,db,_:=kafkaFixture(t,nil);record:=kafkaFixtureRecord(t,offset)
	var message sdkws.MsgData;if proto.Unmarshal(record.Body,&message)!=nil{t.Fatal("fixture message")}
	message.Options=msgprocessor.WithOptions(message.Options,msgprocessor.WithOfflinePush(true))
	var err error;record.Body,err=proto.MarshalOptions{Deterministic:true}.Marshal(&message);if err!=nil{t.Fatal(err)}
	snapshot,_:=kafkaPolicyFixture(t,s);snapshot.Vendor="fcm";snapshot.VendorBinding=hash([]byte("synthetic exact credential/project/protocol binding"))
	policy,err:=snapshot.Fingerprint();if err!=nil{t.Fatal(err)}
	accepted,err:=s.AcceptKafkaRecord(ctx,record,policy,snapshot);if err!=nil{t.Fatal(err)}
	message.Seq=accepted.Branches[0].Seq;branch:=accepted.Branches[0].ID
	if err:=s.RecoverKafkaOnline(ctx,branch,&staleBodyReader{message:&message},discardProjection{},&kafkaOnlineFixtureTransport{policy:policy});err!=nil{t.Fatal(err)}
	devices:=[]string{hash([]byte("synthetic device a")),hash([]byte("synthetic device b"))};sort.Strings(devices)
	input:=KafkaVendorRequest{Vendor:"fcm",Binding:snapshot.VendorBinding,TokenSetSHA256:hash([]byte("synthetic exact original token set")),Protocol:"fcm-http-v1-device-v1",DeviceSHA256:devices,Badge:7,BadgeLedgerValue:7,BadgeResetID:"0123456789abcdef0123456789abcdef",BadgeReceiptSHA256:hash([]byte("synthetic badge accounting receipt"))}
	for _,device:=range devices{input.DeviceRequestSHA256=append(input.DeviceRequestSHA256,hash([]byte("synthetic HTTP v1 frozen request:"+device)))}
	input.ProviderRequestSHA256,err=FCMManifestFingerprint(input);if err!=nil{t.Fatal(err)}
	return ctx,s,db,branch,&message,input,policy
}

// These tests use the real controlled RS/nonce DB and real recovery/transaction
// entrypoints. Provider results are synthetic, NOT vendor or device evidence.
func TestKafkaFCMDeviceMongoUnknownSiblingDoesNotBlockOtherDeviceOrReplayAfterRestart(t *testing.T){
	ctx,s,db,branch,message,input,policy:=fcmManifestFixture(t,51)
	posts:=map[string]int{};loseFirst:=true
	perform:=func(current context.Context,call KafkaOfflineCall,user string,m *KafkaFCMManifest)error{
		var failures []error
		for i,device:=range input.DeviceSHA256{
			dispatch,err:=m.BeginDevice(current,call,user,device,input.DeviceRequestSHA256[i]);if err!=nil{failures=append(failures,err);continue};if dispatch==nil{continue}
			if err:=dispatch.BeforeProvider(current,call,user,input.DeviceRequestSHA256[i]);err!=nil{t.Fatal(err)}
			if err:=dispatch.BeforeProvider(current,call,user,input.DeviceRequestSHA256[i]);!errors.Is(err,ErrConflict){t.Fatal("one device permit accepted a second physical attempt",err)}
			posts[device]++
			report:=KafkaFCMDeviceReport{DispatchStarted:true,OutcomeSHA256:hash([]byte("synthetic exact device HTTP outcome"))}
			if i!=0 || !loseFirst{report.ProviderReceiptSHA256=hash([]byte("synthetic provider receipt:"+device))}
			if err:=dispatch.Finish(current,report);err!=nil{failures=append(failures,err)}
		}
		if err:=m.Settle(current);err!=nil{failures=append(failures,err)}
		return errors.Join(failures...)
	}
	adapter:=&fcmManifestFixtureTransport{store:s,policy:policy,input:input,perform:perform}
	if err:=s.RecoverKafkaOffline(ctx,branch,&staleBodyReader{message:message},adapter);!errors.Is(err,ErrKafkaOfflineUnconfirmed){t.Fatal("one unknown device was called complete",err)}
	for _,device:=range input.DeviceSHA256{if posts[device]!=1{t.Fatal("one device blocked another independent device")}}
	restarted,err:=NewMembershipBoundStore(db,s.epoch,s.activationSHA);if err!=nil{t.Fatal(err)}
	adapter.store=restarted;adapter.input.TokenSetSHA256=hash([]byte("later token set must not replace original"))
	if err:=restarted.RecoverKafkaOffline(ctx,branch,&staleBodyReader{message:message},adapter);!errors.Is(err,ErrKafkaOfflineUnconfirmed){t.Fatal("restart inferred rollback of unknown device",err)}
	for _,device:=range input.DeviceSHA256{if posts[device]!=1{t.Fatal("restart replayed a device")}}
	unknown,err:=restarted.InspectKafkaFCMDevice(ctx,branch,"member",input.DeviceSHA256[0]);if err!=nil || unknown.State!="unconfirmed"{t.Fatal("unknown minimal device receipt missing",err)}
	known,err:=restarted.InspectKafkaFCMDevice(ctx,branch,"member",input.DeviceSHA256[1]);if err!=nil || known.State!="accepted"{t.Fatal("known sibling receipt lost",err)}
	drain:=KafkaAttemptDrainProof{InstanceID:unknown.InstanceID,EvidenceSHA256:hash([]byte("synthetic original instance drained"))}
	confirm:=KafkaFCMDeviceConfirmation{Decision:"not-applied",EvidenceSHA256:hash([]byte("independent exact synthetic device evidence")),Drain:drain}
	stale:=unknown;stale.Revision++
	if err:=restarted.ConfirmKafkaFCMDevice(ctx,"member",stale,confirm);!errors.Is(err,ErrConflict){t.Fatal("stale device inspect granted retry",err)}
	if err:=restarted.ConfirmKafkaFCMDevice(ctx,"member",unknown,confirm);err!=nil{t.Fatal(err)}
	loseFirst=false
	if err:=restarted.RecoverKafkaOffline(ctx,branch,&staleBodyReader{message:message},adapter);err!=nil{t.Fatal("exact confirmed original device could not settle",err)}
	if posts[input.DeviceSHA256[0]]!=2 || posts[input.DeviceSHA256[1]]!=1{t.Fatal("confirmation retried known sibling or lost original device")}
	var row kafkaVendorReceipt
	if err:=db.Collection(Effects).FindOne(ctx,bson.M{"_id":key(s.epoch,"kafka-vendor",branch+"\x00"+hash([]byte("member")))}).Decode(&row);err!=nil || row.State!="completed" || row.Input.TokenSetSHA256!=input.TokenSetSHA256 || !row.Accounted{t.Fatal("completion replaced manifest or did not account recipient exactly once",err)}
}

func TestKafkaFCMDeviceMongoConcurrentClaimAndManifestPayloadChangesCannotGrantTwoAttempts(t *testing.T){
	ctx,s,_,branch,message,input,policy:=fcmManifestFixture(t,52)
	adapter:=&fcmManifestFixtureTransport{store:s,policy:policy,input:input}
	adapter.perform=func(current context.Context,call KafkaOfflineCall,user string,m *KafkaFCMManifest)error{
		changed:=input;changed.Badge++
		var err error;changed.ProviderRequestSHA256,err=FCMManifestFingerprint(changed);if err!=nil{t.Fatal(err)}
		if _,err:=s.FreezeKafkaFCMManifest(current,call,user,changed);!errors.Is(err,ErrConflict){t.Fatal("changed payload/badge replaced immutable manifest",err)}
		work,cancel:=context.WithCancel(current);defer cancel()
		type result struct{dispatch *KafkaFCMDeviceDispatch;err error}
		results:=make(chan result,2);start,done:=make(chan struct{}),make(chan struct{})
		var workers sync.WaitGroup;workers.Add(2)
		t.Cleanup(func(){cancel();select{case <-done:case <-time.After(3*time.Second):ctx.Value(sequenceFixtureOwnershipKey{}).(*sequenceFixtureOwnership).dropAllowed.Store(false);t.Error("owned device claim workers did not drain; DB retained")}})
		for i:=0;i<2;i++{go func(){defer workers.Done();<-start;d,e:=m.BeginDevice(work,call,user,input.DeviceSHA256[0],input.DeviceRequestSHA256[0]);results<-result{d,e}}()}
		go func(){workers.Wait();close(done)}();close(start)
		claimed,unknown:=0,0
		for i:=0;i<2;i++{select{case result:=<-results:if result.err==nil && result.dispatch!=nil{claimed++}else if errors.Is(result.err,ErrKafkaOfflineUnconfirmed){unknown++}else{t.Fatal("unexpected real CAS result",result.err)};case <-current.Done():t.Fatal("concurrent device claims did not finish")}}
		if claimed!=1 || unknown!=1{t.Fatal("same frozen device gained more than one durable attempt",claimed,unknown)}
		return ErrKafkaOfflineUnconfirmed
	}
	if err:=s.RecoverKafkaOffline(ctx,branch,&staleBodyReader{message:message},adapter);!errors.Is(err,ErrKafkaOfflineUnconfirmed){t.Fatal(err)}
}

func TestKafkaFCMDeviceMongoClearAfterClaimPreventsPhysicalPOSTAndRetainsMinimalReceipt(t *testing.T){
	ctx,s,db,branch,message,input,policy:=fcmManifestFixture(t,53)
	physical:=0
	adapter:=&fcmManifestFixtureTransport{store:s,policy:policy,input:input}
	adapter.perform=func(current context.Context,call KafkaOfflineCall,user string,m *KafkaFCMManifest)error{
		dispatch,err:=m.BeginDevice(current,call,user,input.DeviceSHA256[0],input.DeviceRequestSHA256[0]);if err!=nil || dispatch==nil{t.Fatal(err)}
		if err:=s.MutateMessageVisibility(current,db,[]table.PendingVisibilityChange{{ConversationID:"sg_g",Below:2}},func(tx context.Context)error{
			changed,err:=db.Collection("msg").UpdateOne(tx,bson.M{"doc_id":"sg_g:0"},bson.M{"$set":bson.M{"msgs.0.msg":nil,"msgs.0.global_deleted":true}})
			if err!=nil{return err};if changed.MatchedCount!=1{return ErrConflict};return nil
		});err!=nil{t.Fatal(err)}
		if err:=dispatch.BeforeProvider(current,call,user,input.DeviceRequestSHA256[0]);err==nil{physical++}
		if err:=dispatch.NotDispatched(current,hash([]byte("owned transport zero physical POST")));err!=nil{t.Fatal(err)}
		return ErrKafkaOfflineUnconfirmed
	}
	if err:=s.RecoverKafkaOffline(ctx,branch,&staleBodyReader{message:message},adapter);err==nil || physical!=0{t.Fatal("cleared plaintext reached physical send",err)}
	summary,err:=s.InspectKafkaFCMDevice(ctx,branch,"member",input.DeviceSHA256[0]);if err!=nil || summary.State!="not-dispatched"{t.Fatal("zero-dispatch receipt unavailable after redaction",err)}
	var duty deliveryDuty;if err:=db.Collection(Effects).FindOne(ctx,bson.M{"_id":key(s.epoch,"branch",branch)}).Decode(&duty);err!=nil || !duty.Hidden || len(duty.Payload)!=0 || len(duty.Recipients)!=0{t.Fatal("device claim recreated redacted payload/roster",err)}
	var raw bson.M;if err:=db.Collection(Effects).FindOne(ctx,bson.M{"kind":"kafka-fcm-device-v1","device_sha256":input.DeviceSHA256[0]}).Decode(&raw);err!=nil{t.Fatal(err)}
	for _,forbidden:=range []string{"token","tokens","payload","message","users","recipients","url","private_key"}{if _,ok:=raw[forbidden];ok{t.Fatal("device receipt retained sensitive dispatch data")}}
}
