package aliasvendor

import (
 "context"
 "encoding/json"
 "errors"
 "io"
 "net/http"
 "strings"
 "sync/atomic"
 "testing"
)

type aliasRT func(*http.Request)(*http.Response,error)
func (f aliasRT) RoundTrip(r *http.Request)(*http.Response,error){return f(r)}
func aliasResponse(status int,body string)*http.Response{return &http.Response{StatusCode:status,Header:make(http.Header),Body:io.NopCloser(strings.NewReader(body))}}
func aliasTransportFixture(t *testing.T,vendor string)*Transport{
 t.Helper();endpoint:="https://synthetic.invalid/v3/push";if vendor=="getui"{endpoint="https://synthetic.invalid/v2/synthetic-app"}
 document:=ownedDocument{Version:"ordinary-alias-provider-input-v1",Vendor:vendor,Endpoint:endpoint,AppKey:"synthetic-app-key",MasterSecret:"synthetic-test-only-secret",ChannelID:"synthetic-channel",ChannelName:"synthetic-channel-name",PushIntent:"intent:#Intent;end",Production:true}
 raw,err:=json.Marshal(document);if err!=nil{t.Fatal(err)}
 transport,err:=NewOwnedTransport(context.Background(),raw,digest(raw),digest([]byte("synthetic source-owned policy seed")));if err!=nil{t.Fatal("pure construction rejected",err)}
 t.Cleanup(func(){if err:=transport.Close();err!=nil{t.Error("owned pool cleanup rejected")}})
 return transport
}
func aliasPreparedFixture(t *testing.T,transport *Transport)*PreparedRequest{
 t.Helper();value,err:=transport.Prepare(AliasMessage{Alias:"synthetic-member",RequestID:strings.Repeat("a",32),Title:"synthetic title",Content:"synthetic content",ClientMsgID:"synthetic-client-message"});if err!=nil{t.Fatal(err)};return value
}

