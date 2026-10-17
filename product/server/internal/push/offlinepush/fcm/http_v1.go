package fcm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/oauth2"
)

var ErrHTTPV1Unknown=errors.New("FCM HTTP v1 device result unconfirmed; inspect original attempt")
var ErrHTTPV1Input=errors.New("invalid owned FCM HTTP v1 request")

type HTTPV1DeviceCall struct {
	Binding string
	Token string // Memory only. Never put this structure in a log/receipt.
	TokenSHA256 string
	Title,Content,Extra,Sound string
	Badge int
}
type HTTPV1DeviceResult struct {
	DispatchStarted bool
	TokenSHA256 string
	ProviderReceiptSHA256 string
	OutcomeSHA256 string
	KnownUnregistered bool
}
type HTTPV1Transport struct {
	project,binding,endpoint string
	client *http.Client
	closeOwned func()error
	closed atomic.Bool
	closeOnce sync.Once
	closeErr error
}

// The public owned factory constructs the OAuth source itself. This private
// constructor also permits a synthetic token source/transport in pure tests;
// no caller-supplied retrying authenticated client enters the production path.
func newHTTPV1Transport(project,binding string,source oauth2.TokenSource,base http.RoundTripper,closeOwned func()error)(*HTTPV1Transport,error){
	if project=="" || len(project)>256 || strings.TrimSpace(project)!=project || strings.ContainsAny(project,"/\\\x00\r\n?#%:@ ") || !frozenDigest(binding) || source==nil || base==nil || closeOwned==nil{return nil,ErrHTTPV1Input}
	endpoint:="https://fcm.googleapis.com/v1/projects/"+project+"/messages:send"
	t:=&HTTPV1Transport{project:project,binding:binding,endpoint:endpoint,closeOwned:closeOwned}
	physical:=&httpV1PhysicalTransport{owner:t,base:base}
	t.client=&http.Client{Transport:&oauth2.Transport{Source:source,Base:physical},Timeout:5*time.Second,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}
	return t,nil
}

type httpV1AttemptKey struct{}
type httpV1Attempt struct {
	owner *HTTPV1Transport
	proof string
	before func(context.Context,string)error
	used atomic.Bool
	started atomic.Bool
	mu sync.Mutex
	admissionErr error
}
type httpV1PhysicalTransport struct{owner *HTTPV1Transport;base http.RoundTripper}

// The final Store claim check runs BELOW OAuth refresh and immediately before
// the one physical alert RoundTrip. Each deliverDevice installs its own
// attempt; another legitimate device does not share this one-shot permit.
func (t *httpV1PhysicalTransport) RoundTrip(request *http.Request)(*http.Response,error){
	if t==nil || t.owner==nil || t.base==nil || request==nil{return nil,ErrHTTPV1Input}
	attempt,ok:=request.Context().Value(httpV1AttemptKey{}).(*httpV1Attempt)
	if !ok || attempt==nil || attempt.owner!=t.owner || attempt.before==nil || t.owner.closed.Load() || request.Method!=http.MethodPost || request.URL==nil || request.URL.String()!=t.owner.endpoint || request.GetBody!=nil || !request.Close || !attempt.used.CompareAndSwap(false,true){return nil,ErrHTTPV1Input}
	if err:=attempt.before(request.Context(),attempt.proof);err!=nil{
		attempt.mu.Lock();attempt.admissionErr=err;attempt.mu.Unlock()
		return nil,ErrHTTPV1Input
	}
	if t.owner.closed.Load(){return nil,ErrHTTPV1Input}
	if err:=request.Context().Err();err!=nil{return nil,ErrHTTPV1Input}
	// Once this boundary is crossed even a local network error is unknown.
	// The owned base has no application retry; POST has neither GetBody nor
	// an idempotency header, and keep-alives are disabled by the real factory.
	attempt.started.Store(true)
	return t.base.RoundTrip(request)
}

type httpV1Message struct {
	Token string `json:"token"`
	Data map[string]string `json:"data"`
	Notification struct{Title string `json:"title"`;Body string `json:"body"`} `json:"notification"`
	APNS struct{Payload struct{APS struct{Sound string `json:"sound,omitempty"`;Badge int `json:"badge"`} `json:"aps"`} `json:"payload"`} `json:"apns"`
}

