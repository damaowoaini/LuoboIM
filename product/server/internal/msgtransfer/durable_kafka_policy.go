package msgtransfer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

func kafkaOwnedDigest(value any)(string,error){
	body,err:=json.Marshal(value);if err!=nil{return "",messagedelivery.ErrInvalid}
	sum:=sha256.Sum256(body);return hex.EncodeToString(sum[:]),nil
}

// FreezeOwnedKafkaPolicy reads the process-owned configuration once, before
// intake starts. It performs no I/O. Only non-secret fingerprints enter Mongo;
// matching signing/vendor adapters and their bounded lifetime are separate
// required runtime inputs, not inferred from a successful freeze.
func FreezeOwnedKafkaPolicy(database,epoch,activation string)(messagedelivery.KafkaDeliveryPolicy,error){
	if database==""{return messagedelivery.KafkaDeliveryPolicy{},messagedelivery.ErrInvalid}
	p:=messagedelivery.KafkaDeliveryPolicy{Version:"ordinary-kafka-v1",Epoch:epoch,ActivationSHA256:activation,Discovery:config.Config.Envs.Discovery,EncryptionRequired:config.Config.LongConnSvr.IsEncryption}
	bindings:=[]struct{name string;conf config.CallBackConfig;target,mode *string}{
		{"supergroup-online-push",config.Config.Callback.CallbackBeforeSuperGroupOnlinePush,&p.BeforeGroupPushBinding,&p.BeforeGroupFailureMode},
		{"online-push",config.Config.Callback.CallbackOnlinePush,&p.OnlinePushBinding,&p.OnlineFailureMode},
		{"offline-push",config.Config.Callback.CallbackOfflinePush,&p.OfflinePushBinding,&p.OfflineFailureMode},
	}
	for _,binding:=range bindings{
		if !binding.conf.Enable{continue}
		if config.Config.Callback.CallbackUrl=="" || config.Config.Secret==""{return messagedelivery.KafkaDeliveryPolicy{},messagedelivery.ErrUnprepared}
		value,err:=kafkaOwnedDigest([]any{database,epoch,binding.name,config.Config.Callback.CallbackUrl,binding.conf,config.Config.Secret})
		if err!=nil{return messagedelivery.KafkaDeliveryPolicy{},err};*binding.target=value
		*binding.mode="default-stop"
		if binding.conf.CallbackFailedContinue!=nil{*binding.mode="explicit-stop";if *binding.conf.CallbackFailedContinue{*binding.mode="continue"}}
	}
	switch config.Config.Push.Enable{
	case "getui","fcm","jpush":
		p.Vendor=config.Config.Push.Enable
		var err error;p.VendorBinding,err=kafkaOwnedDigest([]any{database,epoch,config.Config.Push,config.Config.IOSPush})
		if err!=nil{return messagedelivery.KafkaDeliveryPolicy{},err}
	case "","none":p.Vendor="none"
	default:return messagedelivery.KafkaDeliveryPolicy{},messagedelivery.ErrUnprepared
	}
	var err error
	p.ConfigurationSHA256,err=kafkaOwnedDigest([]any{database,epoch,activation,p.Discovery,p.EncryptionRequired,p.BeforeGroupPushBinding,p.OnlinePushBinding,p.OfflinePushBinding,p.Vendor,p.VendorBinding})
	if err!=nil{return messagedelivery.KafkaDeliveryPolicy{},err}
	if _,err:=p.Fingerprint();err!=nil{return messagedelivery.KafkaDeliveryPolicy{},err}
	return p,nil
}
