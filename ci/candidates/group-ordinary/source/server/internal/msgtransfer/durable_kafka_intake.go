package msgtransfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"go.mongodb.org/mongo-driver/mongo"
)

// DurableKafkaIntake is the concrete owned source boundary. Constructor
// arguments are startup/operator inputs, never account request metadata.
// This WIP does not yet provide the policy-aware projection/offline/Seq0
// workers needed to replace the existing StartTransfer activation fence.
// The private interface exposes only the two owned intake operations. Runtime
// construction always creates the concrete membership-bound Mongo Store;
// tests may substitute this boundary without starting a broker or a database.
type ordinaryKafkaStore interface {
	CheckReady(context.Context) error
	AcceptKafkaRecord(context.Context, messagedelivery.KafkaRecord, string, ...messagedelivery.KafkaDeliveryPolicy) (messagedelivery.Receipt, error)
}

type DurableKafkaIntake struct {
	store ordinaryKafkaStore
	topic string
	policySHA string
	policy messagedelivery.KafkaDeliveryPolicy
	failureMu sync.Mutex
	firstFailure error
	failed chan struct{}
}

func NewDurableKafkaIntake(db *mongo.Database,epoch,activation,topic,policySHA string)(*DurableKafkaIntake,error){
	if topic=="" || strings.TrimSpace(topic)!=topic || len(topic)>249 || strings.ContainsAny(topic,"\x00\r\n") {return nil,messagedelivery.ErrInvalid}
	h,err:=hex.DecodeString(policySHA)
	if err!=nil || len(h)!=32 || hex.EncodeToString(h)!=policySHA {return nil,messagedelivery.ErrInvalid}
	s,err:=messagedelivery.NewMembershipBoundStore(db,epoch,activation)
	if err!=nil{return nil,err}
	policy,err:=FreezeOwnedKafkaPolicy(db.Name(),epoch,activation)
	if err!=nil{return nil,err}
	actual,err:=policy.Fingerprint();if err!=nil || actual!=policySHA{return nil,messagedelivery.ErrUnprepared}
	return &DurableKafkaIntake{store:s,topic:topic,policySHA:policySHA,policy:policy,failed:make(chan struct{})},nil
}

func (i *DurableKafkaIntake) CheckReady(ctx context.Context)error{
	if i==nil || i.store==nil || ctx==nil{return messagedelivery.ErrUnprepared}
	return i.store.CheckReady(ctx)
}

// No broker offsets are marked while a body is merely buffered in memory.
// The real handler's ConsumeClaim delegates here before entering its legacy
// batching/timer goroutines. A known receipt is enough to ACK source custody;
// projection completion has its own durable obligations, not a repeated write.
func (i *DurableKafkaIntake) consumeClaim(session sarama.ConsumerGroupSession,claim sarama.ConsumerGroupClaim)error{
	if i==nil || i.store==nil || session==nil || claim==nil || claim.Topic()!=i.topic || session.Context()==nil{return messagedelivery.ErrInvalid}
	for {
		if err := i.failure(); err != nil { return err }
		select{
		case <-session.Context().Done():return session.Context().Err()
		case message,ok:=<-claim.Messages():
			if !ok{return nil}
			if message==nil || message.Topic!=claim.Topic() || message.Partition!=claim.Partition() || message.Offset<0{return messagedelivery.ErrInvalid}
			ctx,record,err:=ownedKafkaRecord(session.Context(),message)
			if err!=nil{return err}
			work,cancel:=context.WithTimeout(ctx,30*time.Second)
			_,err=i.store.AcceptKafkaRecord(work,record,i.policySHA,i.policy)
			cancel()
			if err!=nil{return err} // including unknown commit: no source ACK/new ID.
			session.MarkMessage(message,"known-durable-source-receipt-v1")
		}
	}
}

func ownedKafkaRecord(parent context.Context,message *sarama.ConsumerMessage)(context.Context,messagedelivery.KafkaRecord,error){
	if parent==nil || message==nil || len(message.Headers)!=4{return nil,messagedelivery.KafkaRecord{},messagedelivery.ErrInvalid}
	keys:=[]string{constant.OperationID,constant.OpUserID,constant.OpUserPlatform,constant.ConnID}
	allowed:=make(map[string]bool,4);for _,key:=range keys{allowed[key]=true}
	values:=make(map[string]string,4)
	for _,header:=range message.Headers{
		if header==nil || !allowed[string(header.Key)]{return nil,messagedelivery.KafkaRecord{},messagedelivery.ErrInvalid}
		key,value:=string(header.Key),string(header.Value)
		if _,duplicate:=values[key];duplicate || len(value)>2048 || strings.ContainsAny(value,"\x00\r\n"){return nil,messagedelivery.KafkaRecord{},messagedelivery.ErrInvalid}
		values[key]=value
	}
	if values[constant.OpUserID]==""{return nil,messagedelivery.KafkaRecord{},messagedelivery.ErrInvalid}
	ctx:=parent
	ordered:=make([]string,0,4)
	for _,key:=range keys{value,ok:=values[key];if !ok{return nil,messagedelivery.KafkaRecord{},messagedelivery.ErrInvalid};ctx=context.WithValue(ctx,key,value);ordered=append(ordered,value)}
	canonical,err:=json.Marshal(ordered)
	if err!=nil{return nil,messagedelivery.KafkaRecord{},errors.New("invalid owned Kafka context")}
	digest:=sha256.Sum256(canonical)
	return ctx,messagedelivery.KafkaRecord{Topic:message.Topic,Partition:message.Partition,Offset:message.Offset,Key:append([]byte(nil),message.Key...),Body:append([]byte(nil),message.Value...),Actor:values[constant.OpUserID],OperationID:values[constant.OperationID],ContextSHA256:hex.EncodeToString(digest[:])},nil
}

// NewDurableOnlineHistoryHandler does not create the legacy100 Run goroutines
// or perform any I/O. A managed consumer lifecycle must own the supplied
// handler and close/join it; old RegisterHandleAndConsumer is NOT used for it.
func NewDurableOnlineHistoryHandler(intake *DurableKafkaIntake)(*OnlineHistoryRedisConsumerHandler,error){
	if intake==nil || intake.store==nil{return nil,messagedelivery.ErrInvalid}
	return &OnlineHistoryRedisConsumerHandler{durableIntake:intake},nil
}

// A poison/unknown record stops this owned intake instance. Only a fresh,
// explicitly restarted instance can resolve the same broker source again.
func (i *DurableKafkaIntake) recordFailure(err error){
 if i==nil || err==nil{return}
 i.failureMu.Lock();defer i.failureMu.Unlock()
 if i.firstFailure==nil{i.firstFailure=err;if i.failed!=nil{close(i.failed)}}
}
func (i *DurableKafkaIntake) failure()error{
 if i==nil{return messagedelivery.ErrUnprepared}
 i.failureMu.Lock();defer i.failureMu.Unlock();return i.firstFailure
}