func (t *HTTPV1Transport) request(call HTTPV1DeviceCall)([]byte,string,error){
	if t==nil || t.client==nil || t.closed.Load() || call.Binding!=t.binding || call.Badge<0 || int64(call.Badge)>2147483647 || call.Token=="" || len(call.Token)>4096 || strings.TrimSpace(call.Token)!=call.Token || strings.ContainsAny(call.Token,"\x00\r\n") || frozenHash([]byte(call.Token))!=call.TokenSHA256{return nil,"",ErrHTTPV1Input}
	message:=httpV1Message{Token:call.Token,Data:map[string]string{"ex":call.Extra}}
	message.Notification.Title=call.Title;message.Notification.Body=call.Content
	message.APNS.Payload.APS.Sound=call.Sound;message.APNS.Payload.APS.Badge=call.Badge
	body,err:=json.Marshal(struct{Message httpV1Message `json:"message"`}{message})
	if err!=nil{return nil,"",ErrHTTPV1Input}
	proof,err:=json.Marshal([]any{"fcm-http-v1-device",t.project,t.binding,call.TokenSHA256,frozenHash(body)})
	if err!=nil{return nil,"",ErrHTTPV1Input};return body,frozenHash(proof),nil
}

// This fingerprint, not a raw token/body/project URL, enters the per-device
// durable claim. Each device needs its OWN attempt/unknown state. One claim
// over a batch or one global SendEach permit is not an admissible replacement.
func (t *HTTPV1Transport) RequestFingerprint(call HTTPV1DeviceCall)(string,error){_,proof,err:=t.request(call);return proof,err}

// Only the same-package owned worker may call this private transport method.
// before must consume/revalidate the exact per-device durable dispatch claim
// after all token/badge/policy awaits. This proposal has not yet supplied that
// claim adapter and does not claim a completed worker integration.
func (t *HTTPV1Transport) deliverDevice(parent context.Context,call HTTPV1DeviceCall,before func(context.Context,string)error)(HTTPV1DeviceResult,error){
	result:=HTTPV1DeviceResult{TokenSHA256:call.TokenSHA256}
	if parent==nil || before==nil{return result,ErrHTTPV1Input}
	body,proof,err:=t.request(call);if err!=nil{return result,err}
	current,cancel:=context.WithTimeout(parent,5*time.Second);defer cancel()
	attempt:=&httpV1Attempt{owner:t,proof:proof,before:before}
	current=context.WithValue(current,httpV1AttemptKey{},attempt)
	request,err:=http.NewRequestWithContext(current,http.MethodPost,t.endpoint,bytes.NewReader(body));if err!=nil{return result,ErrHTTPV1Input}
	request.GetBody=nil;request.Close=true
	request.Header.Set("Content-Type","application/json; charset=utf-8")
	if t.closed.Load(){return result,ErrHTTPV1Input}
	if err:=current.Err();err!=nil{return result,err}
	// Exactly one Do for THIS device. The final one-shot/claim gate is inside
	// the physical transport so OAuth awaits cannot bypass final admission.
	response,err:=t.client.Do(request)
	result.DispatchStarted=attempt.started.Load()
	if err!=nil{
		attempt.mu.Lock();admissionErr:=attempt.admissionErr;attempt.mu.Unlock()
		if admissionErr!=nil{return result,admissionErr}
		return result,ErrHTTPV1Unknown
	}
	reply,readErr:=io.ReadAll(io.LimitReader(response.Body,(1<<20)+1));closeErr:=response.Body.Close()
	complete:=readErr==nil && closeErr==nil && len(reply)<=1<<20
	outcome,_:=json.Marshal([]any{response.StatusCode,complete,frozenHash(reply)})
	result.OutcomeSHA256=frozenHash(outcome)
	if !complete{return result,ErrHTTPV1Unknown}
	object,err:=httpV1Object(reply);if err!=nil{return result,ErrHTTPV1Unknown}
	for key:=range object{
		if strings.EqualFold(key,"name") && key!="name" || strings.EqualFold(key,"error") && key!="error"{return result,ErrHTTPV1Unknown}
	}
	if response.StatusCode==http.StatusOK{
		var name string
		if _,bad:=object["error"];bad{return result,ErrHTTPV1Unknown}
		if json.Unmarshal(object["name"],&name)!=nil{return result,ErrHTTPV1Unknown}
		prefix:="projects/"+t.project+"/messages/"
		if !strings.HasPrefix(name,prefix) || len(name)<=len(prefix) || len(name)>4096 || strings.TrimSpace(name)!=name || strings.ContainsAny(name[len(prefix):],"/\x00\r\n "){return result,ErrHTTPV1Unknown}
		result.ProviderReceiptSHA256=frozenHash([]byte(name));return result,nil
	}
	// Only the explicit FCM detail identifies an unregistered device. A 404
	// alone may mean an endpoint/project error and is not a token rejection.
	if response.StatusCode==http.StatusNotFound{
		if _,conflictingName:=object["name"];!conflictingName && httpV1Unregistered(object["error"]){result.KnownUnregistered=true;return result,nil}
	}
	return result,ErrHTTPV1Unknown
}

