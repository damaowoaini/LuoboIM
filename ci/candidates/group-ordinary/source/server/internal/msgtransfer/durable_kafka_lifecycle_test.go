package msgtransfer

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

type managedKafkaStub struct {
	sarama.ConsumerGroup // Unused broker operations deliberately have no fake implementation.
	consume func(context.Context,[]string,sarama.ConsumerGroupHandler) error
	close func() error
}
func (s *managedKafkaStub) Consume(ctx context.Context,topics []string,h sarama.ConsumerGroupHandler)error{return s.consume(ctx,topics,h)}
func (s *managedKafkaStub) Close()error{return s.close()}

func TestManagedOrdinaryKafkaReadinessPrecedesConsumerAndHasOwnDeadline(t *testing.T) {
	var consumes,closes atomic.Int32
	unready:=errors.New("synthetic unprepared catalog")
	boundary:=&intakeBoundaryStub{ready:func(ctx context.Context)error{
		deadline,ok:=ctx.Deadline();if !ok || time.Until(deadline)>10*time.Second{t.Fatal("startup depended on an unbounded outer context")};return unready
	}}
	broker:=&managedKafkaStub{consume:func(context.Context,[]string,sarama.ConsumerGroupHandler)error{consumes.Add(1);return nil},close:func()error{closes.Add(1);return nil}}
	managed,err:=NewManagedOrdinaryKafka(intakeFixture(boundary),broker);if err!=nil{t.Fatal(err)}
	if err:=managed.Start(context.Background());!errors.Is(err,unready) || consumes.Load()!=0 || closes.Load()!=0{t.Fatal("constructor/readiness launched unprepared source",err)}
	if err:=managed.Stop(context.Background());!errors.Is(err,messagedelivery.ErrUnprepared){t.Fatal("not-started instance produced a shutdown success receipt",err)}
}

func TestManagedOrdinaryKafkaStopRetainsOneCloseAcrossUnconfirmedDeadline(t *testing.T) {
	entered,closeEntered,releaseClose:=make(chan struct{}),make(chan struct{}),make(chan struct{})
	var consumes,closes atomic.Int32
	broker:=&managedKafkaStub{consume:func(ctx context.Context,topics []string,h sarama.ConsumerGroupHandler)error{
		consumes.Add(1);if len(topics)!=1 || topics[0]!="owned-source"{return messagedelivery.ErrInvalid};close(entered);<-ctx.Done();return ctx.Err()
	},close:func()error{closes.Add(1);close(closeEntered);<-releaseClose;return nil}}
	managed,err:=NewManagedOrdinaryKafka(intakeFixture(&intakeBoundaryStub{}),broker);if err!=nil{t.Fatal(err)}
	if err:=managed.Start(context.Background());err!=nil{t.Fatal(err)}
	t.Cleanup(func(){select{case <-releaseClose:default:close(releaseClose)};end,cancel:=context.WithTimeout(context.Background(),time.Second);defer cancel();if err:=managed.Stop(end);err!=nil{t.Error("owned intake did not close and join",err)}})
	select{case <-entered:case <-time.After(time.Second):t.Fatal("consumer did not start")}
	if err:=managed.Start(context.Background());!errors.Is(err,messagedelivery.ErrConflict){t.Fatal("same instance created a second consumer",err)}
	short,cancel:=context.WithCancel(context.Background());cancel()
	if err:=managed.Stop(short);!errors.Is(err,ErrOrdinaryKafkaShutdownPending){t.Fatal("deadline was reported as successful shutdown",err)}
	select{case <-closeEntered:case <-time.After(time.Second):t.Fatal("close was not started")}
	if consumes.Load()!=1 || closes.Load()!=1{t.Fatal("stop or restart duplicated owned broker lifecycle")}
	close(releaseClose)
	end,cancelEnd:=context.WithTimeout(context.Background(),time.Second);defer cancelEnd()
	if err:=managed.Stop(end);err!=nil || closes.Load()!=1 || managed.Failure()!=nil{t.Fatal("same closing operation could not be joined after deadline",err)}
}

func TestManagedOrdinaryKafkaUnknownSourceStopsWithoutLosingFailureOnCleanShutdown(t *testing.T) {
	var consumes,closes atomic.Int32
	entered,exited:=make(chan struct{}),make(chan struct{})
	boundary:=&intakeBoundaryStub{accept:func(context.Context,messagedelivery.KafkaRecord,string)(messagedelivery.Receipt,error){return messagedelivery.Receipt{},messagedelivery.ErrCommitUnconfirmed}}
	intake:=intakeFixture(boundary)
	broker:=&managedKafkaStub{consume:func(ctx context.Context,_ []string,h sarama.ConsumerGroupHandler)error{
		consumes.Add(1);close(entered)
		err:=h.ConsumeClaim(&intakeSessionStub{ctx:ctx},intakeClaim(intakeMessage(40)))
		if !errors.Is(err,messagedelivery.ErrCommitUnconfirmed){return errors.New("synthetic expected real handler unknown source")}
		<-ctx.Done();close(exited);return ctx.Err()
	},close:func()error{closes.Add(1);return nil}}
	managed,err:=NewManagedOrdinaryKafka(intake,broker);if err!=nil{t.Fatal(err)}
	if err:=managed.Start(context.Background());err!=nil{t.Fatal(err)}
	t.Cleanup(func(){end,cancel:=context.WithTimeout(context.Background(),time.Second);defer cancel();if err:=managed.Stop(end);err!=nil{t.Error("owned consumer cleanup",err)}})
	select{case <-entered:case <-time.After(time.Second):t.Fatal("consumer did not enter")}
	select{case <-exited:case <-time.After(time.Second):t.Fatal("latched unknown source did not cancel managed consumer")}
	end,cancel:=context.WithTimeout(context.Background(),time.Second);defer cancel()
	if err:=managed.Stop(end);err!=nil{t.Fatal("joined/closed consumer may not falsely retain pools",err)}
	if !errors.Is(managed.Failure(),messagedelivery.ErrCommitUnconfirmed) || consumes.Load()!=1 || closes.Load()!=1{t.Fatal("shutdown erased unknown source or automatically replayed consumer")}
}
