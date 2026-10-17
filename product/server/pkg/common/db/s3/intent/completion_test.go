package intent

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/mongo"
)

func completionMD5(s string) string { h := md5.Sum([]byte(s)); return hex.EncodeToString(h[:]) }

func TestCompletionScopePartitionAndWireContract(t *testing.T) {
	ns := uuid.NewString()
	base := scopeID(ns, "engine", "actor")
	seen := map[string]bool{base: true}
	for _, args := range [][3]string{{ns + "x", "engine", "actor"}, {ns, "engine2", "actor"}, {ns, "engine", "actor2"}, {"a:b", "c", "d"}, {"a", "b:c", "d"}} {
		id := scopeID(args[0], args[1], args[2])
		if seen[id] {
			t.Fatal("scope partition collision")
		}
		seen[id] = true
	}
	if base != scopeID(ns, "engine", "actor") || strings.Contains(base, ns) || strings.Contains(base, "actor") {
		t.Fatal("scope is unstable or reveals identifiers")
	}
	part := "5d41402abc4b2a76b9719d911017c592" // MD5 of the fixed synthetic text hello.
	wire := completionMD5(part)
	if !validPartContract(wire, []string{part}) || validPartContract(part, []string{part}) || validPartContract(wire, nil) || validPartContract(wire, []string{"not-an-etag"}) {
		t.Fatal("legacy hash contract changed")
	}
	if ValidWireHash(strings.ToUpper(wire)) || ValidWireHash("../"+wire) {
		t.Fatal("noncanonical hash accepted")
	}
	s := Scope{ID: base}
	if s.ObjectPrefix(wire, 12) == s.ObjectPrefix(wire, 13) || strings.Contains(s.ObjectPrefix(wire, 12), "/data/hash/") {
		t.Fatal("size partition or v2 prefix missing")
	}
}

func TestCompletionMissingActorAndInvalidProofFailClosed(t *testing.T) {
	s := &Store{namespace: uuid.NewString(), proofs: &mongo.Collection{}}
	if _, err := s.Scope(context.Background(), "fixture"); err == nil {
		t.Fatal("anonymous scope accepted")
	}
	ctx := context.WithValue(context.Background(), constant.OpUserID, "synthetic-owner")
	if _, err := (*Store)(nil).Scope(ctx, "fixture"); err == nil {
		t.Fatal("missing repository accepted")
	}
	scope, err := s.Scope(ctx, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	parts := []string{completionMD5("synthetic")}
	wire := completionMD5(parts[0])
	id := proofID(scope, wire, 12)
	good := CompletionProof{ID: id, Version: 2, Scope: scope.ID, Owner: "synthetic-owner", Engine: "fixture", Hash: wire, Size: 12, Key: scope.ObjectPrefix(wire, 12) + uuid.NewString(), ETag: parts[0], HashContract: HashContract, PartHashes: parts}
	if err := validateProof(&good, scope, id, good.Owner, good.Engine, wire, 12); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*CompletionProof){func(p *CompletionProof) { p.Owner = "" }, func(p *CompletionProof) { p.Engine = "other" }, func(p *CompletionProof) { p.Scope = "other" }, func(p *CompletionProof) { p.Size++ }, func(p *CompletionProof) { p.Key = "openim/data/hash/" + wire }, func(p *CompletionProof) { p.ETag = "" }, func(p *CompletionProof) { p.PartHashes = nil }, func(p *CompletionProof) { p.HashContract = "raw-md5" }} {
		bad := good
		change(&bad)
		if validateProof(&bad, scope, id, good.Owner, good.Engine, wire, 12) == nil {
			t.Fatal("invalid proof accepted")
		}
	}
}

func TestCompletionFirstWriterVisibilityReadIsBoundedAndFailClosed(t *testing.T) {
	missing := errors.New("synthetic visibility delay")
	isMissing := func(err error) bool { return errors.Is(err, missing) }
	reads := 0
	value := "fixed winning identity"
	got, err := readAfterFirstWriter(context.Background(), func(context.Context) (*string, error) {
		reads++
		if reads == 1 {
			return nil, missing
		}
		return &value, nil
	}, isMissing)
	if err != nil || got == nil || *got != value || reads != 2 {
		t.Fatal("winning identity was not read without rewriting", err)
	}
	denied := errors.New("synthetic read denied")
	reads = 0
	if _, err = readAfterFirstWriter(context.Background(), func(context.Context) (*string, error) { reads++; return nil, denied }, isMissing); !errors.Is(err, denied) || reads != 1 {
		t.Fatal("non-visibility error retried or hidden")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err = readAfterFirstWriter(ctx, func(context.Context) (*string, error) { return nil, missing }, isMissing); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("missing committed identity did not fail closed at caller bound", err)
	}
}
