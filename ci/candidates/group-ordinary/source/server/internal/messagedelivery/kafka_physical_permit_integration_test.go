package messagedelivery

import (
 "context"
 "errors"
 "strings"
 "sync"
 "sync/atomic"
 "testing"
 "time"

 "github.com/OpenIMSDK/protocol/constant"
 "github.com/OpenIMSDK/protocol/sdkws"
 "github.com/google/uuid"
 "github.com/openimsdk/open-im-server/v3/internal/groupauthority"
 table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
 "github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
 "go.mongodb.org/mongo-driver/bson"
 "go.mongodb.org/mongo-driver/event"
 "go.mongodb.org/mongo-driver/mongo"
 "go.mongodb.org/mongo-driver/mongo/options"
 "google.golang.org/protobuf/proto"
)

type physicalPermitBarrierKey struct{}
type physicalPermitProbe struct {id string;admit func(context.Context)error;notDispatched func(context.Context)error}
type physicalPermitTransport struct {
 store *Store;policy string
 prepare func(context.Context,KafkaOfflineCall,string)(physicalPermitProbe,error)
 perform func(context.Context,physicalPermitProbe)error
}
func (a *physicalPermitTransport) PolicyFingerprint()string{return a.policy}
func (a *physicalPermitTransport) CheckReady(ctx context.Context)error{return a.store.CheckKafkaReady(ctx)}
func (a *physicalPermitTransport) DeliverOffline(ctx context.Context,call KafkaOfflineCall)error{
 for _,user:=range call.DispatchRecipients{probe,err:=a.prepare(ctx,call,user);if err!=nil{return err};return a.perform(ctx,probe)}
 return ErrConflict
}