// Reject duplicate keys case-insensitively in EVERY object, including unknown
// extension fields and nested error/details. Critical fields below are then
// read only by their exact documented spelling; encoding/json's case-folded
// last-wins struct matching is deliberately not an outcome classifier.
func httpV1Object(body []byte)(map[string]json.RawMessage,error){
	decoder:=json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if httpV1StrictValue(decoder,0)!=nil || decoder.Decode(new(any))!=io.EOF{return nil,ErrHTTPV1Unknown}
	var result map[string]json.RawMessage
	if json.Unmarshal(body,&result)!=nil || result==nil{return nil,ErrHTTPV1Unknown}
	return result,nil
}

func httpV1StrictValue(decoder *json.Decoder,depth int)error{
	if depth>64{return ErrHTTPV1Unknown}
	token,err:=decoder.Token();if err!=nil{return ErrHTTPV1Unknown}
	delimiter,isContainer:=token.(json.Delim);if !isContainer{return nil}
	switch delimiter{
	case '{':
		seen:=map[string]bool{}
		for decoder.More(){
			key,err:=decoder.Token();name,ok:=key.(string)
			if err!=nil || !ok || seen[strings.ToLower(name)]{return ErrHTTPV1Unknown}
			seen[strings.ToLower(name)]=true
			if httpV1StrictValue(decoder,depth+1)!=nil{return ErrHTTPV1Unknown}
		}
		end,err:=decoder.Token();if err!=nil || end!=json.Delim('}'){return ErrHTTPV1Unknown}
	case '[':
		for decoder.More(){if httpV1StrictValue(decoder,depth+1)!=nil{return ErrHTTPV1Unknown}}
		end,err:=decoder.Token();if err!=nil || end!=json.Delim(']'){return ErrHTTPV1Unknown}
	default:return ErrHTTPV1Unknown
	}
	return nil
}

func httpV1Unregistered(body []byte)bool{
	errorObject,err:=httpV1Object(body);if err!=nil{return false}
	var code int;var status string;var details []json.RawMessage
	if json.Unmarshal(errorObject["code"],&code)!=nil || code!=404 || json.Unmarshal(errorObject["status"],&status)!=nil || status!="NOT_FOUND" || json.Unmarshal(errorObject["details"],&details)!=nil{return false}
	fcmDetails:=0
	for _,raw:=range details{
		detail,err:=httpV1Object(raw);if err!=nil{return false}
		var kind string;if json.Unmarshal(detail["@type"],&kind)!=nil{return false}
		if kind!="type.googleapis.com/google.firebase.fcm.v1.FcmError"{continue}
		var errorCode string
		if json.Unmarshal(detail["errorCode"],&errorCode)!=nil || errorCode!="UNREGISTERED"{return false}
		fcmDetails++
	}
	return fcmDetails==1
}

func (t *HTTPV1Transport) Close()error{
	if t==nil || t.closeOwned==nil{return ErrHTTPV1Input}
	t.closeOnce.Do(func(){t.closed.Store(true);if err:=t.closeOwned();err!=nil{t.closeErr=errors.New("owned FCM HTTP v1 shutdown unconfirmed")}})
	return t.closeErr
}
