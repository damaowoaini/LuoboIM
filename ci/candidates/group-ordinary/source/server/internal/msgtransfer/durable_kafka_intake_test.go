package msgtransfer

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

// These exercise the real public handler/ConsumeClaim path. Only the durable
// Mongo boundary and the Sarama broker/session are fake here. Real storage,
// receipt, roster and allocator assertions live in kafka_intake_integration_test.
type intakeBoundaryStub struct {
	ready func(context.Context) error
	accept func(context.Context, messagedelivery.KafkaRecord, string) (messagedelivery.Receipt, error)
}
func (s *intakeBoundaryStub) CheckReady(ctx context.Context) error {
	if s.ready != nil { return s.ready(ctx) }; return nil
}
func (s *intakeBoundaryStub) AcceptKafkaRecord(ctx context.Context, r messagedelivery.KafkaRecord, policy string, frozen ...messagedelivery.KafkaDeliveryPolicy) (messagedelivery.Receipt, error) {
	return s.accept(ctx,r,policy)
}
type intakeSessionStub struct {
	ctx context.Context
	mu sync.Mutex
	marked []*sarama.ConsumerMessage
}
func (s *intakeSessionStub) Claims() map[string][]int32 { return map[string][]int32{"owned-source":{2}} }
func (s *intakeSessionStub) MemberID() string { return "synthetic-owned-consumer" }
func (s *intakeSessionStub) GenerationID() int32 { return 1 }
func (s *intakeSessionStub) MarkOffset(string,int32,int64,string) { panic("unexpected direct MarkOffset") }
func (s *intakeSessionStub) Commit() { panic("unexpected explicit broker Commit") }
func (s *intakeSessionStub) ResetOffset(string,int32,int64,string) { panic("unexpected offset reset") }
func (s *intakeSessionStub) MarkMessage(m *sarama.ConsumerMessage, metadata string) {
	if metadata!="known-durable-source-receipt-v1" { panic("unexpected receipt metadata") }
	s.mu.Lock(); defer s.mu.Unlock(); s.marked=append(s.marked,m)
}
func (s *intakeSessionStub) Context() context.Context { return s.ctx }
func (s *intakeSessionStub) markCount() int { s.mu.Lock();defer s.mu.Unlock();return len(s.marked) }
type intakeClaimStub struct { messages chan *sarama.ConsumerMessage }
func (*intakeClaimStub) Topic() string { return "owned-source" }
func (*intakeClaimStub) Partition() int32 { return 2 }
func (*intakeClaimStub) InitialOffset() int64 { return 40 }
func (*intakeClaimStub) HighWaterMarkOffset() int64 { return 44 }
func (c *intakeClaimStub) Messages() <-chan *sarama.ConsumerMessage { return c.messages }
func intakeClaim(messages ...*sarama.ConsumerMessage)*intakeClaimStub {
	c:=&intakeClaimStub{messages:make(chan *sarama.ConsumerMessage,len(messages))}
	for _,message:=range messages{c.messages<-message};close(c.messages);return c
}
func intakeMessage(offset int64)*sarama.ConsumerMessage {
	return &sarama.ConsumerMessage{Topic:"owned-source",Partition:2,Offset:offset,Key:[]byte("sg_synthetic"),Value:[]byte("synthetic broker bytes"),Headers:[]*sarama.RecordHeader{
		{Key:[]byte(constant.OperationID),Value:[]byte("synthetic-operation")},
		{Key:[]byte(constant.OpUserID),Value:[]byte("synthetic-owner")},
		{Key:[]byte(constant.OpUserPlatform),Value:[]byte("10")},
		{Key:[]byte(constant.ConnID),Value:[]byte("synthetic-connection")},
	}}
}
func intakeFixture(store ordinaryKafkaStore)*DurableKafkaIntake {
	return &DurableKafkaIntake{store:store,topic:"owned-source",policySHA:strings.Repeat("a",64),failed:make(chan struct{})}
}

