package aliasvendor

import (
 "bytes"
 "context"
 "crypto/tls"
 "encoding/base64"
 "encoding/json"
 "io"
 "net"
 "net/http"
 "net/url"
 "strconv"
 "strings"
 "sync"
 "sync/atomic"
 "time"

 legacygetui "github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/getui"
 jbody "github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/jpush/body"
)

type ownedDocument struct {
 Version string `json:"version"`
 Vendor string `json:"vendor"`
 Endpoint string `json:"endpoint"`
 AppKey string `json:"appKey"`
 MasterSecret string `json:"masterSecret"`
 ChannelID string `json:"channelID"`
 ChannelName string `json:"channelName"`
 PushIntent string `json:"pushIntent"`
 Production bool `json:"production"`
}
type Transport struct {
 config ownedDocument
 binding string
 root context.Context
 cancel context.CancelFunc
 provider,auth http.RoundTripper
 closeOwned func()
 closed atomic.Bool
 closeOnce sync.Once
 authMu sync.Mutex
 token string
 expiresMS int64
}
type AliasMessage struct {Alias,RequestID,Title,Content,ClientMsgID string}
type PreparedRequest struct {
 transport *Transport
 body []byte
 bodySHA,requestID string
 used atomic.Bool
}
func (r *PreparedRequest) BodySHA256()string{if r==nil{return ""};return r.bodySHA}
func (r *PreparedRequest) RequestID()string{if r==nil{return ""};return r.requestID}
type Result struct {PhysicalAttemptStarted,ProviderAccepted,ProviderIgnored bool;ReceiptSHA256,OutcomeSHA256 string}

// The actual filesystem loader/factory must supply the pinned owned private
// document. No ADC, SDK batch path, current YAML fallback, discovery or HTTP at
// construction. Source binding includes actual material and frozen config seed.
func NewOwnedTransport(parent context.Context,raw []byte,expectedSHA,configurationSeed string)(*Transport,error){
 if parent==nil || parent.Err()!=nil || len(raw)==0 || len(raw)>128<<10 || !validDigest(expectedSHA) || digest(raw)!=expectedSHA || !validDigest(configurationSeed){return nil,ErrInput}
 root,err:=object(raw);if err!=nil || !exact(root,"version","vendor","endpoint","appKey","masterSecret","channelID","channelName","pushIntent","production"){return nil,ErrInput}
 var config ownedDocument
 decoder:=json.NewDecoder(bytes.NewReader(raw));decoder.DisallowUnknownFields()
 if decoder.Decode(&config)!=nil || config.Version!="ordinary-alias-provider-input-v1" || config.Vendor!="getui" && config.Vendor!="jpush" || config.AppKey=="" || len(config.AppKey)>4096 || config.MasterSecret=="" || len(config.MasterSecret)>4096 || strings.ContainsAny(config.AppKey+config.MasterSecret,"\x00\r\n"){return nil,ErrInput}
 endpoint,err:=url.Parse(config.Endpoint)
 if err!=nil || endpoint.Scheme!="https" || endpoint.Hostname()=="" || endpoint.User!=nil || endpoint.RawQuery!="" || endpoint.ForceQuery || endpoint.Fragment!="" || endpoint.RawPath!="" || endpoint.Opaque!="" || endpoint.Path=="" || strings.HasSuffix(endpoint.Path,"/") || strings.ContainsAny(config.Endpoint,"\x00\r\n"){return nil,ErrInput}
 // Getui's configured base already includes appId; its exact original
 // /push/single/alias path join is retained. JPush config contains /v3/push.
 if config.Vendor=="jpush" && !strings.HasSuffix(endpoint.Path,"/v3/push"){return nil,ErrInput}
 identity,_:=json.Marshal([]string{"ordinary-alias-owned-v1",configurationSeed,expectedSHA,config.Vendor,config.Endpoint})
 provider,auth:=ownedHTTP(),ownedHTTP()
 runtime,cancel:=context.WithCancel(parent)
 return &Transport{config:config,binding:digest(identity),root:runtime,cancel:cancel,provider:provider,auth:auth,closeOwned:func(){provider.CloseIdleConnections();auth.CloseIdleConnections()}},nil
}
func ownedHTTP()*http.Transport{return &http.Transport{Proxy:http.ProxyFromEnvironment,DialContext:(&net.Dialer{Timeout:5*time.Second,KeepAlive:-1}).DialContext,TLSClientConfig:&tls.Config{MinVersion:tls.VersionTLS12},DisableKeepAlives:true,ForceAttemptHTTP2:false,TLSNextProto:map[string]func(string,*tls.Conn)http.RoundTripper{},TLSHandshakeTimeout:5*time.Second,ResponseHeaderTimeout:5*time.Second,MaxResponseHeaderBytes:1<<20}}
func (t *Transport) Binding()string{if t==nil{return ""};return t.binding}
func (t *Transport) Vendor()string{if t==nil{return ""};return t.config.Vendor}
func (t *Transport) Protocol()string{if t==nil{return ""};if t.config.Vendor=="getui"{return "getui-alias-v2"};return "jpush-alias-v3"}
func (t *Transport) CheckReady(ctx context.Context)error{if t==nil || ctx==nil || ctx.Err()!=nil || t.closed.Load() || t.root.Err()!=nil || t.provider==nil || t.auth==nil{return ErrInput};return nil}
func (t *Transport) Close()error{if t==nil{return ErrInput};t.closeOnce.Do(func(){t.closed.Store(true);t.cancel();t.closeOwned();t.authMu.Lock();t.token="";t.expiresMS=0;t.authMu.Unlock()});return nil}

