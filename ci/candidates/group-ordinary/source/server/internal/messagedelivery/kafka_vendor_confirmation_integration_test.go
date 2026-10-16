package messagedelivery

import (
 "context"
 "errors"
 "sync"
 "sync/atomic"
 "testing"
 "time"

 "github.com/OpenIMSDK/protocol/sdkws"
 "github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
 "go.mongodb.org/mongo-driver/bson"
 "go.mongodb.org/mongo-driver/event"
 "google.golang.org/protobuf/proto"
)

type genericConfirmFixtureTransport struct {store *Store;policy,binding string}
func (a *genericConfirmFixtureTransport) PolicyFingerprint()string{return a.policy}
func (a *genericConfirmFixtureTransport) CheckReady(ctx context.Context)error{return a.store.CheckKafkaReady(ctx)}
func (a *genericConfirmFixtureTransport) DeliverOffline(ctx context.Context,call KafkaOfflineCall)error{
 for _,user:=range call.DispatchRecipients{
  input:=KafkaVendorRequest{Vendor:"getui",Binding:a.binding,ProviderRequestSHA256:hash([]byte("synthetic legacy compound request")),TokenSetSHA256:hash([]byte("synthetic original empty legacy device set"))}
  dispatch,err:=a.store.BeginKafkaVendor(ctx,call,user,input);if err!=nil{return err};if dispatch==nil{return ErrConflict}
  if err:=dispatch.BeforeProvider(ctx,call,user,input.ProviderRequestSHA256);err!=nil{return err}
  // Only a synthetic legacy aggregate boundary is exercised. There is no
  // supplier POST or claim that user-alias routing exposed a device manifest.
  return dispatch.Finish(ctx,KafkaVendorReport{DispatchStarted:true})
 };return ErrConflict
}
type aliasConfirmBarrierKey struct{}
func TestKafkaVendorMongoStaleOperatorCannotReplaceAlreadyConfirmedOriginalOutcome(t *testing.T){
 arrived,release,done:=make(chan struct{}),make(chan struct{}),make(chan struct{})
 var once sync.Once;resume:=func(){once.Do(func(){close(release)})};defer resume()
 var exactFinds atomic.Int64;var targetID string
 monitor:=&event.CommandMonitor{Started:func(ctx context.Context,command *event.CommandStartedEvent){
  if ctx.Value(aliasConfirmBarrierKey{})!=true || command.CommandName!="find"{return}
  collection,ok:=command.Command.Lookup("find").StringValueOK();if !ok || collection!=Effects{return}
  filter,ok:=command.Command.Lookup("filter").DocumentOK();if !ok{return};id,ok:=filter.Lookup("_id").StringValueOK();if !ok || id!=targetID{return}
  if exactFinds.Add(1)==2{close(arrived);select{case <-release:case <-ctx.Done():}}
 }}
 ctx,_,store,db,_:=kafkaFixture(t,monitor)
 record:=kafkaFixtureRecord(t,74)
 var message sdkws.MsgData
 if proto.Unmarshal(record.Body,&message)!=nil{t.Fatal("synthetic original source")}
 message.Options=msgprocessor.WithOptions(message.Options,msgprocessor.WithOfflinePush(true))
 var err error
 record.Body,err=proto.MarshalOptions{Deterministic:true}.Marshal(&message);if err!=nil{t.Fatal(err)}
 policy,_:=kafkaPolicyFixture(t,store);policy.Vendor="getui";policy.VendorBinding=hash([]byte("synthetic original legacy provider binding"))
 fingerprint,err:=policy.Fingerprint();if err!=nil{t.Fatal(err)}
 accepted,err:=store.AcceptKafkaRecord(ctx,record,fingerprint,policy)
 if err!=nil || len(accepted.Branches)!=1{t.Fatal("synthetic original source not committed",err)}
 branch:=accepted.Branches[0].ID;message.Seq=accepted.Branches[0].Seq
 if err:=store.RecoverKafkaOnline(ctx,branch,&staleBodyReader{message:&message},discardProjection{},&kafkaOnlineFixtureTransport{policy:fingerprint});err!=nil{t.Fatal("original online branch not settled",err)}
 targetID=key(store.epoch,"kafka-vendor",branch+"\x00"+hash([]byte("member")))
 transport:=&genericConfirmFixtureTransport{store:store,policy:fingerprint,binding:policy.VendorBinding}
 if err:=store.RecoverKafkaOffline(ctx,branch,&staleBodyReader{message:&message},transport);!errors.Is(err,ErrKafkaOfflineUnconfirmed){t.Fatal("synthetic original unknown legacy outcome not established",err)}
 expected,err:=store.InspectKafkaVendor(ctx,branch,"member");if err!=nil || expected.State!="unconfirmed"{t.Fatal("original receipt missing",err)}
 proof:=KafkaVendorConfirmation{Decision:"not-applied",EvidenceSHA256:hash([]byte("synthetic independent no-application proof")),Drain:KafkaAttemptDrainProof{InstanceID:expected.InstanceID,EvidenceSHA256:hash([]byte("synthetic original worker drained"))}}
 work,cancel:=context.WithCancel(context.WithValue(ctx,aliasConfirmBarrierKey{},true));result:=make(chan error,1)
 t.Cleanup(func(){cancel();resume();select{case <-done:case <-time.After(3*time.Second):ctx.Value(sequenceFixtureOwnershipKey{}).(*sequenceFixtureOwnership).dropAllowed.Store(false);t.Error("owned operator race did not join; preserving nonce database")}})
 go func(){defer close(done);result<-store.ConfirmKafkaVendor(work,"member",expected,proof)}()
 select{case <-arrived:case <-time.After(3*time.Second):t.Fatal("real operator did not reach its second receipt read")}
 known:=proof;known.Decision="rejected";known.EvidenceSHA256=hash([]byte("synthetic exact independent original outcome evidence"))
 if err:=store.ConfirmKafkaVendor(ctx,"member",expected,known);err!=nil{t.Fatal("other operator could not commit its original valid confirmation",err)}
 resume()
 select{case err:=<-result:if !errors.Is(err,ErrConflict){t.Fatal("stale expected receipt overwrote a newer confirmed outcome",err)};case <-time.After(3*time.Second):t.Fatal("original operator did not finish")}
 var row kafkaVendorReceipt
 if err:=db.Collection(Effects).FindOne(ctx,bson.M{"_id":targetID}).Decode(&row);err!=nil || row.State!="operator-rejected" || row.Revision!=expected.Revision+1{t.Fatal("newer exact confirmation was changed or made eligible for replay",err)}
}
