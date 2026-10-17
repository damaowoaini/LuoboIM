package fcm

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const httpV1Scope="https://www.googleapis.com/auth/firebase.messaging"
const httpV1TokenURL="https://oauth2.googleapis.com/token"

// document must come from the runtime's separately owned private credential
// input, with its file ownership/mode and expected bytes checked by the
// still-required startup loader. No ADC/environment/file lookup happens here.
// Do not log document, the token source, private-key parse errors, or requests.
// Construction parses/validates only and performs no HTTP request.
func NewOwnedHTTPV1Transport(parent context.Context,document []byte,expectedDocumentSHA,configurationSHA string)(*HTTPV1Transport,error){
	if parent==nil || parent.Err()!=nil || len(document)==0 || len(document)>128<<10 || !frozenDigest(expectedDocumentSHA) || frozenHash(document)!=expectedDocumentSHA || !frozenDigest(configurationSHA){return nil,ErrHTTPV1Input}
	object,err:=httpV1Object(document);if err!=nil{return nil,ErrHTTPV1Input}
	var account struct {
		Type string `json:"type"`
		Project string `json:"project_id"`
		Email string `json:"client_email"`
		KeyID string `json:"private_key_id"`
		PrivateKey string `json:"private_key"`
		TokenURI string `json:"token_uri"`
	}
	// Explicit fields are required; duplicate root keys were rejected above.
	if len(object)==0 || json.Unmarshal(document,&account)!=nil || account.Type!="service_account" || account.TokenURI!=httpV1TokenURL || account.Project=="" || len(account.Project)>256 || strings.TrimSpace(account.Project)!=account.Project || strings.ContainsAny(account.Project,"/\\\x00\r\n?#%:@ ") || account.Email=="" || strings.TrimSpace(account.Email)!=account.Email || !strings.HasSuffix(account.Email,".iam.gserviceaccount.com") || strings.ContainsAny(account.Email,"/\\\x00\r\n ") || account.KeyID=="" || len(account.KeyID)>256 || strings.ContainsAny(account.KeyID,"\x00\r\n "){return nil,ErrHTTPV1Input}
	if !httpV1RSAKey(account.PrivateKey){return nil,ErrHTTPV1Input}
	configuration,err:=google.JWTConfigFromJSON(document,httpV1Scope)
	if err!=nil || configuration.TokenURL!=httpV1TokenURL || configuration.Email!=account.Email || configuration.PrivateKeyID!=account.KeyID || len(configuration.Scopes)!=1 || configuration.Scopes[0]!=httpV1Scope{return nil,ErrHTTPV1Input}
	identity,err:=json.Marshal([]string{"ordinary-fcm-http-v1-identity-v1",configurationSHA,expectedDocumentSHA,account.Project,account.Email,account.KeyID,httpV1Scope,httpV1TokenURL})
	if err!=nil{return nil,ErrHTTPV1Input}
	owned,cancel:=context.WithCancel(parent)
	alertBase:=httpV1BaseTransport()
	authBase:=httpV1BaseTransport()
	authClient:=&http.Client{Transport:authBase,Timeout:5*time.Second,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}
	owned=context.WithValue(owned,oauth2.HTTPClient,authClient)
	source:=oauth2.ReuseTokenSource(nil,configuration.TokenSource(owned))
	transport,err:=newHTTPV1Transport(account.Project,frozenHash(identity),source,alertBase,func()error{
		cancel();alertBase.CloseIdleConnections();authBase.CloseIdleConnections();return nil
	})
	if err!=nil{cancel();alertBase.CloseIdleConnections();authBase.CloseIdleConnections();return nil,ErrHTTPV1Input}
	return transport,nil
}

func httpV1BaseTransport()*http.Transport{
	return &http.Transport{
		Proxy:http.ProxyFromEnvironment,
		DialContext:(&net.Dialer{Timeout:5*time.Second,KeepAlive:-1}).DialContext,
		TLSClientConfig:&tls.Config{MinVersion:tls.VersionTLS12},
		TLSHandshakeTimeout:5*time.Second,
		ResponseHeaderTimeout:5*time.Second,
		ExpectContinueTimeout:time.Second,
		DisableKeepAlives:true,
		ForceAttemptHTTP2:false,
	}
}

func httpV1RSAKey(value string)bool{
	if value=="" || len(value)>32<<10{return false}
	block,rest:=pem.Decode([]byte(value));if block==nil || len(strings.TrimSpace(string(rest)))!=0 || len(block.Headers)!=0{return false}
	var key *rsa.PrivateKey
	switch block.Type{
	case "PRIVATE KEY":
		parsed,err:=x509.ParsePKCS8PrivateKey(block.Bytes);if err!=nil{return false}
		var ok bool;key,ok=parsed.(*rsa.PrivateKey);if !ok{return false}
	case "RSA PRIVATE KEY":
		var err error;key,err=x509.ParsePKCS1PrivateKey(block.Bytes);if err!=nil{return false}
	default:return false
	}
	return key!=nil && key.N.BitLen()>=2048 && key.Validate()==nil
}

// Binding is a non-secret identity digest, never the credentials themselves.
// The owned policy must freeze THIS digest before accepting the first source;
// the current path-only configuration binding cannot be substituted for it.
func (t *HTTPV1Transport) Binding()string{if t==nil{return ""};return t.binding}
