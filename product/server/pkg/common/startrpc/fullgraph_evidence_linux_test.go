//go:build linux

package startrpc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

func init() { fullgraphRequiredTokenRunner = assertActualRequiredTokenFullgraph }

// These are actual one-shot operator output projections, never startup input
// or a permit. The cloud controller must first run all four fresh real planes and
// bind their exact source, exit/resource receipts and these private body SHAs.
// A missing graph/result is a prerequisite failure; no synthetic fallback.
type actualGraphRole struct {
	Role             string   `json:"role"`
	PID              int      `json:"pid"`
	StartToken       string   `json:"startToken"`
	SourceSHA256     string   `json:"sourceSHA256"`
	OwnerNonceSHA256 string   `json:"ownerNonceSHA256"`
	ActualAbsent     bool     `json:"actualAbsent"`
	CanonicalDrain   bool     `json:"canonicalDrain"`
	JoinedAndClosed  []string `json:"joinedAndClosed"`
}
type actualReaderEndpoint struct {
	Role                                                                          string
	PID                                                                           int
	StartToken, Endpoint, ConfigurationSHA256, ListenerInode                      string
	ActualTCPConnected, OriginalListenerRetained, ActualUnimplementedAfterRelease bool
}
type actualCancelledRole struct {
	Role                                                                                               string
	PID                                                                                                int
	StartToken, SourceSHA256, BinarySHA256, OwnerNonceSHA256                                           string
	ActualAbsent, OriginalWaitObserved, AnnouncementAbsent, CanonicalDrainAbsent, PrivateReleaseAbsent bool
}
type actualCancellation struct {
	PausedPhase                                                                                              string
	ActualPreparedRoles                                                                                      int
	Roles                                                                                                    []actualCancelledRole
	KnownAdmissionAbsent, ReaderDiscoveryAbsentBefore, ReaderDiscoveryAbsentAfter, OriginalStopReturnedError bool
}
type actualGraphPhase struct {
	Phase                           string                               `json:"phase"`
	Readers                         []ownedcontrol.ObservedHistoryReader `json:"readers"`
	ActualPreparedRoles             int                                  `json:"actualPreparedRoles"`
	DataReadySHA256                 string                               `json:"dataReadySHA256"`
	KnownAdmissionSHA256            string                               `json:"knownAdmissionSHA256"`
	BothActualConnectionsNotServing bool                                 `json:"bothActualConnectionsNotServing"`
	Endpoints                       []actualReaderEndpoint               `json:"endpoints"`
}
type actualGraphEvidence struct {
	Version, Scenario, SourceSHA256, KnownAdmissionSHA256, HeaderSHA256, SignedInventorySHA256, ImportManifestSHA256, DataReadySHA256                  string
	Accepted                                                                                                                                           bool
	ActualStartedRoles                                                                                                                                 int
	Roles                                                                                                                                              []actualGraphRole
	ReaderPhases                                                                                                                                       []actualGraphPhase
	ReaderServed                                                                                                                                       []actualReaderEndpoint
	Cancellation                                                                                                                                       *actualCancellation
	RawPushRejected, ActualUserReadExact, ActualTokenRPCSucceeded, RedisKeyPresentBefore, RedisKeyAbsentAfter, CanonicalShutdown, UnknownOwnerRetained bool
	ActualTokenRPCDispatched                                                                                                                           int
	MongoMessageCount                                                                                                                                  int64
	ACKLoss                                                                                                                                            struct {
		ExactDeleteRequests                                              int
		ActualUpstreamIntegerOne, ActualReplyDiscarded, TransportFailure bool
	}
}

