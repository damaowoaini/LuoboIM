package cache

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type ordinaryBadgeLostReply struct{armed atomic.Bool}
func (*ordinaryBadgeLostReply) DialHook(next redis.DialHook)redis.DialHook{return next}
func (h *ordinaryBadgeLostReply) ProcessHook(next redis.ProcessHook)redis.ProcessHook{
	return func(ctx context.Context,command redis.Cmder)error{
		err:=next(ctx,command)
		args:=command.Args()
		if err==nil && command.Name()=="eval" && len(args)>1 && args[1]==ordinaryBadgeReserveLua && h.armed.CompareAndSwap(true,false){return errors.New("synthetic lost EVAL ACK after actual accounting")}
		return err
	}
}
func (*ordinaryBadgeLostReply) ProcessPipelineHook(next redis.ProcessPipelineHook)redis.ProcessPipelineHook{return next}

type ordinaryBadgeFixtureState struct{cleanupAllowed atomic.Bool}

func ordinaryBadgeRedisFixture(t *testing.T)(context.Context,*redis.Client,*OrdinaryBadgeLedger,*msgCache,string,*ordinaryBadgeFixtureState){
	t.Helper()
	raw:=os.Getenv("LUOBO_RETENTION_TEST_REDIS")
	if raw==""{t.Skip("requires explicit managed task Redis fixture")}
	if !allowedRedisFixtureURI(raw){t.Fatal("exact task Redis URI required")}
	options,err:=redis.ParseURL(raw)
	if err!=nil || options.Addr!="127.0.0.1:36379" || options.DB!=15 || options.Username!="" || options.Password!="" || options.TLSConfig!=nil{t.Fatal("exact task Redis configuration required")}
	options.DialTimeout=time.Second
	client:=redis.NewClient(options)
	t.Cleanup(func(){if err:=client.Close();err!=nil{t.Error("owned badge fixture client close failed")}})
	ctx,cancel:=context.WithTimeout(context.Background(),20*time.Second);t.Cleanup(cancel)
	nonce,err:=badgeNonce();if err!=nil{t.Fatal(err)}
	user:="luobo_ordinary_badge_test_"+nonce
	scope:=badgeHash([]byte("synthetic owned badge scope:"+nonce));epoch:=uuid.NewString()
	activation,evidence:=badgeHash([]byte("synthetic activation:"+nonce)),badgeHash([]byte("synthetic drain proof:"+nonce))
	ledger,err:=NewOrdinaryBadgeLedger(client,scope,epoch,activation,evidence);if err!=nil{t.Fatal(err)}
	keys:=append([]string{"luobo_ordinary_badge_claim_"+nonce},ledger.keys(user,uuid.NewString())...)
	claim,err:=badgeNonce();if err!=nil{t.Fatal(err)}
	ack,err:=client.Eval(ctx,`if redis.call('EXISTS',KEYS[1],KEYS[2],KEYS[3],KEYS[4])~=0 then return 0 end
redis.call('SET',KEYS[1],ARGV[1]);redis.call('SET',KEYS[2],'0');return 1`,keys,claim).Int()
	if err!=nil || ack!=1{t.Fatal("fresh exact badge fixture keys claim unconfirmed; no cleanup granted")}
	state:=&ordinaryBadgeFixtureState{};state.cleanupAllowed.Store(true)
	t.Cleanup(func(){
		if !state.cleanupAllowed.Load(){t.Error("badge workers did not drain; retain exact keys for fixture owner inspection");return}
		end,stop:=context.WithTimeout(context.Background(),3*time.Second);defer stop()
		ack,err:=client.Eval(end,`if redis.call('TYPE',KEYS[1]).ok~='string' or redis.call('GET',KEYS[1])~=ARGV[1] then return -1 end
local owner=redis.call('TYPE',KEYS[3]).ok
if owner~='none' and (owner~='hash' or redis.call('HGET',KEYS[3],'scope')~=ARGV[2] or redis.call('HGET',KEYS[3],'epoch')~=ARGV[3] or redis.call('HGET',KEYS[3],'control')~=ARGV[4]) then return -2 end
local global=redis.call('TYPE',KEYS[4]).ok
if global~='none' and (global~='string' or redis.call('GET',KEYS[4])~=ARGV[4]) then return -3 end
redis.call('DEL',KEYS[1],KEYS[2],KEYS[3],KEYS[4]);return 1`,keys,claim,scope,epoch,ledger.control).Int()
		if err!=nil || ack!=1{t.Error("exact owned badge fixture cleanup unconfirmed");return}
		if remaining,err:=client.Exists(end,keys...).Result();err!=nil || remaining!=0{t.Error("exact badge fixture keys still present or readback unknown")}
	})
	if err:=ledger.CheckReady(ctx);err==nil{t.Fatal("unprepared epoch became ready without operator action")}
	if err:=PrepareOrdinaryBadgeEpoch(ctx,client,scope,epoch,activation,evidence);err!=nil{t.Fatal(err)}
	if err:=ledger.CheckReady(ctx);err!=nil{t.Fatal(err)}
	return ctx,client,ledger,NewMsgCacheModel(client).(*msgCache),user,state
}

