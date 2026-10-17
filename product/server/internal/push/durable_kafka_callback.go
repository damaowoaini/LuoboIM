package push

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/pkg/callbackstruct"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

// The request includes escaped source content plus the frozen roster. Do not
// impose the former draft's 1MiB limit on otherwise admitted message/roster
// data. These transport ceilings exceed the intake's 4MiB protobuf and 8MiB
// serialized roster bounds; an oversized response is unknown, never an ACK.
const kafkaCallbackRequestLimit = 40 << 20
const kafkaCallbackBodyLimit = 32 << 20

type frozenKafkaCallbackEndpoint struct {
	base, binding, mode, command string
	secret []byte
	timeout time.Duration
}

// This holds only process-owned configuration. Its signing material is never
// put in Mongo, status, errors or logs. An old committed policy needs its exact
// old adapter after restart: substituting current configuration is rejected.
// No constructor I/O, incoming URL or incoming callback identity is accepted.
type FrozenKafkaPushCallbacks struct {
	store *messagedelivery.Store
	policySHA string
	endpoints map[string]frozenKafkaCallbackEndpoint
}

func kafkaCallbackBinding(database,epoch,name,base,secret string,conf config.CallBackConfig)(string,error){
	body,err:=json.Marshal([]any{database,epoch,name,base,conf,secret})
	if err!=nil{return "",messagedelivery.ErrInvalid};return digestGateway(body),nil
}

func freezeKafkaCallbackEndpoint(database,epoch,logical,command,base,secret,expected,mode string,conf config.CallBackConfig)(frozenKafkaCallbackEndpoint,error){
	if !conf.Enable{
		if expected!="" || mode!=""{return frozenKafkaCallbackEndpoint{},messagedelivery.ErrUnprepared}
		return frozenKafkaCallbackEndpoint{},nil
	}
	if database=="" || secret=="" || len(secret)>4096 || expected==""{return frozenKafkaCallbackEndpoint{},messagedelivery.ErrInvalid}
	u,err:=neturl.Parse(base)
	if err!=nil || u.Host=="" || u.Hostname()=="" || u.User!=nil || u.RawQuery!="" || u.ForceQuery || u.Fragment!="" || u.Opaque!="" || u.RawPath!="" || u.Scheme!="https" && u.Scheme!="http"{return frozenKafkaCallbackEndpoint{},messagedelivery.ErrInvalid}
	clean:=strings.TrimRight(u.Path,"/")
	if clean!="" && path.Clean(clean)!=clean{return frozenKafkaCallbackEndpoint{},messagedelivery.ErrInvalid}
	actual,err:=kafkaCallbackBinding(database,epoch,logical,base,secret,conf)
	if err!=nil || actual!=expected{return frozenKafkaCallbackEndpoint{},messagedelivery.ErrUnprepared}
	failureMode:="default-stop"
	if conf.CallbackFailedContinue!=nil{failureMode="explicit-stop";if *conf.CallbackFailedContinue{failureMode="continue"}}
	if mode!=failureMode{return frozenKafkaCallbackEndpoint{},messagedelivery.ErrUnprepared}
	timeout:=15*time.Second
	if conf.CallbackTimeOut>0 && conf.CallbackTimeOut<15{timeout=time.Duration(conf.CallbackTimeOut)*time.Second}
	// The binding uses the original configured base (including its trailing
	// slash) exactly. The actual request keeps the legacy base+"/"+command join.
	return frozenKafkaCallbackEndpoint{base:base,binding:actual,mode:mode,command:command,secret:[]byte(secret),timeout:timeout},nil
}

func NewFrozenKafkaPushCallbacks(store *messagedelivery.Store,database string,policy messagedelivery.KafkaDeliveryPolicy,base,secret string,group,online config.CallBackConfig)(*FrozenKafkaPushCallbacks,error){
	policySHA,err:=policy.Fingerprint()
	if err!=nil || store==nil || database==""{return nil,messagedelivery.ErrInvalid}
	endpoints:=map[string]frozenKafkaCallbackEndpoint{}
	for _,entry:=range []struct{kind,logical,command,binding,mode string;conf config.CallBackConfig}{
		{"before-group-push","supergroup-online-push",callbackstruct.CallbackSuperGroupOnlinePushCommand,policy.BeforeGroupPushBinding,policy.BeforeGroupFailureMode,group},
		{"online-push","online-push",callbackstruct.CallbackOnlinePushCommand,policy.OnlinePushBinding,policy.OnlineFailureMode,online},
	}{
		endpoint,err:=freezeKafkaCallbackEndpoint(database,policy.Epoch,entry.logical,entry.command,base,secret,entry.binding,entry.mode,entry.conf)
		if err!=nil{return nil,err};if endpoint.binding!=""{endpoints[entry.kind]=endpoint}
	}
	return &FrozenKafkaPushCallbacks{store:store,policySHA:policySHA,endpoints:endpoints},nil
}

