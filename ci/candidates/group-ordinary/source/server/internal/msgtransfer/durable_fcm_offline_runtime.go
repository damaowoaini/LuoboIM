package msgtransfer

import (
	"context"

	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/fcm"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
)

type OwnedFCMOfflineRuntime struct {
	worker *ManagedKafkaOfflineWorker
	lifetime context.Context
	cancel context.CancelFunc
	policy messagedelivery.KafkaDeliveryPolicy
}

// Concrete source wiring: private owned credentials -> actual binding policy
// -> actual cache token reader/badge ledger -> HTTP v1 adapter -> managed worker.
// It does not seed/migrate, enable a missing callback, start a worker, or remove
// the still-required whole MsgTransfer/Seq0/other-vendor activation fence.
func NewOwnedFCMOfflineRuntime(parent context.Context,db *mongo.Database,rdb redis.UniversalClient,tokens cache.MsgModel,reader messagedelivery.VisibilityReader,epoch,activation,badgeEvidenceSHA string,credentials fcm.OwnedCredentialFile)(*OwnedFCMOfflineRuntime,error){
 return NewOwnedFCMOfflineRuntimeWithRegistry(parent,db,rdb,tokens,reader,epoch,activation,badgeEvidenceSHA,credentials,nil)
}

// A retained registry is an explicit separately pinned private input. No entry
// is inferred from a stale path or substituted with current configuration.
func NewOwnedFCMOfflineRuntimeWithRegistry(parent context.Context,db *mongo.Database,rdb redis.UniversalClient,tokens cache.MsgModel,reader messagedelivery.VisibilityReader,epoch,activation,badgeEvidenceSHA string,credentials fcm.OwnedCredentialFile,retained *fcm.OwnedCredentialFile)(*OwnedFCMOfflineRuntime,error){
 if parent==nil || db==nil || rdb==nil || tokens==nil || reader==nil{return nil,messagedelivery.ErrInvalid}
	lifetime,cancel:=context.WithCancel(parent)
	store,err:=messagedelivery.NewMembershipBoundStore(db,epoch,activation);if err!=nil{cancel();return nil,err}
	policy,transport,err:=NewOwnedKafkaFCMPolicyAndTransport(lifetime,db.Name(),epoch,activation,credentials)
	if err!=nil{cancel();return nil,err}
	badge,err:=cache.NewOrdinaryBadgeLedger(rdb,store.KafkaBadgeScope(),epoch,activation,badgeEvidenceSHA)
	if err!=nil{cancel();transport.Close();return nil,messagedelivery.ErrUnprepared}
	adapter,err:=fcm.NewKafkaHTTPV1OfflineAdapter(store,policy,tokens,badge,transport)
	if err!=nil{cancel();transport.Close();return nil,err}
 adapters:=[]OwnedKafkaOfflineTransport{adapter}
 closeAdapters:=func(){for _,owned:=range adapters{_ = owned.Close()}}
 if retained!=nil{
  materials,err:=fcm.LoadOwnedKafkaFCMPolicies(lifetime,*retained,db.Name(),epoch,activation)
  if err!=nil{cancel();closeAdapters();return nil,messagedelivery.ErrUnprepared}
  for i,material:=range materials{
   owned,err:=fcm.NewKafkaHTTPV1OfflineAdapter(store,material.Policy,tokens,badge,material.Transport)
   if err!=nil{
    cancel();closeAdapters();for _,remaining:=range materials[i:]{_ = remaining.Transport.Close()};return nil,messagedelivery.ErrUnprepared
   }
   adapters=append(adapters,owned)
  }
 }
 worker,err:=NewManagedKafkaOfflineWorker(store,reader,adapters)
 if err!=nil{cancel();closeAdapters();return nil,err}
	return &OwnedFCMOfflineRuntime{worker:worker,lifetime:lifetime,cancel:cancel,policy:policy},nil
}

// Credentials, token refresh and worker dispatch use the SAME managed root.
// Startup readiness uses its own bounded child; a startup timeout is never
// retained as the lifetime of a successfully started OAuth token source.
func (r *OwnedFCMOfflineRuntime) Start()error{if r==nil{return messagedelivery.ErrInvalid};return r.worker.Start(r.lifetime)}
func (r *OwnedFCMOfflineRuntime) Stop(ctx context.Context)error{if r==nil{return messagedelivery.ErrInvalid};r.cancel();return r.worker.Stop(ctx)}
func (r *OwnedFCMOfflineRuntime) Policy()messagedelivery.KafkaDeliveryPolicy{if r==nil{return messagedelivery.KafkaDeliveryPolicy{}};return r.policy}
func (r *OwnedFCMOfflineRuntime) DrainSummary()(KafkaOfflineDrainSummary,error){if r==nil{return KafkaOfflineDrainSummary{},messagedelivery.ErrInvalid};return r.worker.DrainSummary()}
