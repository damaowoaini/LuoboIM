package push

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/pkg/callbackstruct"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

func TestKafkaPushCallbackPreservesLegacyOnlineDTOsAndContentExtraction(t *testing.T){
	message:=&sdkws.MsgData{SendID:"synthetic-sender",GroupID:"synthetic-group",ClientMsgID:"synthetic-stable-message",ContentType:constant.GroupInfoSetNotification,SessionType:constant.SuperGroupChatType,SenderPlatformID:constant.AndroidPlatformID,AtUserIDList:[]string{"synthetic-member"},Content:[]byte(`{"detail":"synthetic original notification"}`),Seq:23}
	input:=messagedelivery.KafkaPushCallbackInput{OperationID:"synthetic-original-operation",Message:message,Users:[]string{"synthetic-a","synthetic-b"}}
	base:=callbackstruct.UserStatusBaseCallback{OperationID:input.OperationID,PlatformID:int(message.SenderPlatformID),Platform:constant.PlatformIDToName(int(message.SenderPlatformID))}
	for _,kind:=range []string{"before-group-push","online-push"}{
		t.Run(kind,func(t *testing.T){
			input.Kind=kind
			command,body,err:=kafkaPushCallbackBody(input);if err!=nil{t.Fatal(err)}
			base.CallbackCommand=command
			var legacy any
			if kind=="before-group-push"{
				legacy=callbackstruct.CallbackBeforeSuperGroupOnlinePushReq{UserStatusBaseCallback:base,ClientMsgID:message.ClientMsgID,SendID:message.SendID,GroupID:message.GroupID,ContentType:message.ContentType,SessionType:message.SessionType,AtUserIDs:message.AtUserIDList,Content:GetContent(message),Seq:message.Seq}
			}else{
				legacy=callbackstruct.CallbackBeforePushReq{UserStatusBatchCallbackReq:callbackstruct.UserStatusBatchCallbackReq{UserStatusBaseCallback:base,UserIDList:input.Users},ClientMsgID:message.ClientMsgID,SendID:message.SendID,GroupID:message.GroupID,ContentType:message.ContentType,SessionType:message.SessionType,AtUserIDs:message.AtUserIDList,Content:GetContent(message)}
			}
			want,err:=json.Marshal(legacy)
			if err!=nil || !bytes.Equal(body,want){t.Fatal("durable adapter changed the existing callback wire DTO/content extraction")}
		})
	}
}

func TestKafkaPushCallbackEndpointFreezesExactOwnedPolicyAndContinuationMode(t *testing.T){
	for _,mode:=range []string{"default-stop","explicit-stop","continue"}{
		t.Run(mode,func(t *testing.T){
			conf:=config.CallBackConfig{Enable:true,CallbackTimeOut:3}
			if mode!="default-stop"{value:=mode=="continue";conf.CallbackFailedContinue=&value}
			binding,err:=kafkaCallbackBinding("synthetic-owned-db","synthetic-epoch","online-push","https://callback.fixture.invalid/base","synthetic-signing-only",conf);if err!=nil{t.Fatal(err)}
			e,err:=freezeKafkaCallbackEndpoint("synthetic-owned-db","synthetic-epoch","online-push",callbackstruct.CallbackOnlinePushCommand,"https://callback.fixture.invalid/base","synthetic-signing-only",binding,mode,conf)
			if err!=nil || e.binding!=binding || e.mode!=mode || e.timeout!=3*time.Second{t.Fatal("exact owned policy not retained",err)}
			conf.Enable=false
			if e.binding!=binding || e.mode!=mode{t.Fatal("mutable configuration changed the frozen adapter")}
			if _,err:=freezeKafkaCallbackEndpoint("foreign-db","synthetic-epoch","online-push",callbackstruct.CallbackOnlinePushCommand,e.base,"synthetic-signing-only",binding,mode,config.CallBackConfig{Enable:true});err==nil{t.Fatal("foreign namespace/config borrowed an existing binding")}
		})
	}
	for _,base:=range []string{"https://u:p@callback.fixture.invalid","https://callback.fixture.invalid/?x=1","https://callback.fixture.invalid/#x","https://callback.fixture.invalid/a/../b","https://callback.fixture.invalid/%2e%2e/b"}{
		conf:=config.CallBackConfig{Enable:true};binding,_:=kafkaCallbackBinding("db","epoch","online-push",base,"synthetic",conf)
		if _,err:=freezeKafkaCallbackEndpoint("db","epoch","online-push","command",base,"synthetic",binding,"default-stop",conf);err==nil{t.Fatal("ambiguous callback endpoint accepted")}
	}
}