func readActualFullgraphEvidence(t *testing.T, scenario string) actualGraphEvidence {
	t.Helper()
	var v actualGraphEvidence
	suffix := strings.ToUpper(strings.ReplaceAll(scenario, "-", "_"))
	path, expected := os.Getenv("LUOBO_FULLGRAPH_"+suffix+"_RESULT"), os.Getenv("LUOBO_FULLGRAPH_"+suffix+"_SHA256")
	source := os.Getenv("LUOBO_FULLGRAPH_SOURCE_SHA256")
	if path == "" || !ownedHash(expected) || !ownedHash(source) {
		t.Fatal("mandatory source-bound real fullgraph result missing")
	}
	raw, e := namespaceinventory.ReadPrivateDocument(path, 1<<20)
	sum := sha256.Sum256(raw)
	if e != nil || hex.EncodeToString(sum[:]) != expected || namespaceinventory.DecodeStrict(raw, new(map[string]json.RawMessage)) != nil || json.Unmarshal(raw, &v) != nil || v.Version != "owned-fullgraph-token-v2-result-v1" || v.Scenario != scenario || v.SourceSHA256 != source || !v.Accepted || !ownedHash(v.HeaderSHA256) || !ownedHash(v.SignedInventorySHA256) || !ownedHash(v.ImportManifestSHA256) {
		t.Fatal("actual fullgraph source/result custody or outcome differs")
	}
	if strings.HasPrefix(scenario, "reader-") {
		if v.ActualStartedRoles != 0 || len(v.Roles) != 0 || v.KnownAdmissionSHA256 != "" || v.Cancellation == nil || len(v.ReaderServed) != 0 {
			t.Fatal("cancelled startup became a runtime")
		}
		return v
	}
	if v.ActualStartedRoles != 14 || !ownedHash(v.KnownAdmissionSHA256) || !ownedHash(v.DataReadySHA256) || len(v.Roles) != 14 || v.Cancellation != nil {
		t.Fatal("actual full graph incomplete")
	}
	seen := map[string]bool{}
	for _, r := range v.Roles {
		if seen[r.Role] || r.PID <= 1 || r.StartToken == "" || r.SourceSHA256 != source || !ownedHash(r.OwnerNonceSHA256) || !r.ActualAbsent {
			t.Fatal("original role owner lost or duplicated")
		}
		seen[r.Role] = true
	}
	for _, role := range ownedcontrol.NamespaceRuntimeRoles() {
		if !seen[role] {
			t.Fatal("actual required role absent", role)
		}
	}
	if !v.RawPushRejected || !v.ActualUserReadExact || v.ActualTokenRPCDispatched != 1 || !v.RedisKeyPresentBefore || !v.RedisKeyAbsentAfter || v.MongoMessageCount != 0 {
		t.Fatal("actual required token/identity/source observations incomplete")
	}
	return v
}
func (v actualGraphEvidence) role(t *testing.T, role string) actualGraphRole {
	t.Helper()
	for _, r := range v.Roles {
		if r.Role == role {
			return r
		}
	}
	t.Fatal("actual owner absent", role)
	return actualGraphRole{}
}
func (v actualGraphEvidence) phase(t *testing.T, name string) actualGraphPhase {
	t.Helper()
	for _, p := range v.ReaderPhases {
		if p.Phase == name {
			for _, r := range p.Readers {
				if r.Registration.HeaderSHA256 != v.HeaderSHA256 || r.Registration.InventorySHA256 != v.SignedInventorySHA256 || r.Registration.ManifestSHA256 != v.ImportManifestSHA256 || r.SourceSHA256 != v.SourceSHA256 {
					t.Fatal("reader borrowed another source/header/inventory")
				}
			}
			return p
		}
	}
	t.Fatal("original synchronous phase absent", name)
	return actualGraphPhase{}
}
func assertActualRequiredTokenFullgraph(t *testing.T) {
	t.Helper()
	ack, unknown := readActualFullgraphEvidence(t, "ack"), readActualFullgraphEvidence(t, "redis-unknown")
	want := []string{"rpc-handlers", "rpc-serve", "business-workers", "redis", "discovery"}
	if !ack.ActualTokenRPCSucceeded || !ack.CanonicalShutdown || ack.UnknownOwnerRetained || !reflect.DeepEqual(ack.role(t, "push").JoinedAndClosed, want) {
		t.Fatal("actual required push resource/admission close order differs")
	}
	for _, r := range ack.Roles {
		if !r.CanonicalDrain {
			t.Fatal("actual ACK owner lacks canonical drain", r.Role)
		}
	}
	if unknown.ActualTokenRPCSucceeded || unknown.CanonicalShutdown || !unknown.UnknownOwnerRetained || unknown.ACKLoss.ExactDeleteRequests != 1 || !unknown.ACKLoss.ActualUpstreamIntegerOne || !unknown.ACKLoss.ActualReplyDiscarded || unknown.ACKLoss.TransportFailure {
		t.Fatal("actual Redis committed mutation/lost ACK was retried or given permission")
	}
	for _, r := range unknown.Roles {
		if r.CanonicalDrain != (r.Role != "push") {
			t.Fatal("unknown owner or other original resources lost", r.Role)
		}
	}
}

