package msgtransfer

import (
 "context"
 "errors"
 "strings"
 "sync"
 "sync/atomic"
 "testing"
 "time"

 "github.com/OpenIMSDK/protocol/sdkws"
 "github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

type ownedWorkerStoreFixture struct {
 ready func(context.Context)error
 required []string
 rows func(context.Context,string,int64)([]messagedelivery.KafkaOnlinePending,error)
 recover func(context.Context,string,messagedelivery.VisibilityReader,messagedelivery.KafkaOfflineTransport)error
 lists atomic.Int64
}
func (s *ownedWorkerStoreFixture) CheckKafkaReady(ctx context.Context)error{if s.ready!=nil{return s.ready(ctx)};return nil}
func (s *ownedWorkerStoreFixture) RequiredKafkaOfflinePolicies(context.Context)([]string,error){return append([]string(nil),s.required...),nil}
func (s *ownedWorkerStoreFixture) ListKafkaOfflinePending(ctx context.Context,after string,limit int64)([]messagedelivery.KafkaOnlinePending,error){s.lists.Add(1);if s.rows!=nil{return s.rows(ctx,after,limit)};return nil,nil}
func (s *ownedWorkerStoreFixture) RecoverKafkaOffline(ctx context.Context,branch string,reader messagedelivery.VisibilityReader,transport messagedelivery.KafkaOfflineTransport)error{if s.recover!=nil{return s.recover(ctx,branch,reader,transport)};return nil}
func (*ownedWorkerStoreFixture) KafkaOfflineInstanceID()string{return "78ba17af-6daa-4220-9319-cf4dbe63af80"}
type ownedWorkerAdapterFixture struct {policy string;closed atomic.Int64;closeErr error;onClose func()}
func (a *ownedWorkerAdapterFixture) PolicyFingerprint()string{return a.policy}
func (*ownedWorkerAdapterFixture) CheckReady(context.Context)error{return nil}
func (*ownedWorkerAdapterFixture) DeliverOffline(context.Context,messagedelivery.KafkaOfflineCall)error{return nil}
func (a *ownedWorkerAdapterFixture) Close()error{a.closed.Add(1);if a.onClose!=nil{a.onClose()};return a.closeErr}
type ownedWorkerReaderFixture struct{}
func (ownedWorkerReaderFixture) GetMsgBySeqs(context.Context,string,string,[]int64)(int64,int64,[]*sdkws.MsgData,error){return 0,0,nil,nil}
func stopOwnedWorker(t *testing.T,w *ManagedKafkaOfflineWorker){t.Helper();ctx,cancel:=context.WithTimeout(context.Background(),2*time.Second);defer cancel();if err:=w.Stop(ctx);err!=nil{t.Fatal("owned worker did not drain",err)}}

func TestOwnedOfflineWorkerMissingRetainedPolicyRejectsStartupAndClosesOwnedPools(t *testing.T){
 adapter:=&ownedWorkerAdapterFixture{policy:strings.Repeat("a",64)}
 store:=&ownedWorkerStoreFixture{required:[]string{strings.Repeat("b",64)}}
 worker,err:=newManagedKafkaOfflineWorker(store,ownedWorkerReaderFixture{},[]OwnedKafkaOfflineTransport{adapter});if err!=nil{t.Fatal(err)}
 if err:=worker.Start(context.Background());!errors.Is(err,messagedelivery.ErrUnprepared) || store.lists.Load()!=0{t.Fatal("missing frozen policy started recovery",err)}
 stopOwnedWorker(t,worker)
 if adapter.closed.Load()!=1{t.Fatal("failed startup leaked or double closed owned pool")}
 if _,err:=worker.DrainSummary();err!=nil{t.Fatal("known no-start cleanup not inspectable",err)}
 if err:=worker.Start(context.Background());err==nil{t.Fatal("stopped instance restarted")}
}

func TestOwnedOfflineWorkerStopCancelsReadinessAndJoinsSameInstance(t *testing.T){
 entered:=make(chan struct{});returned:=make(chan error,1)
 adapter:=&ownedWorkerAdapterFixture{policy:strings.Repeat("a",64)}
 store:=&ownedWorkerStoreFixture{ready:func(ctx context.Context)error{
  deadline,ok:=ctx.Deadline();if !ok || time.Until(deadline)>15*time.Second{return messagedelivery.ErrInvalid}
  close(entered);<-ctx.Done();return ctx.Err()
 }}
 worker,err:=newManagedKafkaOfflineWorker(store,ownedWorkerReaderFixture{},[]OwnedKafkaOfflineTransport{adapter});if err!=nil{t.Fatal(err)}
 t.Cleanup(func(){stopOwnedWorker(t,worker)})
 go func(){returned<-worker.Start(context.Background())}()
 select{case <-entered:case <-time.After(2*time.Second):t.Fatal("readiness not entered")}
 stopOwnedWorker(t,worker)
 select{case err:=<-returned:if !errors.Is(err,context.Canceled){t.Fatal("readiness cancellation lost",err)};case <-time.After(2*time.Second):t.Fatal("startup did not join")}
 if store.lists.Load()!=0 || adapter.closed.Load()!=1{t.Fatal("readiness failure dispatched or leaked pool")}
 summary,err:=worker.DrainSummary();if err!=nil || summary.InstanceID!=store.KafkaOfflineInstanceID() || !summary.WorkerJoined || !summary.OwnedPoolsClosed{t.Fatal("wrong instance drain proof",err)}
}

func TestOwnedOfflineWorkerTimedOutStopCannotCloseActivePoolOrConfirmUnknownDelivery(t *testing.T){
 entered,release,workReturned:=make(chan struct{}),make(chan struct{}),make(chan struct{})
 var releaseOnce sync.Once;resume:=func(){releaseOnce.Do(func(){close(release)})};defer resume()
 adapter:=&ownedWorkerAdapterFixture{policy:strings.Repeat("a",64)}
 store:=&ownedWorkerStoreFixture{required:[]string{adapter.policy}}
 store.rows=func(context.Context,string,int64)([]messagedelivery.KafkaOnlinePending,error){return []messagedelivery.KafkaOnlinePending{{BranchID:"synthetic-branch",Cursor:"synthetic-cursor",PolicySHA256:adapter.policy}},nil}
 store.recover=func(context.Context,string,messagedelivery.VisibilityReader,messagedelivery.KafkaOfflineTransport)error{close(entered);<-release;close(workReturned);return messagedelivery.ErrKafkaOfflineUnconfirmed}
 adapter.onClose=func(){select{case <-workReturned:default:t.Error("owned transport closed before same recovery returned")}}
 worker,err:=newManagedKafkaOfflineWorker(store,ownedWorkerReaderFixture{},[]OwnedKafkaOfflineTransport{adapter});if err!=nil{t.Fatal(err)}
 t.Cleanup(func(){resume();stopOwnedWorker(t,worker)})
 if err:=worker.Start(context.Background());err!=nil{t.Fatal(err)}
 select{case <-entered:case <-time.After(2*time.Second):t.Fatal("real worker did not enter recovery")}
 short,cancel:=context.WithTimeout(context.Background(),20*time.Millisecond)
 err=worker.Stop(short);cancel()
 if !errors.Is(err,ErrKafkaOfflineShutdownPending) || adapter.closed.Load()!=0{t.Fatal("timeout manufactured a stopped pool",err)}
 if _,err:=worker.DrainSummary();!errors.Is(err,ErrKafkaOfflineShutdownPending){t.Fatal("active operation gained drain proof",err)}
 resume();stopOwnedWorker(t,worker);stopOwnedWorker(t,worker)
 if adapter.closed.Load()!=1{t.Fatal("subsequent Stop repeated pool shutdown")}
 _,unknown:=worker.Status();if !unknown{t.Fatal("successful local shutdown reclassified unknown business outcome")}
}

func TestOwnedOfflineWorkerCloseErrorAndDuplicatePolicyNeverCreateDrainAuthority(t *testing.T){
 policy:=strings.Repeat("a",64);adapter:=&ownedWorkerAdapterFixture{policy:policy,closeErr:errors.New("synthetic close result unknown")}
 store:=&ownedWorkerStoreFixture{}
 if _,err:=newManagedKafkaOfflineWorker(store,ownedWorkerReaderFixture{},[]OwnedKafkaOfflineTransport{adapter,&ownedWorkerAdapterFixture{policy:policy}});err==nil{t.Fatal("ambiguous retained policy accepted")}
 worker,err:=newManagedKafkaOfflineWorker(store,ownedWorkerReaderFixture{},[]OwnedKafkaOfflineTransport{adapter});if err!=nil{t.Fatal(err)}
 ctx,cancel:=context.WithTimeout(context.Background(),2*time.Second);defer cancel()
 if err:=worker.Stop(ctx);!errors.Is(err,ErrKafkaOfflineShutdownPending){t.Fatal("unknown close became success",err)}
 if _,err:=worker.DrainSummary();err==nil{t.Fatal("unknown close gained confirmation authority")}
 if adapter.closed.Load()!=1{t.Fatal("unexpected pool close count")}
}
