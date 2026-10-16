package fcm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

type httpV1Fixture struct {
	calls int
	closeCalls int
	beforeCalls int
	respond func(httpV1Message)(int,string,error)
	messages []httpV1Message
	requests []*http.Request
}
func (f *httpV1Fixture) RoundTrip(request *http.Request)(*http.Response,error){
	f.calls++
	if request.Method!=http.MethodPost || request.URL.String()!="https://fcm.googleapis.com/v1/projects/synthetic-owned/messages:send" || request.GetBody!=nil || !request.Close{return nil,errors.New("synthetic HTTP v1 wire boundary failed")}
	deadline,ok:=request.Context().Deadline();if !ok || time.Until(deadline)>5*time.Second{return nil,errors.New("synthetic missing device deadline")}
	var input struct{Message httpV1Message `json:"message"`}
	if json.NewDecoder(request.Body).Decode(&input)!=nil{return nil,errors.New("synthetic invalid request JSON")}
	f.messages=append(f.messages,input.Message)
	f.requests=append(f.requests,request)
	status,body,err:=f.respond(input.Message);if err!=nil{return nil,err}
	return &http.Response{StatusCode:status,Header:http.Header{"Content-Type":[]string{"application/json"}},Body:io.NopCloser(strings.NewReader(body)),ContentLength:int64(len(body)),Request:request},nil
}
func (f *httpV1Fixture) close()error{f.closeCalls++;return nil}
func (f *httpV1Fixture) before(ctx context.Context,proof string)error{
	f.beforeCalls++
	if ctx.Err()!=nil || !frozenDigest(proof){return ErrHTTPV1Input};return nil
}
func httpV1TransportFixture(t *testing.T,respond func(httpV1Message)(int,string,error))(*HTTPV1Transport,*httpV1Fixture){
	t.Helper();fixture:=&httpV1Fixture{respond:respond}
	transport,err:=newHTTPV1Transport("synthetic-owned",frozenHash([]byte("synthetic owned HTTP v1 identity")),oauth2.StaticTokenSource(&oauth2.Token{AccessToken:"synthetic-memory-only-access"}),fixture,fixture.close)
	if err!=nil{t.Fatal(err)}
	t.Cleanup(func(){if err:=transport.Close();err!=nil{t.Error("owned synthetic HTTP v1 pool close failed")}})
	return transport,fixture
}
func httpV1Call(t *HTTPV1Transport,token string)HTTPV1DeviceCall{return HTTPV1DeviceCall{Binding:t.binding,Token:token,TokenSHA256:frozenHash([]byte(token)),Title:"synthetic title",Content:"synthetic content",Extra:"synthetic ex",Sound:"default",Badge:7}}

func TestFCMHTTPV1EachDeviceGetsIndependentPOSTAndFrozenWireValues(t *testing.T){
	transport,fixture:=httpV1TransportFixture(t,func(httpV1Message)(int,string,error){return 200,`{"name":"projects/synthetic-owned/messages/synthetic-receipt"}`,nil})
	for _,token:=range []string{"synthetic-device-a","synthetic-device-b"}{
		call:=httpV1Call(transport,token)
		result,err:=transport.deliverDevice(context.Background(),call,fixture.before)
		if err!=nil || !result.DispatchStarted || result.TokenSHA256!=call.TokenSHA256 || !frozenDigest(result.ProviderReceiptSHA256) || result.KnownUnregistered{t.Fatal("matching HTTP v1 response did not bind this device",err)}
	}
	if fixture.calls!=2 || fixture.beforeCalls!=2 || len(fixture.messages)!=2{t.Fatal("a global batch permit blocked the second legitimate device")}
	for _,message:=range fixture.messages{if message.APNS.Payload.APS.Badge!=7 || message.Notification.Title!="synthetic title" || message.Notification.Body!="synthetic content" || message.Data["ex"]!="synthetic ex"{t.Fatal("HTTP v1 changed legacy notification/data/APNS fields")}}
}

func TestFCMHTTPV1UnknownDeviceDoesNotRetryOrConsumeAnotherDevicesAttempt(t *testing.T){
	transport,fixture:=httpV1TransportFixture(t,func(message httpV1Message)(int,string,error){
		if message.Token=="synthetic-unknown-device"{return 0,"",errors.New("synthetic lost device ACK")}
		return 200,`{"name":"projects/synthetic-owned/messages/synthetic-known-receipt"}`,nil
	})
	first,err:=transport.deliverDevice(context.Background(),httpV1Call(transport,"synthetic-unknown-device"),fixture.before)
	if !errors.Is(err,ErrHTTPV1Unknown) || !first.DispatchStarted || first.ProviderReceiptSHA256!="" || fixture.calls!=1{t.Fatal("unknown HTTP v1 device ACK was retried or called success",err)}
	second,err:=transport.deliverDevice(context.Background(),httpV1Call(transport,"synthetic-known-device"),fixture.before)
	if err!=nil || !second.DispatchStarted || !frozenDigest(second.ProviderReceiptSHA256) || fixture.calls!=2{t.Fatal("independent second device was wrongly blocked",err)}
}