// Exact legacy DTO/content extraction is retained. In particular, online
// callback response recipient/content overrides remain ignored, as before.
// Offline callback filtering/modification is a separate required duty.
func kafkaPushCallbackBody(input messagedelivery.KafkaPushCallbackInput)(string,[]byte,error){
	m:=input.Message
	if m==nil{return "",nil,messagedelivery.ErrInvalid}
	base:=callbackstruct.UserStatusBaseCallback{OperationID:input.OperationID,PlatformID:int(m.SenderPlatformID),Platform:constant.PlatformIDToName(int(m.SenderPlatformID))}
	var request any
	switch input.Kind{
	case "before-group-push":
		base.CallbackCommand=callbackstruct.CallbackSuperGroupOnlinePushCommand
		request=callbackstruct.CallbackBeforeSuperGroupOnlinePushReq{UserStatusBaseCallback:base,ClientMsgID:m.ClientMsgID,SendID:m.SendID,GroupID:m.GroupID,ContentType:m.ContentType,SessionType:m.SessionType,AtUserIDs:append([]string(nil),m.AtUserIDList...),Content:GetContent(m),Seq:m.Seq}
	case "online-push":
		base.CallbackCommand=callbackstruct.CallbackOnlinePushCommand
		request=callbackstruct.CallbackBeforePushReq{UserStatusBatchCallbackReq:callbackstruct.UserStatusBatchCallbackReq{UserStatusBaseCallback:base,UserIDList:append([]string(nil),input.Users...)},ClientMsgID:m.ClientMsgID,SendID:m.SendID,GroupID:m.GroupID,ContentType:m.ContentType,SessionType:m.SessionType,AtUserIDs:append([]string(nil),m.AtUserIDList...),Content:GetContent(m)}
	default:return "",nil,messagedelivery.ErrInvalid
	}
	body,err:=json.Marshal(request)
	if err!=nil || len(body)>kafkaCallbackRequestLimit{return "",nil,messagedelivery.ErrInvalid}
	return base.CallbackCommand,body,nil
}

func kafkaCallbackRequest(ctx context.Context,endpoint frozenKafkaCallbackEndpoint,operation string,body []byte)(*http.Request,error){
	request,err:=http.NewRequestWithContext(ctx,http.MethodPost,endpoint.base+"/"+endpoint.command,bytes.NewReader(body))
	if err!=nil{return nil,messagedelivery.ErrInvalid}
	var nonce [16]byte
	if _,err:=rand.Read(nonce[:]);err!=nil{return nil,messagedelivery.ErrUnprepared}
	stamp,nonceHex:=strconv.FormatInt(time.Now().Unix(),10),hex.EncodeToString(nonce[:])
	mac:=hmac.New(sha256.New,endpoint.secret)
	mac.Write([]byte(stamp+"\n"+nonceHex+"\n"+endpoint.command+"\n"));mac.Write(body)
	request.Header.Set("Content-Type","application/json; charset=utf-8")
	request.Header.Set("X-OpenIM-Callback-Timestamp",stamp)
	request.Header.Set("X-OpenIM-Callback-Nonce",nonceHex)
	request.Header.Set("X-OpenIM-Callback-Signature",hex.EncodeToString(mac.Sum(nil)))
	request.Header.Set(constant.OperationID,operation)
	// No Idempotency-Key, redirect, replayable request body or reused connection
	// is supplied: the callback has not promised idempotency. This is one Do,
	// not a claim that a remote system applied it exactly once.
	request.GetBody=nil
	request.Close=true
	return request,nil
}

