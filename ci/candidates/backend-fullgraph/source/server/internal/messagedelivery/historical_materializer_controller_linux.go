//go:build linux

package messagedelivery

import (
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

// NewReviewedHistoricalImportController is the private operator's concrete
// assembly: actual Store writer, actual owned exclusion agent/live capture,
// authoritative existing wire namespace algorithm, and the SAME reviewed
// old-epoch verifier for discovery and page admission. It opens no network
// connection here and does not activate a runtime or set ReadReady. Empty
// explicitly reviewed prior trust is allowed only by the source-trust loader;
// each original locator without trust remains a durable pending disposition.
func NewReviewedHistoricalImportController(config namespaceimport.Config, writer *NamespaceImportWriter, journal namespaceimport.Journal, agent *ownedcontrol.OwnedAgent, live *namespaceinventory.LiveSource, options ownedcontrol.HistoricalCaptureOptions, trustPath, trustSHA string) (*namespaceimport.Controller, *ownedcontrol.HistoricalCaptureSource, error) {
	if writer == nil || !writer.validHeader(config.Header) || options.OriginalHistoryTrustPath != trustPath || options.OriginalHistoryTrustSHA256 != trustSHA {
		return nil, nil, ErrUnprepared
	}
	original, e := LoadOriginalHistoryVerifier(trustPath, trustSHA, config.Header)
	if e != nil {
		return nil, nil, e
	}
	describe := func(message *sdkws.MsgData) (namespaceimport.NamespaceOriginDescriptor, error) {
		value, e := DescribeNamespace(message)
		if e != nil {
			return namespaceimport.NamespaceOriginDescriptor{}, e
		}
		return namespaceimport.NamespaceOriginDescriptor{CID: value.CID, Kind: value.Kind, GroupID: value.GroupID, OriginIDs: append([]string(nil), value.OriginIDs...), SHA256: value.SHA256}, nil
	}
	return ownedcontrol.NewOwnedHistoricalImportController(config, writer, journal, agent, live, describe, options, original, original)
}