func TestFCMHTTPV1DeviceProofAndFinalAdmissionPrecedePhysicalPOST(t *testing.T){
	transport,fixture:=httpV1TransportFixture(t,func(httpV1Message)(int,string,error){return 200,`{"name":"projects/synthetic-owned/messages/synthetic"}`,nil})
	call:=httpV1Call(transport,"synthetic-device")
	proof,err:=transport.RequestFingerprint(call);if err!=nil{t.Fatal(err)}
	changed:=call;changed.Badge++
	next,err:=transport.RequestFingerprint(changed);if err!=nil || next==proof{t.Fatal("changed badge did not change exact device request proof",err)}
	rejected:=errors.New("synthetic privacy/owner claim revoked")
	result,err:=transport.deliverDevice(context.Background(),call,func(context.Context,string)error{return rejected})
	if !errors.Is(err,rejected) || result.DispatchStarted || fixture.calls!=0{t.Fatal("final refusal reached vendor transport",err)}
	wrong:=call;wrong.TokenSHA256=frozenHash([]byte("foreign token"))
	if _,err:=transport.deliverDevice(context.Background(),wrong,fixture.before);!errors.Is(err,ErrHTTPV1Input) || fixture.calls!=0{t.Fatal("wrong device binding reached vendor")}
}

func TestFCMHTTPV1OnlyExactNameOrExplicitUnregisteredDetailSettlesDevice(t *testing.T){
	for _,test:=range []struct{name string;status int;body string;knownReject bool}{
		{"empty",200,`{}`,false},
		{"null",200,`null`,false},
		{"foreign-project",200,`{"name":"projects/foreign/messages/id"}`,false},
		{"duplicate-name",200,`{"name":"projects/synthetic-owned/messages/id","name":"projects/synthetic-owned/messages/other"}`,false},
		{"trailing-document",200,`{"name":"projects/synthetic-owned/messages/id"} {}`,false},
		{"server-unknown",503,`{"error":{"code":503,"status":"UNAVAILABLE"}}`,false},
		{"auth-error-not-device-unregistered",401,`{"error":{"code":401,"status":"UNAUTHENTICATED"}}`,false},
		{"bare-notfound-not-device-proof",404,`{"error":{"code":404,"status":"NOT_FOUND"}}`,false},
		{"unregistered",404,`{"error":{"code":404,"status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`,true},
	}{t.Run(test.name,func(t *testing.T){
		transport,fixture:=httpV1TransportFixture(t,func(httpV1Message)(int,string,error){return test.status,test.body,nil})
		result,err:=transport.deliverDevice(context.Background(),httpV1Call(transport,"synthetic-device"),fixture.before)
		if result.KnownUnregistered!=test.knownReject || (err==nil)!=test.knownReject || result.ProviderReceiptSHA256!="" || fixture.calls!=1 || !result.DispatchStarted{t.Fatal("ambiguous provider response became a device ACK",err)}
	})}
}

func TestFCMHTTPV1ClosedPoolRejectsNewAttemptsWithoutReopening(t *testing.T){
	transport,fixture:=httpV1TransportFixture(t,func(httpV1Message)(int,string,error){return 200,`{"name":"projects/synthetic-owned/messages/id"}`,nil})
	if err:=transport.Close();err!=nil{t.Fatal(err)}
	if err:=transport.Close();err!=nil{t.Fatal(err)}
	result,err:=transport.deliverDevice(context.Background(),httpV1Call(transport,"synthetic-device"),fixture.before)
	if !errors.Is(err,ErrHTTPV1Input) || result.DispatchStarted || fixture.calls!=0 || fixture.closeCalls!=1{t.Fatal("closed managed transport was reused or reopened",err)}
}