func TestAliasHTTPValidProviderAcceptanceUsesOneRequestAndDoesNotClaimDeviceDelivery(t *testing.T){
 for _,vendor:=range []string{"getui","jpush"}{t.Run(vendor,func(t *testing.T){
  transport:=aliasTransportFixture(t,vendor);var authCalls,posts atomic.Int64
  // Auth/provider boundary is in-process only. No localhost server or supplier
  // token/credential is used. The fake service token never enters output.
  transport.auth=aliasRT(func(*http.Request)(*http.Response,error){authCalls.Add(1);return aliasResponse(200,`{"code":0,"msg":"","data":{"token":"synthetic-service-only","expire_time":"4102444800000"}}`),nil})
  transport.provider=aliasRT(func(request *http.Request)(*http.Response,error){posts.Add(1);if request.GetBody!=nil || !request.Close{t.Error("supplier POST became replayable")};if vendor=="jpush"{return aliasResponse(200,`{"sendno":"0","msg_id":"123456789"}`),nil};return aliasResponse(200,`{"code":0,"msg":"","data":{"synthetic-task":{"synthetic-cid":"successed_online"}}}`),nil})
  prepared:=aliasPreparedFixture(t,transport);guards:=0
  outcome,err:=transport.Send(context.Background(),prepared,func()error{guards++;if vendor=="getui" && authCalls.Load()!=1{t.Error("final source/privacy guard ran before auth")};return nil})
  if err!=nil || !outcome.PhysicalAttemptStarted || !outcome.ProviderAccepted || outcome.ProviderIgnored || !validDigest(outcome.ReceiptSHA256) || !validDigest(outcome.OutcomeSHA256) || posts.Load()!=1 || guards!=1{t.Fatal("exact supplier acceptance was not bounded to one POST",err)}
  if _,err:=transport.Send(context.Background(),prepared,func()error{return nil});err==nil || posts.Load()!=1{t.Fatal("same prepared alias request was replayed")}
 })}
}
func TestAliasHTTPAmbiguousOrLostResponseNeverRetriesOrClaimsAcceptance(t *testing.T){
 for _,test:=range []struct{name,vendor,body string;transportFailure bool}{
  {"lost-ack","jpush","",true},
  {"jpush-error-case","jpush",`{"sendno":"0","msg_id":"123","Error":{"code":1}}`,false},
  {"jpush-duplicate","jpush",`{"sendno":"0","msg_id":"123","msg_id":"456"}`,false},
  {"jpush-wrong-correlation","jpush",`{"sendno":"1","msg_id":"123"}`,false},
  {"getui-nested-duplicate","getui",`{"code":0,"msg":"","data":{"task":{"cid":"successed_online","cid":"successed_ignore"}}}`,false},
  {"getui-unknown-status","getui",`{"code":0,"msg":"","data":{"task":{"cid":"unknown"}}}`,false},
  {"getui-extra-task","getui",`{"code":0,"msg":"","data":{"task1":{"cid":"successed_online"},"task2":{"other":"successed_online"}}}`,false},
  {"getui-wrong-code-case","getui",`{"Code":0,"msg":"","data":{"task":{"cid":"successed_online"}}}`,false},
 }{t.Run(test.name,func(t *testing.T){
  transport:=aliasTransportFixture(t,test.vendor);posts:=0
  transport.auth=aliasRT(func(*http.Request)(*http.Response,error){return aliasResponse(200,`{"code":0,"msg":"","data":{"token":"synthetic-service-only","expire_time":"4102444800000"}}`),nil})
  transport.provider=aliasRT(func(*http.Request)(*http.Response,error){posts++;if test.transportFailure{return nil,errors.New("synthetic lost ACK")};return aliasResponse(200,test.body),nil})
  prepared:=aliasPreparedFixture(t,transport);outcome,err:=transport.Send(context.Background(),prepared,func()error{return nil})
  if !errors.Is(err,ErrUnknown) || !outcome.PhysicalAttemptStarted || outcome.ProviderAccepted || outcome.ProviderIgnored || outcome.ReceiptSHA256!="" || posts!=1 || !validDigest(outcome.OutcomeSHA256){t.Fatal("ambiguous result became an acceptance",err)}
  if _,err:=transport.Send(context.Background(),prepared,func()error{return nil});err==nil || posts!=1{t.Fatal("unknown result automatically retried")}
 })}
}
func TestAliasHTTPFinalSourceGuardAndActualBodyHashPreventWrongPOST(t *testing.T){
 transport:=aliasTransportFixture(t,"getui");auth,posts:=0,0
 transport.auth=aliasRT(func(*http.Request)(*http.Response,error){auth++;return aliasResponse(200,`{"code":0,"msg":"","data":{"token":"synthetic-service-only","expire_time":"4102444800000"}}`),nil})
 transport.provider=aliasRT(func(*http.Request)(*http.Response,error){posts++;return aliasResponse(200,`{"code":0,"msg":"","data":{"task":{"cid":"successed_online"}}}`),nil})
 outcome,err:=transport.Send(context.Background(),aliasPreparedFixture(t,transport),func()error{return errors.New("synthetic owner clear after auth")})
 if err==nil || outcome.PhysicalAttemptStarted || posts!=0 || auth!=1{t.Fatal("stale source/privacy guard reached provider")}
 changed:=aliasPreparedFixture(t,transport);changed.body=append(changed.body,[]byte("changed-after-frozen-hash")...);guards:=0
 outcome,err=transport.Send(context.Background(),changed,func()error{guards++;return nil})
 if err==nil || outcome.PhysicalAttemptStarted || posts!=0 || guards!=0{t.Fatal("wrong actual request body consumed source capability")}
 outcome,err=transport.Send(context.Background(),aliasPreparedFixture(t,transport),func()error{return nil})
 if err!=nil || !outcome.ProviderAccepted || posts!=1{t.Fatal("one denied request blocked another independent request",err)}
}
func TestAliasHTTPKnownIgnoreAndLegacyBadgeChannelsRemainExplicit(t *testing.T){
 getui:=aliasTransportFixture(t,"getui");prepared:=aliasPreparedFixture(t,getui)
 var raw map[string]any;if json.Unmarshal(prepared.body,&raw)!=nil{t.Fatal("owned Getui payload invalid")}
 ios:=raw["push_channel"].(map[string]any)["ios"].(map[string]any)
 if ios["auto_badge"]!=nil{t.Fatal("Getui acquired a new automatic badge policy")}
 jpush:=aliasTransportFixture(t,"jpush");prepared=aliasPreparedFixture(t,jpush)
 raw=nil;if json.Unmarshal(prepared.body,&raw)!=nil{t.Fatal("owned JPush payload invalid")}
 notification:=raw["notification"].(map[string]any)
 if notification["ios"].(map[string]any)["badge"]!="+1" || notification["ios"].(map[string]any)["mutable-content"]!=true || raw["options"].(map[string]any)["apns_production"]!=true{t.Fatal("existing JPush badge/production contract silently changed")}
 accepted,ignored:=providerOutcome("getui",200,[]byte(`{"code":0,"msg":"","data":{"task":{"cid":"successed_ignore"}}}`),true)
 if accepted || !ignored{t.Fatal("provider known inactive result was called device delivery or unclassified")}
}
