package fcm

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
)

func TestFCMHTTPV1OwnedCredentialsBindActualMaterialAndNeverDiscoverOrAuthenticateAtConstruction(t *testing.T){
	// Generated only inside this pure test. No embedded private credential,
	// ADC lookup, real credential file or supplier HTTP endpoint is used.
	key,err:=rsa.GenerateKey(rand.Reader,2048);if err!=nil{t.Fatal("synthetic RSA generation failed")}
	keyBytes:=pem.EncodeToMemory(&pem.Block{Type:"PRIVATE KEY",Bytes:mustHTTPV1PKCS8(t,key)})
	account:=map[string]string{"type":"service_account","project_id":"synthetic-owned","client_email":"synthetic@synthetic-owned.iam.gserviceaccount.com","private_key_id":"synthetic-key-id","private_key":string(keyBytes),"token_uri":httpV1TokenURL}
	encode:=func()[]byte{b,err:=json.Marshal(account);if err!=nil{t.Fatal(err)};return b}
	document:=encode();configuration:=frozenHash([]byte("synthetic exact configuration"))
	first,err:=NewOwnedHTTPV1Transport(context.Background(),document,frozenHash(document),configuration)
	if err!=nil || !frozenDigest(first.Binding()){t.Fatal("valid owned constructor did not remain offline",err)}
	if err:=first.Close();err!=nil{t.Fatal(err)}
	account["private_key_id"]="synthetic-rotated-key-id";rotated:=encode()
	second,err:=NewOwnedHTTPV1Transport(context.Background(),rotated,frozenHash(rotated),configuration)
	if err!=nil || second.Binding()==first.Binding(){t.Fatal("changed actual credential identity reused old binding",err)}
	if err:=second.Close();err!=nil{t.Fatal(err)}
	for _,test:=range []struct{name,field,value string}{
		{"foreign-token-uri","token_uri","https://foreign.invalid/token"},
		{"metadata-discovery","type","external_account"},
		{"empty-project","project_id",""},
		{"encoded-project","project_id","synthetic%2fother"},
		{"wrong-key","private_key","synthetic-invalid-key"},
	}{t.Run(test.name,func(t *testing.T){
		original:=account[test.field];account[test.field]=test.value;invalid:=encode();account[test.field]=original
		if transport,err:=NewOwnedHTTPV1Transport(context.Background(),invalid,frozenHash(invalid),configuration);err==nil || transport!=nil{t.Fatal("invalid owned identity constructed a client")}
	})}
	duplicate:=[]byte(strings.TrimSuffix(string(rotated),"}")+`,"project_id":"synthetic-owned"}`)
	if transport,err:=NewOwnedHTTPV1Transport(context.Background(),duplicate,frozenHash(duplicate),configuration);err==nil || transport!=nil{t.Fatal("duplicate credential identity was accepted")}
	if transport,err:=NewOwnedHTTPV1Transport(context.Background(),rotated,frozenHash([]byte("different bytes")),configuration);err==nil || transport!=nil{t.Fatal("credential pin mismatch was accepted")}
	cancelled,cancel:=context.WithCancel(context.Background());cancel()
	if transport,err:=NewOwnedHTTPV1Transport(cancelled,rotated,frozenHash(rotated),configuration);err==nil || transport!=nil{t.Fatal("cancelled lifecycle constructed a credential pool")}
}

func mustHTTPV1PKCS8(t *testing.T,key *rsa.PrivateKey)[]byte{
	t.Helper();out,err:=x509.MarshalPKCS8PrivateKey(key);if err!=nil{t.Fatal("synthetic RSA encoding failed")};return out
}