func TestFCMHTTPV1NestedOrCaseAmbiguousResponsesRemainUnknownWithoutRetry(t *testing.T){
	for _,test:=range []struct{name string;status int;body string}{
		{"success-uppercase-error",200,`{"name":"projects/synthetic-owned/messages/id","Error":{"code":500}}`},
		{"success-casefold-name",200,`{"Name":"projects/synthetic-owned/messages/id"}`},
		{"duplicate-nested-code",404,`{"error":{"code":403,"code":404,"status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`},
		{"casefold-nested-code",404,`{"error":{"Code":403,"code":404,"status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`},
		{"duplicate-nested-status",404,`{"error":{"code":404,"status":"PERMISSION_DENIED","status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`},
		{"casefold-only-status",404,`{"error":{"code":404,"Status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`},
		{"duplicate-detail-type",404,`{"error":{"code":404,"status":"NOT_FOUND","details":[{"@type":"other","@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`},
		{"duplicate-detail-error-code",404,`{"error":{"code":404,"status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"INVALID_ARGUMENT","errorCode":"UNREGISTERED"}]}}`},
		{"casefold-detail-error-code",404,`{"error":{"code":404,"status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","ErrorCode":"INVALID_ARGUMENT","errorCode":"UNREGISTERED"}]}}`},
		{"casefold-only-detail-error-code",404,`{"error":{"code":404,"status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","ErrorCode":"UNREGISTERED"}]}}`},
		{"conflicting-fcm-details",404,`{"error":{"code":404,"status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"},{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"INVALID_ARGUMENT"}]}}`},
		{"unknown-extension-nested-duplicate",200,`{"name":"projects/synthetic-owned/messages/id","future":{"inner":{"x":1,"X":2}}}`},
	}{t.Run(test.name,func(t *testing.T){
		transport,fixture:=httpV1TransportFixture(t,func(httpV1Message)(int,string,error){return test.status,test.body,nil})
		result,err:=transport.deliverDevice(context.Background(),httpV1Call(transport,"synthetic-device"),fixture.before)
		if !errors.Is(err,ErrHTTPV1Unknown) || result.KnownUnregistered || result.ProviderReceiptSHA256!="" || !result.DispatchStarted || fixture.calls!=1 || fixture.beforeCalls!=1{t.Fatal("ambiguous response changed device state or retried",err)}
	})}
	// An ordinary unambiguous extension remains compatible; strict duplicate
	// rejection does not remove the valid name / UNREGISTERED positive cases.
	transport,fixture:=httpV1TransportFixture(t,func(httpV1Message)(int,string,error){return 200,`{"name":"projects/synthetic-owned/messages/id","future":{"number":1,"nested":[{"flag":true}]}}`,nil})
	if result,err:=transport.deliverDevice(context.Background(),httpV1Call(transport,"synthetic-device"),fixture.before);err!=nil || !frozenDigest(result.ProviderReceiptSHA256) || fixture.calls!=1{t.Fatal("unambiguous response extension was rejected",err)}
}

type httpV1TokenSource struct{token func()(*oauth2.Token,error)}
func (s httpV1TokenSource) Token()(*oauth2.Token,error){return s.token()}

func TestFCMHTTPV1FinalPhysicalClaimRunsAfterOAuthAndCannotBeConsumedTwice(t *testing.T){
	fixture:=&httpV1Fixture{respond:func(httpV1Message)(int,string,error){return 200,`{"name":"projects/synthetic-owned/messages/id"}`,nil}}
	refreshed:=false
	source:=httpV1TokenSource{token:func()(*oauth2.Token,error){refreshed=true;return &oauth2.Token{AccessToken:"synthetic-memory-only"},nil}}
	transport,err:=newHTTPV1Transport("synthetic-owned",frozenHash([]byte("synthetic identity")),source,fixture,fixture.close);if err!=nil{t.Fatal(err)}
	t.Cleanup(func(){if err:=transport.Close();err!=nil{t.Error("owned synthetic close failed")}})
	refused:=errors.New("synthetic post-OAuth revocation")
	before:=0
	result,err:=transport.deliverDevice(context.Background(),httpV1Call(transport,"synthetic-device-a"),func(context.Context,string)error{
		before++;if !refreshed{t.Fatal("physical admission preceded OAuth await")};return refused
	})
	if !errors.Is(err,refused) || result.DispatchStarted || fixture.calls!=0 || before!=1{t.Fatal("revoked post-OAuth request crossed physical boundary",err)}
	result,err=transport.deliverDevice(context.Background(),httpV1Call(transport,"synthetic-device-b"),fixture.before)
	if err!=nil || !result.DispatchStarted || fixture.calls!=1 || len(fixture.requests)!=1{t.Fatal("another device could not obtain its independent attempt",err)}
	physical:=transport.client.Transport.(*oauth2.Transport).Base
	if _,err:=physical.RoundTrip(fixture.requests[0]);err==nil || fixture.calls!=1 || fixture.beforeCalls!=1{t.Fatal("same physical permit was consumed twice")}
}