// Uses the exact existing ephemeral RS nonce/claim/drop/disconnect fixture.
// The optional wire adapter only discards synthetic commit replies; it cannot
// contact any endpoint beyond that fixture. No supplier token/body is emitted.
func physicalPermitFixture(t *testing.T,kind string,monitor *event.CommandMonitor,dialer options.ContextDialer)(context.Context,*Store,*mongo.Database,string,*sdkws.MsgData,*physicalPermitTransport){
 t.Helper();ctx,authority,base,db:=sequenceMongoTransport(t,monitor,dialer)
 if _,err:=db.Collection("group").InsertOne(ctx,bson.M{"group_id":"g","status":int32(constant.GroupOk),"group_type":int32(constant.WorkingGroup),groupauthority.RevisionField:int64(1)});err!=nil{t.Fatal(err)}
 for uid,role:=range map[string]int32{"owner":constant.GroupOwner,"member":constant.GroupOrdinaryUsers}{
  if _,err:=db.Collection("group_member").InsertOne(ctx,bson.M{"group_id":"g","user_id":uid,"role_level":role,"join_time":time.Unix(1,0).UTC()});err!=nil{t.Fatal(err)}
 }
 for _,collection:=range []string{Sequences,Receipts}{
  if _,err:=db.Collection(collection).InsertOne(ctx,bson.M{"_id":MembershipSchemaID,"version":int32(1),"epoch":base.epoch,"activation_sha256":base.activationSHA,"legacy_message_writers_retired":true,"legacy_group_writers_retired":true,"owner_bounds_seeded":true,"readers_bound":true});err!=nil{t.Fatal(err)}
 }
 for _,uid:=range []string{"member","owner"}{window:=boundaryWindowFixture();window.Epoch,window.UserID=base.epoch,uid;window.ID=key(base.epoch,"membership-window","sg_g\x00"+uid);if _,err:=db.Collection(Sequences).InsertOne(ctx,window);err!=nil{t.Fatal(err)}}
 initializeSequence(t,ctx,authority,base,"sg_g",0)
 store,err:=NewMembershipBoundStore(db,base.epoch,base.activationSHA);if err!=nil{t.Fatal(err)}
 ctx=context.WithValue(ctx,constant.OpUserID,"owner");record:=kafkaFixtureRecord(t,81)
 var message sdkws.MsgData;if proto.Unmarshal(record.Body,&message)!=nil{t.Fatal("synthetic source invalid")}
 message.Options=msgprocessor.WithOptions(message.Options,msgprocessor.WithOfflinePush(true));record.Body,err=proto.MarshalOptions{Deterministic:true}.Marshal(&message);if err!=nil{t.Fatal(err)}
 policy,_:=kafkaPolicyFixture(t,store);policy.Vendor="getui";if kind=="fcm"{policy.Vendor="fcm"};policy.VendorBinding=hash([]byte("synthetic exact owned binding "+kind))
 fingerprint,err:=policy.Fingerprint();if err!=nil{t.Fatal(err)};accepted,err:=store.AcceptKafkaRecord(ctx,record,fingerprint,policy)
 if err!=nil || len(accepted.Branches)!=1{t.Fatal("original source not committed",err)};branch:=accepted.Branches[0].ID;message.Seq=accepted.Branches[0].Seq
 if err:=store.RecoverKafkaOnline(ctx,branch,&staleBodyReader{message:&message},discardProjection{},&kafkaOnlineFixtureTransport{policy:fingerprint});err!=nil{t.Fatal("original online branch not settled",err)}
 adapter:=&physicalPermitTransport{store:store,policy:fingerprint}
 adapter.prepare=func(current context.Context,call KafkaOfflineCall,user string)(physicalPermitProbe,error){
  switch kind{
  case "alias":
   input:=KafkaAliasRequest{Vendor:"getui",Binding:policy.VendorBinding,Protocol:"getui-alias-v2",RequestID:strings.ReplaceAll(uuid.NewString(),"-",""),BodySHA256:hash([]byte("synthetic original alias body"))}
   dispatch,err:=adapter.store.BeginKafkaAliasVendor(current,call,user,input);if err!=nil{return physicalPermitProbe{},err};if dispatch==nil{return physicalPermitProbe{},ErrConflict}
   return physicalPermitProbe{id:dispatch.row.ID,admit:func(ctx context.Context)error{return dispatch.BeforeProvider(ctx,call,user,input.BodySHA256)},notDispatched:dispatch.NotDispatched},nil
  case "generic":
   input:=KafkaVendorRequest{Vendor:"getui",Binding:policy.VendorBinding,ProviderRequestSHA256:hash([]byte("synthetic original legacy body")),TokenSetSHA256:hash([]byte("synthetic empty legacy set"))}
   dispatch,err:=adapter.store.BeginKafkaVendor(current,call,user,input);if err!=nil{return physicalPermitProbe{},err};if dispatch==nil{return physicalPermitProbe{},ErrConflict}
   return physicalPermitProbe{id:dispatch.row.ID,admit:func(ctx context.Context)error{return dispatch.BeforeProvider(ctx,call,user,input.ProviderRequestSHA256)},notDispatched:dispatch.NotDispatched},nil
  case "fcm":
   input:=KafkaVendorRequest{Vendor:"fcm",Binding:policy.VendorBinding,TokenSetSHA256:hash([]byte("synthetic original token set")),Protocol:"fcm-http-v1-device-v1",DeviceSHA256:[]string{hash([]byte("synthetic original device"))},DeviceRequestSHA256:[]string{hash([]byte("synthetic original device body"))},Badge:7,BadgeLedgerValue:7,BadgeResetID:"0123456789abcdef0123456789abcdef",BadgeReceiptSHA256:hash([]byte("synthetic badge receipt"))}
   input.ProviderRequestSHA256,_=FCMManifestFingerprint(input)
   manifest,err:=adapter.store.FreezeKafkaFCMManifest(current,call,user,input);if err!=nil{return physicalPermitProbe{},err}
   dispatch,err:=manifest.BeginDevice(current,call,user,input.DeviceSHA256[0],input.DeviceRequestSHA256[0]);if err!=nil{return physicalPermitProbe{},err};if dispatch==nil{return physicalPermitProbe{},ErrConflict}
   return physicalPermitProbe{id:dispatch.row.ID,admit:func(ctx context.Context)error{return dispatch.BeforeProvider(ctx,call,user,input.DeviceRequestSHA256[0])},notDispatched:func(ctx context.Context)error{return dispatch.NotDispatched(ctx,hash([]byte("synthetic owned zero physical transport")))}},nil
  };return physicalPermitProbe{},ErrInvalid
 }
 return ctx,store,db,branch,&message,adapter
}
func physicalPermitClear(ctx context.Context,store *Store,db *mongo.Database)error{
 return store.MutateMessageVisibility(ctx,db,[]table.PendingVisibilityChange{{ConversationID:"sg_g",Below:2}},func(tx context.Context)error{
  changed,err:=db.Collection("msg").UpdateOne(tx,bson.M{"doc_id":"sg_g:0"},bson.M{"$set":bson.M{"msgs.0.msg":nil,"msgs.0.global_deleted":true}})
  if err!=nil{return err};if changed.MatchedCount!=1 || changed.ModifiedCount!=1{return ErrConflict};return nil
 })
}
func physicalPermitNoPlaintext(t *testing.T,ctx context.Context,db *mongo.Database,store *Store,branch string){
 t.Helper();var duty deliveryDuty
 if err:=db.Collection(Effects).FindOne(ctx,bson.M{"_id":key(store.epoch,"branch",branch)}).Decode(&duty);err!=nil || !duty.Hidden || len(duty.Payload)!=0 || len(duty.Recipients)!=0{t.Fatal("clear rebuilt or retained sensitive duty",err)}
}

