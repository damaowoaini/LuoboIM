package msgtransfer

import (
	"context"
	"path/filepath"

	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/fcm"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

// Capture the process-owned configuration before file I/O. No request metadata
// can select credentials/project/scope. The seed configuration digest is not
// the final policy digest: actual credential bytes bind VendorBinding first,
// then ConfigurationSHA256 is recomputed with that binding (no hash cycle).
func NewOwnedKafkaFCMPolicyAndTransport(ctx context.Context,database,epoch,activation string,input fcm.OwnedCredentialFile)(messagedelivery.KafkaDeliveryPolicy,*fcm.HTTPV1Transport,error){
	if ctx==nil{return messagedelivery.KafkaDeliveryPolicy{},nil,messagedelivery.ErrInvalid}
	policy,err:=FreezeOwnedKafkaPolicy(database,epoch,activation)
	if err!=nil || policy.Vendor!="fcm"{return messagedelivery.KafkaDeliveryPolicy{},nil,messagedelivery.ErrUnprepared}
	configured,err:=filepath.Abs(config.Config.Push.Fcm.ServiceAccount)
	if err!=nil || config.Config.Push.Fcm.ServiceAccount=="" || configured!=input.Path{return messagedelivery.KafkaDeliveryPolicy{},nil,messagedelivery.ErrUnprepared}
	seed:=policy.ConfigurationSHA256
	transport,err:=fcm.NewOwnedHTTPV1TransportFromFile(ctx,input,seed)
	if err!=nil{return messagedelivery.KafkaDeliveryPolicy{},nil,messagedelivery.ErrUnprepared}
	current,err:=FreezeOwnedKafkaPolicy(database,epoch,activation)
	if err!=nil || current!=policy{transport.Close();return messagedelivery.KafkaDeliveryPolicy{},nil,messagedelivery.ErrUnprepared}
	policy.VendorBinding=transport.Binding()
	policy.ConfigurationSHA256,err=kafkaOwnedDigest([]any{database,epoch,activation,policy.Discovery,policy.EncryptionRequired,policy.BeforeGroupPushBinding,policy.OnlinePushBinding,policy.OfflinePushBinding,policy.Vendor,policy.VendorBinding})
	if err!=nil{transport.Close();return messagedelivery.KafkaDeliveryPolicy{},nil,messagedelivery.ErrUnprepared}
	if _,err:=policy.Fingerprint();err!=nil{transport.Close();return messagedelivery.KafkaDeliveryPolicy{},nil,messagedelivery.ErrUnprepared}
	return policy,transport,nil
}