func TestDurableKafkaRealHandlerMarksOnlyKnownCommittedSource(t *testing.T) {
	for _,unknown:=range []bool{false,true}{
		t.Run(map[bool]string{false:"known",true:"commit-unknown"}[unknown],func(t *testing.T){
			session:=&intakeSessionStub{ctx:context.Background()};calls:=0
			boundary:=&intakeBoundaryStub{accept:func(ctx context.Context,r messagedelivery.KafkaRecord,policy string)(messagedelivery.Receipt,error){
				calls++
				if session.markCount()!=calls-1{t.Fatal("source marked before durable receipt")}
				deadline,ok:=ctx.Deadline();if !ok || time.Until(deadline)>30*time.Second || mcontext.GetOpUserID(ctx)!="synthetic-owner" || r.Offset!=int64(39+calls) || policy!=strings.Repeat("a",64){t.Fatal("owned source/deadline/policy changed")}
				if unknown && calls==2{return messagedelivery.Receipt{},messagedelivery.ErrCommitUnconfirmed}
				return messagedelivery.Receipt{SourceID:"synthetic-known-boundary-receipt"},nil
			}}
			intake:=intakeFixture(boundary);handler,err:=NewDurableOnlineHistoryHandler(intake)
			if err!=nil{t.Fatal(err)}
			err=handler.ConsumeClaim(session,intakeClaim(intakeMessage(40),intakeMessage(41),intakeMessage(42)))
			if unknown{
				if !errors.Is(err,messagedelivery.ErrCommitUnconfirmed) || calls!=2 || session.markCount()!=1 || !errors.Is(intake.failure(),messagedelivery.ErrCommitUnconfirmed){t.Fatal("unknown source advanced broker mark or replayed later record",err)}
				// A new claim on this SAME failed instance must not restart work.
				if err:=handler.ConsumeClaim(session,intakeClaim(intakeMessage(41)));!errors.Is(err,messagedelivery.ErrCommitUnconfirmed) || calls!=2{t.Fatal("failed instance automatically replayed unknown source",err)}
			}else if err!=nil || calls!=3 || session.markCount()!=3 || intake.failure()!=nil{t.Fatal("known receipts were not marked exactly once",err)}
		})
	}
}

func TestDurableKafkaOwnedHeadersAreNamedAndCannotBorrowSessionIdentity(t *testing.T) {
	original:=intakeMessage(40);ctx,record,err:=ownedKafkaRecord(context.Background(),original)
	if err!=nil || mcontext.GetOpUserID(ctx)!="synthetic-owner"{t.Fatal(err)}
	// Header order is not identity. The old parser used positional values;
	// the new actual boundary binds the four producer key names instead.
	reordered:=intakeMessage(40);reordered.Headers[0],reordered.Headers[3]=reordered.Headers[3],reordered.Headers[0]
	_,same,err:=ownedKafkaRecord(context.Background(),reordered)
	if err!=nil || !reflect.DeepEqual(record,same){t.Fatal("canonical named context depends on header order",err)}
	for _,fault:=range []string{"duplicate","foreign","missing","empty-actor","control","wrong-partition"}{
		t.Run(fault,func(t *testing.T){
			message:=intakeMessage(40)
			switch fault{
			case "duplicate":message.Headers[0]=message.Headers[1]
			case "foreign":message.Headers[0].Key=[]byte("Authorization")
			case "missing":message.Headers=message.Headers[:3]
			case "empty-actor":message.Headers[1].Value=nil
			case "control":message.Headers[1].Value=[]byte("synthetic-owner\nother")
			case "wrong-partition":message.Partition=3
			}
			calls:=0;boundary:=&intakeBoundaryStub{accept:func(context.Context,messagedelivery.KafkaRecord,string)(messagedelivery.Receipt,error){calls++;return messagedelivery.Receipt{},nil}}
			handler,_:=NewDurableOnlineHistoryHandler(intakeFixture(boundary))
			// A parent with a valid unrelated opuser may not fill missing or
			// malformed broker identity and thereby admit another source.
			parent:=context.WithValue(context.Background(),constant.OpUserID,"borrowed-parent-owner")
			session:=&intakeSessionStub{ctx:parent}
			if err:=handler.ConsumeClaim(session,intakeClaim(message));!errors.Is(err,messagedelivery.ErrInvalid) || calls!=0 || session.markCount()!=0{t.Fatal("malformed source reached persistence/mark",err)}
		})
	}
	original.Key[0]='x';original.Value[0]='x'
	if record.Key[0]=='x' || record.Body[0]=='x'{t.Fatal("source proof borrowed mutable broker buffers")}
}

func TestDurableKafkaSessionCancellationReachesWorkAndDoesNotMark(t *testing.T) {
	ctx,cancel:=context.WithCancel(context.Background());defer cancel()
	entered:=make(chan struct{});done:=make(chan error,1)
	boundary:=&intakeBoundaryStub{accept:func(work context.Context,_ messagedelivery.KafkaRecord,_ string)(messagedelivery.Receipt,error){close(entered);<-work.Done();return messagedelivery.Receipt{},work.Err()}}
	intake:=intakeFixture(boundary);handler,_:=NewDurableOnlineHistoryHandler(intake);session:=&intakeSessionStub{ctx:ctx}
	go func(){done<-handler.ConsumeClaim(session,intakeClaim(intakeMessage(40)))}()
	select{case <-entered:case <-time.After(time.Second):t.Fatal("real handler did not enter source work")}
	cancel()
	select{case err:=<-done:if !errors.Is(err,context.Canceled){t.Fatal(err)};case <-time.After(time.Second):t.Fatal("canceled session work did not join")}
	if session.markCount()!=0 || intake.failure()!=nil{t.Fatal("cancellation marked source or misclassified normal rebalance as poison")}
}
