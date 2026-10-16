package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"go.mongodb.org/mongo-driver/x/mongo/driver/connstring"
	"github.com/google/uuid"
)

var errInput = errors.New("invalid_operator_input")

const purpose = "luobo-ordinary-alias-receipt-operator/v1"

type operatorInput struct {
	Version    int    `json:"version"`
	Purpose    string `json:"purpose"`
	URI        string `json:"uri"`
	Database   string `json:"database"`
	ReplicaSet string `json:"replicaSet"`
	Epoch string `json:"epoch"`
	Activation string `json:"activationSHA256"`
}

func validName(value string) bool {
	if value == "" || len(value) > 63 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func validDatabase(value string) bool {
	return validName(value) && strings.ToLower(value) != "admin" && strings.ToLower(value) != "config" && strings.ToLower(value) != "local"
}

func privateParent(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errInput
	}
	resolved, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil || resolved != filepath.Dir(path) {
		return errInput
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return errInput
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return errInput
	}
	return nil
}

func readPrivateDoc(path, expectedSHA string)([]byte,error){
 if privateParent(path)!=nil || !operatorSHA(expectedSHA){return nil,errInput}
 parent,err:=os.Lstat(filepath.Dir(path));if err!=nil{return nil,errInput}
 before,err:=os.Lstat(path)
 if err!=nil || !before.Mode().IsRegular() || before.Mode().Perm()!=0600 || before.Size()<1 || before.Size()>65536{return nil,errInput}
 owner,ok:=before.Sys().(*syscall.Stat_t);if !ok || owner.Uid!=uint32(os.Geteuid()) || owner.Nlink!=1{return nil,errInput}
 file,err:=os.OpenFile(path,os.O_RDONLY|syscall.O_NOFOLLOW,0);if err!=nil{return nil,errInput}
 opened,statErr:=file.Stat()
 if statErr!=nil || !os.SameFile(before,opened) || before.Mode()!=opened.Mode(){file.Close();return nil,errInput}
 raw,readErr:=io.ReadAll(io.LimitReader(file,65537))
 after,statErr:=file.Stat();closeErr:=file.Close()
 current,pathErr:=os.Lstat(path);currentParent,parentErr:=os.Lstat(filepath.Dir(path))
 if readErr!=nil || statErr!=nil || closeErr!=nil || pathErr!=nil || parentErr!=nil || len(raw)!=int(before.Size()) || len(raw)>65536 || !os.SameFile(before,after) || !os.SameFile(before,current) || !os.SameFile(parent,currentParent) || before.Mode()!=after.Mode() || before.Mode()!=current.Mode() || !before.ModTime().Equal(after.ModTime()) || !before.ModTime().Equal(current.ModTime()) || privateParent(path)!=nil || operatorHash(raw)!=expectedSHA {
  for i:=range raw{raw[i]=0};return nil,errInput
 }
 return raw,nil
}

func loadInput(path, expected, expectedSHA string)(operatorInput,error){
 raw,err:=readPrivateDoc(path,expectedSHA);if err!=nil{return operatorInput{},err}
 defer func(){for i:=range raw{raw[i]=0}}()
 return decodeInput(raw,expected)
}

func decodeInput(raw []byte, expected string) (operatorInput, error) {
	var value operatorInput
	// Encoding/json accepts duplicate and case-folded field names. This operator
	// input does not: an exact purpose/binding must have only one interpretation.
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return value, errInput
	}
	allowed := map[string]bool{"version": true, "purpose": true, "uri": true, "database": true, "replicaSet": true, "epoch":true,"activationSHA256":true}
	seen := map[string]bool{}
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		folded := strings.ToLower(key)
		if err != nil || !ok || !allowed[key] || seen[folded] {
			return value, errInput
		}
		seen[folded] = true
		var discard json.RawMessage
		if d.Decode(&discard) != nil {
			return value, errInput
		}
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return value, errInput
	}
	var extra any
	if d.Decode(&extra) != io.EOF || json.Unmarshal(raw, &value) != nil {
		return value, errInput
	}
	if !canonicalUUID(value.Epoch) || !operatorSHA(value.Activation) || value.Version != 1 || value.Purpose != purpose || !validDatabase(expected) || value.Database != expected || !validDatabase(value.Database) || !validName(value.ReplicaSet) {
		return value, errInput
	}
	u, err := url.Parse(value.URI)
	if err != nil || u.Scheme != "mongodb" || u.Fragment != "" || u.Opaque != "" {
		return value, errInput
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return value, errInput
	}
	keys := map[string]bool{}
	for key, values := range query {
		folded := strings.ToLower(key)
		if keys[folded] || len(values) != 1 {
			return value, errInput
		}
		keys[folded] = true
		if (folded == "tlsinsecure" || folded == "tlsallowinvalidcertificates" || folded == "tlsallowinvalidhostnames") && values[0] != "false" {
			return value, errInput
		}
	}
	// Explicit seed-list URIs only. Rejecting SRV here keeps input validation pure
	// (the pinned driver's SRV parser can perform DNS before a connection exists).
	parsed, err := connstring.ParseAndValidate(value.URI)
	if err != nil || len(parsed.Hosts) == 0 || parsed.ReplicaSet != value.ReplicaSet || parsed.DirectConnection || parsed.ConnectSet && parsed.Connect == connstring.SingleConnect || parsed.LoadBalanced || parsed.SSLInsecure || parsed.ReadPreference != "" && parsed.ReadPreference != "primary" || parsed.Database != "" && parsed.Database != value.Database {
		return value, errInput
	}
	// External authenticators may resolve process/cloud credentials. This tool
	// permits only no-auth or explicit URI username/password SCRAM credentials.
	if parsed.AuthMechanismProperties != nil || parsed.AuthMechanism != "" && parsed.AuthMechanism != "SCRAM-SHA-1" && parsed.AuthMechanism != "SCRAM-SHA-256" {
		return value, errInput
	}
	if u.User != nil {
		password, supplied := u.User.Password()
		if u.User.Username() == "" || !supplied || password == "" {
			return value, errInput
		}
	} else if parsed.AuthMechanism != "" {
		return value, errInput
	}
	return value, nil
}

func bindingDigest(input operatorInput) string {
	// No credential/URI digest: a weak operator password must not gain a public
	// offline guessing oracle. Only noncredential database+replica-set binding.
	sum := sha256.Sum256([]byte(input.Database + "\x00" + input.ReplicaSet))
	return hex.EncodeToString(sum[:])
}

func operatorHash(raw []byte)string{sum:=sha256.Sum256(raw);return hex.EncodeToString(sum[:])}
func operatorSHA(value string)bool{raw,err:=hex.DecodeString(value);return err==nil && len(raw)==32 && hex.EncodeToString(raw)==value}
func canonicalUUID(value string)bool{id,err:=uuid.Parse(value);return err==nil && id!=uuid.Nil && id.String()==value}