// Shared before/after behavioral case. Before is expected to fail ONLY the
// zero-permit/zero-POST assertion; the setup and real Clear ACK must succeed.
func TestKafkaPhysicalPermitMongoClearBeforeFinalAdmissionNeverGrantsPOST(t *testing.T){
 for _,kind:=range []string{"alias","generic","fcm"}{t.Run(kind,func(t *testing.T){
  arrived,release,done:=make(chan struct{}),make(chan struct{}),make(chan struct{})
  var once sync.Once;resume:=func(){once.Do(func(){close(release)})};defer resume()
  var paused atomic.Bool;var target atomic.Value;target.Store("")
  monitor:=&event.CommandMonitor{Started:func(ctx context.Context,event *event.CommandStartedEvent){
   if ctx.Value(physicalPermitBarrierKey{})!=true{return}
   name:=event.CommandName;collection,_:=event.Command.Lookup(name).StringValueOK();if collection!=Effects{return}
   match:=kind=="alias" && name=="update" // First write is old child CAS or new parent fence, both AFTER validation.
   if kind!="alias" && name=="find"{filter,ok:=event.Command.Lookup("filter").DocumentOK();if ok{id,_:=filter.Lookup("_id").StringValueOK();match=id==target.Load().(string)}}
   if match && paused.CompareAndSwap(false,true){close(arrived);select{case <-release:case <-ctx.Done():}}
  }}
  ctx,store,db,branch,message,adapter:=physicalPermitFixture(t,kind,monitor,nil)
  work,cancel:=context.WithCancel(ctx);var posts,grants atomic.Int64;result:=make(chan error,1)
  t.Cleanup(func(){cancel();resume();select{case <-done:case <-time.After(3*time.Second):ctx.Value(sequenceFixtureOwnershipKey{}).(*sequenceFixtureOwnership).dropAllowed.Store(false);t.Error("owned permit probe did not join; preserve nonce DB")}})
  adapter.perform=func(current context.Context,probe physicalPermitProbe)error{target.Store(probe.id);if err:=probe.admit(context.WithValue(current,physicalPermitBarrierKey{},true));err==nil{grants.Add(1);posts.Add(1)};return ErrKafkaOfflineUnconfirmed}
  go func(){defer close(done);result<-store.RecoverKafkaOffline(work,branch,&staleBodyReader{message:message},adapter)}()
  select{case <-arrived:case <-time.After(3*time.Second):t.Fatal("real final validation boundary not reached")}
  if err:=physicalPermitClear(ctx,store,db);err!=nil{t.Fatal("real Clear did not commit before final permit",err)}
  resume();select{case err:=<-result:if err==nil{t.Fatal("redacted duty recovery falsely succeeded")};case <-time.After(3*time.Second):t.Fatal("permit probe did not finish")}
  physicalPermitNoPlaintext(t,ctx,db,store,branch)
  if grants.Load()!=0 || posts.Load()!=0{t.Fatal("Clear committed before admission but old plaintext acquired a physical POST permit")}
 })}
}
func TestKafkaPhysicalPermitMongoKnownCommitIsOneStartedAttemptEvenIfClearCommitsLater(t *testing.T){
 for _,kind:=range []string{"alias","generic","fcm"}{t.Run(kind,func(t *testing.T){
  ctx,store,db,branch,message,adapter:=physicalPermitFixture(t,kind,nil,nil);posts:=0
  adapter.perform=func(current context.Context,probe physicalPermitProbe)error{
   if err:=probe.admit(current);err!=nil{return err}
   if err:=physicalPermitClear(current,store,db);err!=nil{return err}
   // Already-started means the known permit COMMIT, not a promise that Clear
   // can cancel a socket. This fake supplier boundary is never a real network.
   posts++
   if err:=probe.admit(current);!errors.Is(err,ErrConflict){return errors.New("one attempt granted more than one local permit")}
   var raw bson.M;if err:=db.Collection(Effects).FindOne(current,bson.M{"_id":probe.id}).Decode(&raw);err!=nil{return err}
   if raw["state"]!="dispatching"{return ErrConflict}
   return ErrKafkaOfflineUnconfirmed
  }
  if err:=store.RecoverKafkaOffline(ctx,branch,&staleBodyReader{message:message},adapter);err==nil || posts!=1{t.Fatal("known started attempt was misreported/replayed",err)}
  physicalPermitNoPlaintext(t,ctx,db,store,branch)
 })}
}
func TestKafkaPhysicalPermitMongoLostCommitACKDoesNotGrantHTTPOrReplayAfterRestart(t *testing.T){
 for _,kind:=range []string{"alias","generic","fcm"}{t.Run(kind,func(t *testing.T){
  drop:=&commitReplyDropper{requests:map[int32]bool{},all:true};base:=drop.monitor()
  var mu sync.Mutex;var updates []deliveryWireFact
  monitor:=&event.CommandMonitor{Started:func(ctx context.Context,event *event.CommandStartedEvent){
   base.Started(ctx,event)
   if ctx.Value(deliveryDropCommitKey{})!=true || event.CommandName!="update"{return};collection,_:=event.Command.Lookup("update").StringValueOK();if collection!=Effects{return}
   auto,ok:=event.Command.Lookup("autocommit").BooleanOK();txn,txOK:=event.Command.Lookup("txnNumber").Int64OK();lsid,lsOK:=event.Command.Lookup("lsid").DocumentOK()
   mu.Lock();updates=append(updates,deliveryWireFact{command:"update",transaction:ok && !auto && txOK && lsOK,txn:txn,lsidSHA:hash(lsid)});mu.Unlock()
  }}
  ctx,store,db,branch,message,adapter:=physicalPermitFixture(t,kind,monitor,drop);posts,permits:=0,0;originalID:=""
  adapter.perform=func(current context.Context,probe physicalPermitProbe)error{
   if originalID==""{originalID=probe.id};permits++;err:=probe.admit(context.WithValue(current,deliveryDropCommitKey{},true));if err==nil{posts++}
   if !errors.Is(err,ErrCommitUnconfirmed){return errors.New("lost commit did not preserve unknown attempt admission")}
   if err:=probe.notDispatched(current);!errors.Is(err,ErrKafkaOfflineUnconfirmed){return errors.New("unknown permission commit was rewritten as not-dispatched")};return ErrKafkaOfflineUnconfirmed
  }
  if err:=store.RecoverKafkaOffline(ctx,branch,&staleBodyReader{message:message},adapter);err==nil || posts!=0 || permits!=1 || drop.dropped.Load()<1{t.Fatal("unknown commit gave physical permission or fault was not exercised",err)}
  mu.Lock();facts:=append([]deliveryWireFact(nil),updates...);mu.Unlock();drop.mu.Lock();commits:=append([]deliveryWireFact(nil),drop.facts...);drop.mu.Unlock()
  if len(facts)!=2 || len(commits)<1{t.Fatal("actual permit TX did not write exactly parent+child once")}
  for _,fact:=range append(facts,commits...){if !fact.transaction || fact.txn!=facts[0].txn || fact.lsidSHA!=facts[0].lsidSHA{t.Fatal("unknown commit reran work or changed actual transaction")}}
  restarted,err:=NewMembershipBoundStore(db,store.epoch,store.activationSHA);if err!=nil{t.Fatal(err)};adapter.store=restarted
  if err:=restarted.RecoverKafkaOffline(ctx,branch,&staleBodyReader{message:message},adapter);err==nil || posts!=0 || permits!=1{t.Fatal("restart retried an unknown permission commit",err)}
  // The DB may have committed even though no local HTTP permit was granted.
  // Neither restart nor failed HTTP admission rewrites that as not-dispatched.
  kindName:="kafka-vendor-v1";if kind=="alias"{kindName="kafka-alias-vendor-v1"};if kind=="fcm"{kindName="kafka-fcm-device-v1"}
  if !validSHA(originalID){t.Fatal("original attempted receipt identity was not captured")}
  var raw bson.M;if err:=db.Collection(Effects).FindOne(ctx,bson.M{"_id":originalID,"kind":kindName,"branch_id":branch}).Decode(&raw);err!=nil || raw["state"]!="dispatching"{t.Fatal("old admitted/unknown state was rewritten as unsent",err)}
  if kind!="alias" && raw["physical_permit"]!=true{t.Fatal("real commit did not preserve exact physical permission receipt")}
 })}
}
