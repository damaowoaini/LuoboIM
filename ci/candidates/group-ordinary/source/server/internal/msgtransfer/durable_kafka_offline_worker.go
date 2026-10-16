package msgtransfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

var ErrKafkaOfflineShutdownPending=errors.New("ordinary offline worker shutdown unconfirmed; retain owned pools")

type OwnedKafkaOfflineTransport interface {
	messagedelivery.KafkaOfflineTransport
	Close()error
}
type ordinaryOfflineStore interface {
	CheckKafkaReady(context.Context)error
	RequiredKafkaOfflinePolicies(context.Context)([]string,error)
	ListKafkaOfflinePending(context.Context,string,int64)([]messagedelivery.KafkaOnlinePending,error)
	RecoverKafkaOffline(context.Context,string,messagedelivery.VisibilityReader,messagedelivery.KafkaOfflineTransport)error
	KafkaOfflineInstanceID()string
}

// This object takes exclusive ownership of its adapter pools. The actual
// factory creates a private Store for this one worker, not a shared live Store.
// Close/drain evidence concerns pool shutdown; original unknown device results
// remain durable and are never reclassified as rollback by a successful Stop.
type ManagedKafkaOfflineWorker struct {
	store ordinaryOfflineStore
	reader messagedelivery.VisibilityReader
	transports map[string]OwnedKafkaOfflineTransport
	mu sync.Mutex
	started,closing bool
	cancel context.CancelFunc
	done,closeDone chan struct{}
	closeOnce sync.Once
	closeFailed,unknownSeen bool
	lastOutcome string
}

func NewManagedKafkaOfflineWorker(store *messagedelivery.Store,reader messagedelivery.VisibilityReader,adapters []OwnedKafkaOfflineTransport)(*ManagedKafkaOfflineWorker,error){
	if store==nil{return nil,messagedelivery.ErrInvalid}
	return newManagedKafkaOfflineWorker(store,reader,adapters)
}
func newManagedKafkaOfflineWorker(store ordinaryOfflineStore,reader messagedelivery.VisibilityReader,adapters []OwnedKafkaOfflineTransport)(*ManagedKafkaOfflineWorker,error){
	if store==nil || reader==nil || len(adapters)==0 || store.KafkaOfflineInstanceID()==""{return nil,messagedelivery.ErrInvalid}
	registry:=map[string]OwnedKafkaOfflineTransport{}
	for _,adapter:=range adapters{
		if adapter==nil{return nil,messagedelivery.ErrInvalid};fingerprint:=adapter.PolicyFingerprint()
		decoded,err:=hex.DecodeString(fingerprint)
		if err!=nil || len(decoded)!=32 || hex.EncodeToString(decoded)!=fingerprint || registry[fingerprint]!=nil{return nil,messagedelivery.ErrInvalid}
		registry[fingerprint]=adapter
	}
	return &ManagedKafkaOfflineWorker{store:store,reader:reader,transports:registry,done:make(chan struct{}),closeDone:make(chan struct{}),lastOutcome:"not-started"},nil
}

func (w *ManagedKafkaOfflineWorker) Start(parent context.Context)error{
	if w==nil || parent==nil{return messagedelivery.ErrInvalid}
	w.mu.Lock()
	if w.started || w.closing{w.mu.Unlock();return messagedelivery.ErrConflict}
	ctx,cancel:=context.WithCancel(parent);w.cancel=cancel;w.started=true;w.lastOutcome="checking-readiness";w.mu.Unlock()
	launched:=false
	defer func(){if !launched{cancel();close(w.done)}}()
	ready,stopReady:=context.WithTimeout(ctx,15*time.Second);defer stopReady()
	if err:=w.store.CheckKafkaReady(ready);err!=nil{w.outcome("storage-readiness-rejected",false);return err}
	for _,adapter:=range w.transports{if err:=adapter.CheckReady(ready);err!=nil{w.outcome("configured-adapter-unready",false);return err}}
	required,err:=w.store.RequiredKafkaOfflinePolicies(ready);if err!=nil{w.outcome("pending-policy-readiness-rejected",false);return err}
	for _,policy:=range required{if w.transports[policy]==nil{w.outcome("retained-policy-adapter-missing",false);return messagedelivery.ErrUnprepared}}
	if err:=ctx.Err();err!=nil{return err}
	w.outcome("running",false);launched=true;go w.run(ctx);return nil
}