func TestKafkaPushCallbackResponseSeparatesSemanticRejectionFromUnknownTransport(t *testing.T){
	for _,test:=range []struct{name string;status int;body string;complete bool;want string}{
		{"normal",200,`{"actionCode":0,"errCode":0}`,true,"acknowledged"},
		{"legacy-empty-object",200,`{}`,true,"acknowledged"},
		{"legacy-nonzero-error-with-next-zero",200,`{"actionCode":0,"errCode":1004,"nextCode":0}`,true,"acknowledged"},
		{"explicit-action-reject",200,`{"actionCode":1,"errCode":1002}`,true,"rejected"},
		{"next-reject",200,`{"nextCode":1}`,true,"rejected"},
		{"authentication-reject",401,`not json`,false,"rejected"},
		{"authorization-reject",403,`{}`,true,"rejected"},
		{"server-unknown",503,`{}`,true,"unknown"},
		{"redirect-never-followed",302,`{}`,true,"unknown"},
		{"truncated",200,`{}`,false,"unknown"},
		{"null",200,`null`,true,"unknown"},
		{"duplicate-permission-field",200,`{"actionCode":1,"ActionCode":0}`,true,"unknown"},
		{"trailing-document",200,`{} {}`,true,"unknown"},
	}{t.Run(test.name,func(t *testing.T){if got:=kafkaCallbackResponse(test.status,[]byte(test.body),test.complete);got!=test.want{t.Fatal("callback outcome misclassified",got,test.want)}})}
}

func TestKafkaPushCallbackRequestUsesOriginalSigningContractWithoutReplayableBody(t *testing.T){
	endpoint:=frozenKafkaCallbackEndpoint{base:"https://callback.fixture.invalid",command:callbackstruct.CallbackOnlinePushCommand,secret:[]byte("synthetic-only-signing")}
	body:=[]byte(`{"synthetic":true}`)
	ctx,cancel:=context.WithTimeout(context.Background(),time.Second);defer cancel()
	request,err:=kafkaCallbackRequest(ctx,endpoint,"synthetic-original-operation",body);if err!=nil{t.Fatal(err)}
	defer request.Body.Close()
	actual,err:=io.ReadAll(request.Body)
	if err!=nil || !bytes.Equal(actual,body) || request.GetBody!=nil || !request.Close || request.Header.Get("Idempotency-Key")!="" || request.Context()!=ctx || request.URL.Path!="/"+endpoint.command{t.Fatal("request was retargeted, made replayable or lost its bounded context")}
	stamp,nonce:=request.Header.Get("X-OpenIM-Callback-Timestamp"),request.Header.Get("X-OpenIM-Callback-Nonce")
	decoded,err:=hex.DecodeString(nonce);if err!=nil || len(decoded)!=16 || stamp==""{t.Fatal("invalid signing nonce/timestamp")}
	mac:=hmac.New(sha256.New,endpoint.secret);mac.Write([]byte(stamp+"\n"+nonce+"\n"+endpoint.command+"\n"));mac.Write(body)
	if request.Header.Get("X-OpenIM-Callback-Signature")!=hex.EncodeToString(mac.Sum(nil)) || request.Header.Get(constant.OperationID)!="synthetic-original-operation"{t.Fatal("callback signing differs from the existing server contract")}
}