// Preserve original vendor payload/channel/production contracts. JPush +1 is
// applied only through one original physical request; a successful durable
// receipt is not resent. Getui's former disabled auto_badge remains absent.
func (t *Transport) Prepare(message AliasMessage)(*PreparedRequest,error){
 if t==nil || t.closed.Load() || t.root.Err()!=nil || message.Alias=="" || len(message.Alias)>1024 || strings.ContainsAny(message.Alias,"\x00\r\n") || len(message.RequestID)!=32 || !validDigest(message.RequestID+message.RequestID){return nil,ErrInput}
 var value any
 if t.config.Vendor=="jpush"{
  extras:=jbody.Extras{ClientMsgID:message.ClientMsgID}
  notification:=jbody.Notification{Alert:message.Title,Android:jbody.Android{Alert:message.Title,Extras:extras},IOS:jbody.Ios{Alert:message.Title,Sound:"default",Badge:"+1",Extras:extras,MutableContent:true}}
  notification.Android.Intent.URL=t.config.PushIntent
  value=jbody.PushObj{Platform:"all",Audience:map[string]any{"alias":[]string{message.Alias}},Notification:&notification,Message:&jbody.Message{MsgContent:message.Content},Options:&jbody.Options{ApnsProduction:t.config.Production}}
 }else{
  value=map[string]any{"request_id":message.RequestID,"audience":map[string]any{"alias":[]string{message.Alias}},"push_message":map[string]any{"notification":map[string]any{"title":message.Title,"body":message.Content,"click_type":"startapp","channelID":t.config.ChannelID,"ChannelName":t.config.ChannelName}},"push_channel":map[string]any{"ios":map[string]any{"type":"notify","aps":map[string]any{"sound":"default","alert":map[string]string{"title":message.Title,"body":message.Content}}},"android":map[string]any{"ups":map[string]any{"notification":map[string]string{"title":message.Title,"body":message.Content,"click_type":"startapp"},"options":map[string]any{"HW":map[string]any{"/message/android/notification/default_sound":false,"/message/android/notification/channel_id":"RingRing4","/message/android/notification/sound":"/raw/ring001","/message/android/notification/importance":"NORMAL"},"XM":map[string]string{"/extra.channel_id":"high_system"},"VV":map[string]int{"/classification":1}}}}}}
 }
 // Preserve the existing exported DTO's explicit null/omitempty fields.
 // Its mutable-config-reading helper methods and logging Client are never used.
 if t.config.Vendor=="getui"{
  raw,err:=json.Marshal(value);if err!=nil{return nil,ErrInput}
  var dto legacygetui.PushReq;if json.Unmarshal(raw,&dto)!=nil{return nil,ErrInput};value=dto
 }
 body,err:=json.Marshal(value);if err!=nil || len(body)>40<<20{return nil,ErrInput}
 return &PreparedRequest{transport:t,body:body,bodySHA:digest(body),requestID:message.RequestID},nil
}