func TestOrdinaryBadgeRedisLostReplyReplayAndUserResetNeverReAddOldBranch(t *testing.T){
	ctx,client,ledger,cache,user,_:=ordinaryBadgeRedisFixture(t)
	hook:=&ordinaryBadgeLostReply{};client.AddHook(hook)
	first,second,third:=uuid.NewString(),uuid.NewString(),uuid.NewString()
	hook.armed.Store(true)
	if _,err:=ledger.Reserve(ctx,user,first);!errors.Is(err,ErrOrdinaryBadgeUnprepared){t.Fatal("lost actual EVAL reply was called confirmed",err)}
	known,err:=ledger.Reserve(ctx,user,first)
	if err!=nil || known.Value!=1{t.Fatal("retry re-applied accounting after lost ACK",err)}
	other,err:=ledger.Reserve(ctx,user,second)
	if err!=nil || other.Value!=2{t.Fatal("distinct branch did not increment once",err)}
	replay,err:=ledger.Reserve(ctx,user,first)
	if err!=nil || replay.Value!=2 || replay.ReceiptSHA256!=known.ReceiptSHA256{t.Fatal("old receipt changed identity or re-incremented",err)}
	if _,err:=cache.IncrUserBadgeUnreadCountSum(ctx,user);err==nil{t.Fatal("legacy aggregate increment bypassed active duty receipts")}
	if err:=cache.SetUserBadgeUnreadCountSum(ctx,user,0);err!=nil{t.Fatal(err)}
	if err:=ledger.Validate(ctx,user,first,known);err==nil{t.Fatal("old frozen provider request ignored user's reset")}
	afterReset,err:=ledger.Reserve(ctx,user,first)
	if err!=nil || afterReset.Value!=0 || afterReset.ResetID==known.ResetID || afterReset.ReceiptSHA256!=known.ReceiptSHA256{t.Fatal("old branch re-added after user reset",err)}
	if err:=ledger.Validate(ctx,user,first,afterReset);err!=nil{t.Fatal(err)}
	next,err:=ledger.Reserve(ctx,user,third)
	if err!=nil || next.Value!=1{t.Fatal("new branch after reset did not account normally",err)}
	// Simulate only a stale legacy projection, never damage shared inputs.
	if err:=client.Set(ctx,userBadgeUnreadCountSum+user,0,0).Err();err!=nil{t.Fatal(err)}
	if value,err:=cache.GetUserBadgeUnreadCountSum(ctx,user);err!=nil || value!=1{t.Fatal("actual getter read stale projection instead of owned counter",err)}
	if replay,err:=ledger.Reserve(ctx,user,third);err!=nil || replay.Value!=1{t.Fatal("projection recovery re-accounted the message",err)}
	if value,err:=client.Get(ctx,userBadgeUnreadCountSum+user).Int();err!=nil || value!=1{t.Fatal("retry did not repair the exact legacy projection",err)}
}

func TestOrdinaryBadgeRedisConcurrentSameBranchHasOneAccountingReceipt(t *testing.T){
	ctx,_,ledger,cache,user,state:=ordinaryBadgeRedisFixture(t)
	work,cancel:=context.WithCancel(ctx);defer cancel()
	branch:=uuid.NewString();start:=make(chan struct{});done:=make(chan struct{})
	type result struct{snapshot OrdinaryBadgeSnapshot;err error};results:=make(chan result,2)
	var workers sync.WaitGroup;workers.Add(2)
	for i:=0;i<2;i++{go func(){defer workers.Done();select{case <-start:case <-work.Done():return};snapshot,err:=ledger.Reserve(work,user,branch);results<-result{snapshot,err}}()}
	go func(){workers.Wait();close(done)}()
	t.Cleanup(func(){cancel();select{case <-done:case <-time.After(3*time.Second):state.cleanupAllowed.Store(false);t.Error("exact badge workers failed to drain")}})
	close(start)
	var first OrdinaryBadgeSnapshot
	for i:=0;i<2;i++{select{case result:=<-results:
		if result.err!=nil || result.snapshot.Value!=1{t.Fatal("same branch was not idempotent under concurrency",result.err)}
		if i==0{first=result.snapshot}else if first!=result.snapshot{t.Fatal("concurrent calls did not observe the same accounting result")}
	case <-work.Done():t.Fatal("owned concurrent badge test did not finish")}}
	if value,err:=cache.GetUserBadgeUnreadCountSum(ctx,user);err!=nil || value!=1{t.Fatal("actual getter showed more than one increment",err)}
}