func kafkaCallbackResponse(status int,body []byte,complete bool)string{
	if status==http.StatusUnauthorized || status==http.StatusForbidden{return "rejected"}
	if !complete || status<200 || status>=300{return "unknown"}
	decoder:=json.NewDecoder(bytes.NewReader(body))
	start,err:=decoder.Token();if err!=nil || start!=json.Delim('{'){return "unknown"}
	seen:=map[string]bool{}
	for decoder.More(){
		key,err:=decoder.Token();name,ok:=key.(string);name=strings.ToLower(name)
		if err!=nil || !ok || seen[name]{return "unknown"};seen[name]=true
		var value json.RawMessage;if decoder.Decode(&value)!=nil{return "unknown"}
	}
	end,err:=decoder.Token()
	if err!=nil || end!=json.Delim('}') || decoder.Decode(new(any))!=io.EOF{return "unknown"}
	var response callbackstruct.CommonCallbackResp
	if json.Unmarshal(body,&response)!=nil{return "unknown"}
	// Preserve CommonCallbackResp.Parse: actionCode/nextCode determine semantic
	// rejection. An errCode alone was not a legacy rejection when actionCode=0.
	if response.ActionCode!=0 || response.NextCode==callbackstruct.Next{return "rejected"}
	return "acknowledged"
}

func (e *FrozenKafkaPushCallbacks) Execute(ctx context.Context,call messagedelivery.OnlineCall)error{
	if e==nil || e.store==nil || ctx==nil || call.PolicySHA256!=e.policySHA{return messagedelivery.ErrUnprepared}
	input,err:=e.store.KafkaPushCallbackInput(ctx,call);if err!=nil{return err}
	if input.Binding==""{return e.store.RequireKafkaPushCallbacks(ctx,call)}
	endpoint,ok:=e.endpoints[input.Kind]
	if !ok || endpoint.binding!=input.Binding || endpoint.mode!=input.FailureMode{return messagedelivery.ErrUnprepared}
	command,body,err:=kafkaPushCallbackBody(input)
	if err!=nil || command!=endpoint.command{return messagedelivery.ErrInvalid}
	current,cancel:=context.WithTimeout(ctx,endpoint.timeout);defer cancel()
	request,err:=kafkaCallbackRequest(current,endpoint,input.OperationID,body);if err!=nil{return err}
	dispatch,err:=e.store.BeginKafkaPushCallback(current,call,digestGateway(body));if err!=nil{return err}
	if dispatch==nil{return e.store.RequireKafkaPushCallbacks(current,call)}
	if err:=dispatch.BeforeHTTP(current,call,body);err!=nil{
		if dispatch.NotDispatched(ctx)!=nil{return messagedelivery.ErrKafkaPushCallbackUnconfirmed};return err
	}
	transport:=&http.Transport{DialContext:(&net.Dialer{Timeout:2*time.Second}).DialContext,DisableKeepAlives:true,ResponseHeaderTimeout:endpoint.timeout,TLSHandshakeTimeout:2*time.Second,MaxResponseHeaderBytes:1<<20,TLSNextProto:map[string]func(string,*tls.Conn)http.RoundTripper{}}
	defer transport.CloseIdleConnections()
	client:=&http.Client{Transport:transport,Timeout:endpoint.timeout,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}
	if err:=current.Err();err!=nil{
		if dispatch.NotDispatched(ctx)!=nil{return messagedelivery.ErrKafkaPushCallbackUnconfirmed};return err
	}
	// After this line every failure is dispatched/unknown, even cancellation or
	// a dial failure; no knowledge of non-application is invented from an error.
	response,err:=client.Do(request)
	if err!=nil{
		return dispatch.Finish(ctx,"unknown",digestGateway([]byte("callback transport result unavailable")))
	}
	reply,readErr:=io.ReadAll(io.LimitReader(response.Body,kafkaCallbackBodyLimit+1))
	closeErr:=response.Body.Close()
	complete:=readErr==nil && closeErr==nil && len(reply)<=kafkaCallbackBodyLimit
	outcome:=kafkaCallbackResponse(response.StatusCode,reply,complete)
	proof,marshalErr:=json.Marshal([]any{response.StatusCode,complete,digestGateway(reply)})
	if marshalErr!=nil{return messagedelivery.ErrKafkaPushCallbackUnconfirmed}
	if err:=dispatch.Finish(ctx,outcome,digestGateway(proof));err!=nil{return err}
	return e.store.RequireKafkaPushCallbacks(current,call)
}
