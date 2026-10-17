//go:build linux

package messagedelivery

import (
	"crypto/ed25519"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

// Private operator composition, not a network request/JSON permit. All process,
// credential, broker and storage handles below are the real controlled handles;
// the normal OwnedAgent still verifies every declared writer and source plane.
type HistoricalEpochImportInputs struct {
	Previous          *HistoryReadinessPreparation
	Retirement        *ownedcontrol.VerifiedHistoryReaderRetirement
	TargetStore       *Store
	Config            namespaceimport.Config
	SessionPath       string
	Launcher          *ownedcontrol.Launcher
	Writers           []ownedcontrol.OwnedWriter
	Kafka             *ownedcontrol.KafkaProbe
	MongoUsers        []ownedcontrol.MongoExclusion
	RedisUsers        []ownedcontrol.RedisExclusion
	KafkaUsers        []ownedcontrol.KafkaExclusion
	ControlKeyID      string
	ControlPrivateKey ed25519.PrivateKey
	Journal           namespaceimport.Journal
	Live              *namespaceinventory.LiveSource
	Capture           ownedcontrol.HistoricalCaptureOptions
}
type HistoricalEpochImport struct {
	Controller *namespaceimport.Controller
	Writer     *NamespaceImportWriter
	Agent      *ownedcontrol.OwnedAgent
	Source     *ownedcontrol.HistoricalCaptureSource
	Metadata   *HistoricalEpochMetadataBootstrap
}

// This is the actual next-epoch caller: Begin invokes the same Agent's real
// acquisition/drain/exclusion, then its metadata adapter performs the sole
// controlled base-marker transition BEFORE capture. Controller Capture,
// ReadPage, semantic validation, ApplyPage, closures and final publication all
// use the SAME concrete source/writer. No callback may substitute page bodies,
// a fence SHA, Ready flags, a current roster or another raw snapshot.
//
// Construction does not execute any probe, service stop, capture or DB write.
// The original owning caller first completes Retirement.Advance. This factory
// does not restore a write capability after restart/unknown commit: use the
// read-only InspectHistoricalEpochTransition/Controller.Inspect boundaries.
func NewOwnedHistoricalEpochImport(in HistoricalEpochImportInputs) (*HistoricalEpochImport, error) {
	if in.Previous == nil || in.Retirement == nil || in.TargetStore == nil || in.Journal == nil || in.Live == nil {
		return nil, ErrInvalid
	}
	writer, e := NewNamespaceImportWriter(in.TargetStore, in.Config.Header)
	if e != nil {
		return nil, e
	}
	metadata, e := NewHistoricalEpochMetadataBootstrap(in.Previous, in.Retirement, writer, in.Config, in.SessionPath)
	if e != nil {
		return nil, e
	}
	agent, e := ownedcontrol.NewOwnedAgent(in.Config.Header, in.Launcher, in.Writers, in.Kafka, in.MongoUsers, in.RedisUsers, in.KafkaUsers, metadata, in.ControlKeyID, in.ControlPrivateKey)
	if e != nil {
		return nil, e
	}
	original, e := LoadOriginalHistoryVerifier(in.Capture.OriginalHistoryTrustPath, in.Capture.OriginalHistoryTrustSHA256, in.Config.Header)
	if e != nil {
		return nil, e
	}
	describe := func(message *sdkws.MsgData) (namespaceimport.NamespaceOriginDescriptor, error) {
		actual, e := DescribeNamespace(message)
		if e != nil {
			return namespaceimport.NamespaceOriginDescriptor{}, e
		}
		return namespaceimport.NamespaceOriginDescriptor{CID: actual.CID, Kind: actual.Kind, GroupID: actual.GroupID, OriginIDs: append([]string(nil), actual.OriginIDs...), SHA256: actual.SHA256}, nil
	}
	controller, source, e := ownedcontrol.NewOwnedHistoricalImportController(in.Config, writer, in.Journal, agent, in.Live, describe, in.Capture, original, original)
	if e != nil {
		return nil, e
	}
	if e = metadata.BindCapturedSource(source); e != nil {
		return nil, e
	}
	return &HistoricalEpochImport{Controller: controller, Writer: writer, Agent: agent, Source: source, Metadata: metadata}, nil
}
