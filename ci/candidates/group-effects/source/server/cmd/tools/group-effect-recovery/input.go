package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/x/mongo/driver/connstring"
)

var errInput = errors.New("invalid private operator input")

type operatorInput struct {
	URI        string `json:"uri"`
	Database   string `json:"database"`
	ReplicaSet string `json:"replicaSet"`
}

func sha(v string) bool {
	b, e := hex.DecodeString(v)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == v
}
func id(v string) bool { u, e := uuid.Parse(v); return e == nil && u != uuid.Nil && u.String() == v }
func plain(v string) bool {
	return v != "" && len(v) <= 128 && !strings.ContainsAny(v, "/\\\x00\r\n\t $")
}
func validDatabase(v string) bool {
	return plain(v) && v != "admin" && v != "config" && v != "local" && !strings.ContainsAny(v, ".\"")
}
func owned(info os.FileInfo, mode os.FileMode) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint32(os.Geteuid()) == st.Uid && info.Mode().Perm() == mode
}
func privateParent(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	canonical, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil || canonical != filepath.Dir(path) {
		return false
	}
	info, err := os.Lstat(filepath.Dir(path))
	return err == nil && info.IsDir() && owned(info, 0o700)
}
func loadInput(path, db, replica, endpoints string) (operatorInput, error) {
	var input operatorInput
	if !privateParent(path) {
		return input, errInput
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || !owned(before, 0o600) || before.Size() > 16384 {
		return input, errInput
	}
	f, err := os.Open(path)
	if err != nil {
		return input, errInput
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) || !owned(after, 0o600) {
		_ = f.Close()
		return input, errInput
	}
	body, readErr := io.ReadAll(io.LimitReader(f, 16385))
	final, statErr := f.Stat()
	closeErr := f.Close()
	if readErr != nil || statErr != nil || closeErr != nil || len(body) > 16384 || !os.SameFile(before, final) || before.Size() != final.Size() || !before.ModTime().Equal(final.ModTime()) || !owned(final, 0o600) {
		return input, errInput
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || input.Database != db || input.ReplicaSet != replica || !validDatabase(db) || !plain(replica) || !sha(endpoints) {
		return operatorInput{}, errInput
	}
	cs, err := connstring.ParseAndValidate(input.URI)
	if err != nil || cs.Scheme != "mongodb" || len(cs.Hosts) == 0 || len(cs.Hosts) > 8 || cs.ReplicaSet != replica || cs.Database != "" && cs.Database != db || cs.DirectConnection || cs.Connect == connstring.SingleConnect || cs.LoadBalanced || cs.ReadPreference != "" && cs.ReadPreference != "primary" || cs.SSLInsecure || cs.SSLInsecureSet {
		return operatorInput{}, errInput
	}
	hosts := append([]string(nil), cs.Hosts...)
	sort.Strings(hosts)
	sum := sha256.Sum256([]byte(strings.Join(hosts, "\x00")))
	if hex.EncodeToString(sum[:]) != endpoints {
		return operatorInput{}, errInput
	}
	return input, nil
}
func openJournal(path string) (*os.File, error) {
	if !privateParent(path) {
		return nil, errInput
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, errInput
	}
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		_ = f.Close()
		return nil, errInput
	}
	err = parent.Sync()
	closeErr := parent.Close()
	if err != nil || closeErr != nil {
		_ = f.Close()
		return nil, errInput
	}
	return f, nil
}
func appendJournal(f *os.File, value any) error {
	if json.NewEncoder(f).Encode(value) != nil {
		return errInput
	}
	return f.Sync()
}
