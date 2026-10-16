package fcm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// Pure hash/token-set helpers are separated from the retired SendAll draft.
// The closure recipe MUST exclude that draft's five files; none of its SDK
// constructors or transport tests form part of this HTTP v1 successor.
func frozenHash(body []byte)string{sum:=sha256.Sum256(body);return hex.EncodeToString(sum[:])}
func frozenDigest(value string)bool{body,err:=hex.DecodeString(value);return err==nil && len(body)==32 && hex.EncodeToString(body)==value}
func TokenSetFingerprint(tokens []string)(string,error){
	if tokens==nil{tokens=[]string{}}
	seen:=map[string]bool{}
	for _,token:=range tokens{
		if token=="" || len(token)>4096 || strings.TrimSpace(token)!=token || strings.ContainsAny(token,"\x00\r\n") || seen[token]{return "",ErrHTTPV1Input};seen[token]=true
	}
	body,err:=json.Marshal(tokens);if err!=nil{return "",ErrHTTPV1Input};return frozenHash(body),nil
}