func (w *ManagedKafkaOfflineWorker) run(ctx context.Context){
	defer close(w.done);cursor:=""
	for ctx.Err()==nil{
		rows,err:=w.store.ListKafkaOfflinePending(ctx,cursor,32)
		if err!=nil{w.outcome("pending-read-unconfirmed",true);if !waitKafkaWorker(ctx,time.Second){return};continue}
		if len(rows)==0{cursor="";if !waitKafkaWorker(ctx,500*time.Millisecond){return};continue}
		for _,row:=range rows{
			if ctx.Err()!=nil{return};cursor=row.Cursor;adapter:=w.transports[row.PolicySHA256]
			if adapter==nil{w.outcome("frozen-policy-adapter-unavailable",true);continue}
			err:=w.store.RecoverKafkaOffline(ctx,row.BranchID,w.reader,adapter)
			switch{case err==nil:w.outcome("offline-page-settled",false);case errors.Is(err,messagedelivery.ErrKafkaOfflineBusy):w.outcome("offline-page-owned-by-another-attempt",false);default:w.outcome("offline-page-unconfirmed",true)}
		}
		if !waitKafkaWorker(ctx,100*time.Millisecond){return}
	}
}
func (w *ManagedKafkaOfflineWorker) outcome(code string,unknown bool){w.mu.Lock();defer w.mu.Unlock();w.lastOutcome=code;w.unknownSeen=w.unknownSeen || unknown}
func (w *ManagedKafkaOfflineWorker) Status()(string,bool){if w==nil{return "invalid-worker",true};w.mu.Lock();defer w.mu.Unlock();return w.lastOutcome,w.unknownSeen}

// Stop is also the cleanup path after failed readiness or before first Start.
// One close operation waits for the SAME worker to join before closing pools.
// A caller deadline does not mark success or start a second Close attempt.
func (w *ManagedKafkaOfflineWorker) Stop(ctx context.Context)error{
	if w==nil || ctx==nil{return messagedelivery.ErrInvalid}
	w.mu.Lock();w.closing=true;started:=w.started;cancel:=w.cancel;w.mu.Unlock()
	if cancel!=nil{cancel()}
	w.closeOnce.Do(func(){go func(){
		if started{<-w.done}
		failed:=false
		for _,adapter:=range w.transports{if adapter.Close()!=nil{failed=true}}
		w.mu.Lock();w.closeFailed=failed;w.lastOutcome="stopped";if failed{w.lastOutcome="pool-close-unconfirmed"};w.mu.Unlock();close(w.closeDone)
	}()})
	select{case <-w.closeDone:w.mu.Lock();failed:=w.closeFailed;w.mu.Unlock();if failed{return ErrKafkaOfflineShutdownPending};return nil;case <-ctx.Done():return ErrKafkaOfflineShutdownPending}
}

type KafkaOfflineDrainSummary struct {
	Version string `json:"version"`
	InstanceID string `json:"instanceID"`
	WorkerJoined bool `json:"workerJoined"`
	OwnedPoolsClosed bool `json:"ownedPoolsClosed"`
	Policies []string `json:"policies"`
}

func (w *ManagedKafkaOfflineWorker) DrainSummary()(KafkaOfflineDrainSummary,error){
	if w==nil{return KafkaOfflineDrainSummary{},messagedelivery.ErrInvalid}
	select{case <-w.closeDone:default:return KafkaOfflineDrainSummary{},ErrKafkaOfflineShutdownPending}
	w.mu.Lock();defer w.mu.Unlock();if w.closeFailed || !w.closing{return KafkaOfflineDrainSummary{},ErrKafkaOfflineShutdownPending}
	policies:=make([]string,0,len(w.transports));for policy:=range w.transports{policies=append(policies,policy)};sort.Strings(policies)
	return KafkaOfflineDrainSummary{Version:"ordinary-offline-drained-v1",InstanceID:w.store.KafkaOfflineInstanceID(),WorkerJoined:true,OwnedPoolsClosed:true,Policies:policies},nil
}

func (s KafkaOfflineDrainSummary) SHA256()string{body,_:=json.Marshal(s);body=append(body,'\n');sum:=sha256.Sum256(body);return hex.EncodeToString(sum[:])}
