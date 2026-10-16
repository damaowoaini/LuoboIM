package msgtransfer

import (
	"context"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

var ErrKafkaOnlineWorkerShutdownPending=errors.New("ordinary online worker shutdown unconfirmed; retain owned resources")

type ordinaryOnlineStore interface{
	CheckKafkaReady(context.Context) error
	ListKafkaOnlinePending(context.Context,string,int64)([]messagedelivery.KafkaOnlinePending,error)
	RecoverKafkaOnline(context.Context,string,messagedelivery.VisibilityReader,messagedelivery.Projection,messagedelivery.KafkaOnlineTransport) error
}

// The actual managed ordinary lane uses the Store's existing visibility/seq
// recovery, not an in-memory queue or the old age-dropping Push worker. The
// registry owns exact policy-bound transports for current and pending epochs;
// it may not substitute a new current config for an old committed duty.
type ManagedKafkaOnlineWorker struct {
	store ordinaryOnlineStore
	reader messagedelivery.VisibilityReader
	projection messagedelivery.Projection
	transports map[string]messagedelivery.KafkaOnlineTransport
	mu sync.Mutex
	started bool
	cancel context.CancelFunc
	done chan struct{}
	lastOutcome string
	attempts uint64
}

func NewManagedKafkaOnlineWorker(store *messagedelivery.Store,reader messagedelivery.VisibilityReader,projection messagedelivery.Projection,transports []messagedelivery.KafkaOnlineTransport)(*ManagedKafkaOnlineWorker,error){
	if store==nil || reader==nil || projection==nil || len(transports)==0{return nil,messagedelivery.ErrInvalid}
	registry:=map[string]messagedelivery.KafkaOnlineTransport{}
	for _,transport:=range transports{
		if transport==nil{return nil,messagedelivery.ErrInvalid}
		fingerprint:=transport.PolicyFingerprint()
		decoded,err:=hex.DecodeString(fingerprint)
		if err!=nil || len(decoded)!=32 || hex.EncodeToString(decoded)!=fingerprint || registry[fingerprint]!=nil{return nil,messagedelivery.ErrInvalid}
		registry[fingerprint]=transport
	}
	return &ManagedKafkaOnlineWorker{store:store,reader:reader,projection:projection,transports:registry,done:make(chan struct{}),lastOutcome:"not-started"},nil
}

func (w *ManagedKafkaOnlineWorker) Start(parent context.Context)error{
	if w==nil || parent==nil{return messagedelivery.ErrInvalid}
	w.mu.Lock();defer w.mu.Unlock()
	if w.started{return messagedelivery.ErrConflict}
	ready,cancelReady:=context.WithTimeout(parent,10*time.Second);defer cancelReady()
	if err:=w.store.CheckKafkaReady(ready);err!=nil{return err}
	if err:=parent.Err();err!=nil{return err}
	ctx,cancel:=context.WithCancel(parent);w.cancel=cancel;w.started=true;w.lastOutcome="running"
	go w.run(ctx)
	return nil
}

func (w *ManagedKafkaOnlineWorker) run(ctx context.Context){
	defer close(w.done)
	cursor:=""
	for ctx.Err()==nil{
		rows,err:=w.store.ListKafkaOnlinePending(ctx,cursor,32)
		if err!=nil{w.outcome("catalog-or-storage-unconfirmed",false);if !waitKafkaWorker(ctx,2*time.Second){return};continue}
		if len(rows)==0{cursor="";if !waitKafkaWorker(ctx,500*time.Millisecond){return};continue}
		for _,row:=range rows{
			if ctx.Err()!=nil{return}
			cursor=row.Cursor
			transport:=w.transports[row.PolicySHA256]
			if transport==nil{w.outcome("frozen-policy-adapter-unavailable",false);continue}
			err:=w.store.RecoverKafkaOnline(ctx,row.BranchID,w.reader,w.projection,transport)
			switch{
			case err==nil:w.outcome("online-page-acknowledged",true)
			case errors.Is(err,messagedelivery.ErrOnlineBusy):w.outcome("online-page-owned-by-another-attempt",false)
			default:w.outcome("online-page-unconfirmed",true)
			}
		}
		if !waitKafkaWorker(ctx,100*time.Millisecond){return}
	}
}
func waitKafkaWorker(ctx context.Context,delay time.Duration)bool{
	timer:=time.NewTimer(delay);defer timer.Stop()
	select{case <-ctx.Done():return false;case <-timer.C:return true}
}
func (w *ManagedKafkaOnlineWorker) outcome(code string,attempt bool){
	w.mu.Lock();defer w.mu.Unlock();w.lastOutcome=code;if attempt{w.attempts++}
}

// No error strings, URL/config values, tokens, recipient IDs or payloads enter
// operational status. Callers inspect original minimal receipts for details.
func (w *ManagedKafkaOnlineWorker) Status()(string,uint64){
	if w==nil{return "invalid-worker",0};w.mu.Lock();defer w.mu.Unlock();return w.lastOutcome,w.attempts
}
func (w *ManagedKafkaOnlineWorker) Stop(ctx context.Context)error{
	if w==nil || ctx==nil{return messagedelivery.ErrInvalid}
	w.mu.Lock();if !w.started{w.mu.Unlock();return messagedelivery.ErrUnprepared};cancel:=w.cancel;w.mu.Unlock();cancel()
	select{case <-w.done:return nil;case <-ctx.Done():return ErrKafkaOnlineWorkerShutdownPending}
}