type onePhysicalPOST struct {base http.RoundTripper;endpoint,bodySHA string;before func()error;started *atomic.Bool;entered atomic.Bool}
func (g *onePhysicalPOST) RoundTrip(r *http.Request)(*http.Response,error){
 if r==nil || !g.entered.CompareAndSwap(false,true) || r.Method!=http.MethodPost || r.URL.String()!=g.endpoint || r.Body==nil || r.GetBody!=nil || r.Header.Get("Idempotency-Key")!="" || g.before==nil || g.bodySHA=="" || r.Context().Err()!=nil{return nil,ErrUnknown}
 // Verify the actual bytes at the final HTTP boundary as well as the
 // privately prepared fingerprint. This executes AFTER Getui authentication.
 body,err:=io.ReadAll(io.LimitReader(r.Body,(40<<20)+1));closeErr:=r.Body.Close()
 if err!=nil || closeErr!=nil || len(body)>40<<20 || digest(body)!=g.bodySHA || r.ContentLength!=int64(len(body)) || len(r.TransferEncoding)!=0{return nil,ErrUnknown}
 r.Body=io.NopCloser(bytes.NewReader(body))
 if err:=g.before();err!=nil{return nil,ErrUnknown};g.started.Store(true)
 return g.base.RoundTrip(r)
}
func newPOST(ctx context.Context,endpoint string,body []byte)(*http.Request,error){
 request,err:=http.NewRequestWithContext(ctx,http.MethodPost,endpoint,bytes.NewReader(body));if err!=nil{return nil,ErrInput}
 request.Header.Set("Content-Type","application/json; charset=utf-8");request.GetBody=nil;request.Close=true;return request,nil
}
func (t *Transport) authentication(ctx context.Context)(string,error){
 t.authMu.Lock();defer t.authMu.Unlock()
 now:=time.Now().UnixMilli()
 if t.token!="" && now<t.expiresMS-10000{return t.token,nil}
 stamp:=strconv.FormatInt(now,10);body,_:=json.Marshal(map[string]string{"appkey":t.config.AppKey,"timestamp":stamp,"sign":digest([]byte(t.config.AppKey+stamp+t.config.MasterSecret))})
 current,cancel:=context.WithTimeout(ctx,5*time.Second);defer cancel()
 request,err:=newPOST(current,t.config.Endpoint+"/auth",body);if err!=nil{return "",ErrUnknown}
 client:=&http.Client{Transport:t.auth,Timeout:5*time.Second,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}
 response,err:=client.Do(request);if err!=nil{return "",ErrUnknown}
 raw,readErr:=io.ReadAll(io.LimitReader(response.Body,(1<<20)+1));closeErr:=response.Body.Close()
 if readErr!=nil || closeErr!=nil || len(raw)>1<<20{return "",ErrUnknown}
 token,expiry,err:=getuiAuthResult(response.StatusCode,raw,now);if err!=nil{return "",ErrUnknown};t.token=token;t.expiresMS=expiry;return token,nil
}
func (t *Transport) Send(parent context.Context,prepared *PreparedRequest,before func()error)(Result,error){
 result:=Result{}
 if t==nil || parent==nil || prepared==nil || prepared.transport!=t || before==nil || t.CheckReady(parent)!=nil || !prepared.used.CompareAndSwap(false,true){return result,ErrInput}
 ctx,cancel:=context.WithTimeout(parent,15*time.Second);defer cancel();stop:=context.AfterFunc(t.root,cancel);defer stop()
 endpoint:=t.config.Endpoint;if t.config.Vendor=="getui"{endpoint+="/push/single/alias"}
 request,err:=newPOST(ctx,endpoint,prepared.body);if err!=nil{return result,ErrUnknown}
 if t.config.Vendor=="getui"{token,err:=t.authentication(ctx);if err!=nil{return result,ErrUnknown};request.Header.Set("token",token)}else{request.Header.Set("Authorization","Basic "+base64.StdEncoding.EncodeToString([]byte(t.config.AppKey+":"+t.config.MasterSecret)))}
 var started atomic.Bool
 gate:=&onePhysicalPOST{base:t.provider,endpoint:endpoint,bodySHA:prepared.bodySHA,before:before,started:&started}
 client:=&http.Client{Transport:gate,Timeout:15*time.Second,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}
 response,err:=client.Do(request);result.PhysicalAttemptStarted=started.Load();if err!=nil{result.OutcomeSHA256=digest([]byte("owned alias transport outcome unavailable"));return result,ErrUnknown}
 raw,readErr:=io.ReadAll(io.LimitReader(response.Body,(32<<20)+1));closeErr:=response.Body.Close();complete:=readErr==nil && closeErr==nil && len(raw)<=32<<20
 outcome,_:=json.Marshal([]any{"owned alias HTTP result",response.StatusCode,complete,digest(raw)});result.OutcomeSHA256=digest(outcome)
 result.ProviderAccepted,result.ProviderIgnored=providerOutcome(t.config.Vendor,response.StatusCode,raw,complete)
 if !result.ProviderAccepted && !result.ProviderIgnored{return result,ErrUnknown}
 proof,_:=json.Marshal([]any{"alias-provider-acceptance-not-device-delivery",t.binding,prepared.requestID,prepared.bodySHA,response.StatusCode,digest(raw)})
 result.ReceiptSHA256=digest(proof);return result,nil
}

// Metadata comparison for the current startup policy. Credentials are supplied
// only by the actual private document, never recovered from these YAML fields.
func (t *Transport) MatchesOwnedConfiguration(vendor,endpoint,channelID,channelName,pushIntent string,production bool)bool{
 if t==nil || t.closed.Load() || t.config.Vendor!=vendor || t.config.Endpoint!=endpoint{return false}
 if vendor=="getui"{return t.config.ChannelID==channelID && t.config.ChannelName==channelName}
 return t.config.PushIntent==pushIntent && t.config.Production==production
}
