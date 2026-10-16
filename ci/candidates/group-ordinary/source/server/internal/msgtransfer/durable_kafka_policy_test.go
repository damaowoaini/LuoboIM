package msgtransfer

import (
	"testing"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

func TestOwnedKafkaPolicyPreservesEnabledCallbackAndVendorBindingsAcrossConfigChanges(t *testing.T){
	old:=config.Config;t.Cleanup(func(){config.Config=old})
	config.Config.Envs.Discovery="zookeeper"
	config.Config.Push.Enable="getui"
	config.Config.Push.GeTui.AppKey="synthetic-key"
	config.Config.Push.GeTui.MasterSecret="synthetic-secret"
	config.Config.Callback.CallbackUrl="https://synthetic.invalid/callback"
	config.Config.Secret="synthetic-signing-channel"
	config.Config.Callback.CallbackBeforeSuperGroupOnlinePush.Enable=true
	config.Config.Callback.CallbackOnlinePush.Enable=true
	config.Config.Callback.CallbackOfflinePush.Enable=true
	config.Config.Callback.CallbackOfflinePush.CallbackTimeOut=2
	epoch:="c2fb972f-c1ae-4d34-b937-2ec837d7529d"
	activation:="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	first,err:=FreezeOwnedKafkaPolicy("synthetic-task-db",epoch,activation);if err!=nil{t.Fatal(err)}
	before,err:=first.Fingerprint();if err!=nil || first.BeforeGroupPushBinding=="" || first.OnlinePushBinding=="" || first.OfflinePushBinding=="" || first.VendorBinding=="" || first.Vendor!="getui"{t.Fatal("enabled configuration was silently disabled",err)}
	config.Config.Callback.CallbackOfflinePush.Enable=false
	config.Config.Push.GeTui.MasterSecret="synthetic-later-secret"
	later,err:=FreezeOwnedKafkaPolicy("synthetic-task-db",epoch,activation);if err!=nil{t.Fatal(err)}
	after,err:=later.Fingerprint();if err!=nil || after==before || later.OfflinePushBinding!="" || later.VendorBinding==first.VendorBinding{t.Fatal("new runtime configuration borrowed old bindings",err)}
	unchanged,err:=first.Fingerprint();if err!=nil || unchanged!=before || first.OfflinePushBinding==""{t.Fatal("original immutable snapshot borrowed mutable current config",err)}
}

func TestOwnedKafkaPolicyKeepsCallbackTriStateAndRejectsUnknownVendor(t *testing.T){
	old:=config.Config;t.Cleanup(func(){config.Config=old})
	config.Config.Envs.Discovery="k8s";config.Config.Push.Enable="none"
	config.Config.Callback.CallbackUrl="https://synthetic.invalid/callback";config.Config.Secret="synthetic-signing"
	config.Config.Callback.CallbackOfflinePush.Enable=true
	config.Config.Callback.CallbackOfflinePush.CallbackFailedContinue=nil
	epoch:="c2fb972f-c1ae-4d34-b937-2ec837d7529d";activation:="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	first,err:=FreezeOwnedKafkaPolicy("synthetic-db",epoch,activation);if err!=nil{t.Fatal(err)}
	no:=false;config.Config.Callback.CallbackOfflinePush.CallbackFailedContinue=&no
	second,err:=FreezeOwnedKafkaPolicy("synthetic-db",epoch,activation);if err!=nil || first.OfflinePushBinding==second.OfflinePushBinding{t.Fatal("nil and explicit false callback contract were collapsed",err)}
	config.Config.Push.Enable="unrecognized-configured-vendor"
	if _,err:=FreezeOwnedKafkaPolicy("synthetic-db",epoch,activation);err==nil{t.Fatal("unknown vendor became a dummy successful adapter")}
}