func (p actualGraphPhase) assertActualEndpoints(t *testing.T, served []actualReaderEndpoint) {
	t.Helper()
	if len(p.Readers) != 2 || len(p.Endpoints) != 2 || !p.BothActualConnectionsNotServing {
		t.Fatal("actual original reader phase/endpoint evidence absent")
	}
	seen := map[string]bool{}
	for _, reader := range p.Readers {
		found := false
		for _, point := range p.Endpoints {
			if point.Role != reader.Role {
				continue
			}
			if found || seen[point.Role] {
				t.Fatal("duplicate reader endpoint")
			}
			found = true
			seen[point.Role] = true
			host, port, e := net.SplitHostPort(point.Endpoint)
			number, e2 := strconv.Atoi(port)
			_, e3 := strconv.ParseUint(point.ListenerInode, 10, 64)
			if e != nil || e2 != nil || e3 != nil || host != "127.0.0.1" || number < 1 || number > 65535 || point.PID != reader.PID || point.StartToken != reader.StartToken || point.ConfigurationSHA256 != reader.Registration.ConfigurationSHA256 || !point.ActualTCPConnected || !point.OriginalListenerRetained || point.ActualUnimplementedAfterRelease {
				t.Fatal("deadline lacked original process listener ownership or a real connection")
			}
			if served != nil {
				ok := false
				for _, after := range served {
					if after.Role == point.Role {
						if ok || after.PID != point.PID || after.StartToken != point.StartToken || after.Endpoint != point.Endpoint || after.ConfigurationSHA256 != point.ConfigurationSHA256 || after.ListenerInode != point.ListenerInode || !after.ActualTCPConnected || !after.OriginalListenerRetained || !after.ActualUnimplementedAfterRelease {
							t.Fatal("released reader did not serve the original endpoint")
						}
						ok = true
					}
				}
				if !ok {
					t.Fatal("released reader response absent")
				}
			}
		}
		if !found {
			t.Fatal("actual reader listener absent")
		}
	}
}
func assertActualCancelledReaderPhase(t *testing.T, scenario, name string, ack actualGraphEvidence) {
	t.Helper()
	value := readActualFullgraphEvidence(t, scenario)
	cancelled := value.Cancellation
	phase := value.phase(t, name)
	phase.assertActualEndpoints(t, nil)
	expected := 2
	if name == "known-readiness-still-keeps-writer-fence" {
		expected = 14
	}
	if cancelled.PausedPhase != name || cancelled.ActualPreparedRoles != expected || len(cancelled.Roles) != expected || !cancelled.KnownAdmissionAbsent || !cancelled.ReaderDiscoveryAbsentBefore || !cancelled.ReaderDiscoveryAbsentAfter || !cancelled.OriginalStopReturnedError || phase.ActualPreparedRoles != expected || phase.KnownAdmissionSHA256 != "" {
		t.Fatal("original cancelled owner obtained publication or lost stop outcome")
	}
	if expected == 2 && (value.DataReadySHA256 != "" || phase.DataReadySHA256 != "") || expected == 14 && (!ownedHash(value.DataReadySHA256) || phase.DataReadySHA256 != value.DataReadySHA256) {
		t.Fatal("cancelled reader borrowed a later data publication")
	}
	seen := map[string]bool{}
	for _, row := range cancelled.Roles {
		if seen[row.Role] || row.PID <= 1 || row.StartToken == "" || row.SourceSHA256 != value.SourceSHA256 || !ownedHash(row.BinarySHA256) || !ownedHash(row.OwnerNonceSHA256) || !row.ActualAbsent || !row.OriginalWaitObserved || !row.AnnouncementAbsent || !row.CanonicalDrainAbsent || !row.PrivateReleaseAbsent {
			t.Fatal("cancelled original child was not joined or fabricated ready/drain")
		}
		seen[row.Role] = true
	}
	for _, reader := range phase.Readers {
		matched := false
		for _, row := range cancelled.Roles {
			if row.Role == reader.Role {
				matched = row.PID == reader.PID && row.StartToken == reader.StartToken && row.OwnerNonceSHA256 == reader.OwnerNonceSHA256
			}
		}
		if !matched {
			t.Fatal("cancelled reader changed process identity")
		}
	}
	// A permanently unreachable endpoint must not satisfy the deadline oracle:
	// the successful independent plane must bind and serve its SAME two sockets.
	if len(ack.ReaderServed) != 2 {
		t.Fatal("real released reader response absent")
	}
	ack.phase(t, name).assertActualEndpoints(t, ack.ReaderServed)
}
