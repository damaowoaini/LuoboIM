package messagedelivery

import (
	"sort"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

const OrdinaryModifierEncoding = "ordinary-msgdata-v1"
const LegacyModifierBatchEncoding = "legacy-modify-batch-v1"

type modifierCustodyMetadata struct {
	Normal, Branch, Notification string
	CIDs, Owners                 []string
	Types                        []int32
	Count                        int64
}

// The historical producer's actual protocol is MsgDataToModifyByMQ:
// messages=field1 (repeated bytes), conversationID=field2. Scan one message at
// a time so raw4MiB cannot become a huge repeated-struct allocation. The raw
// frame itself is retained unchanged; this is neither a projection nor a new
// per-message Kafka position/source.
func modifierCustodyMessages(raw KafkaOriginalRecord) (modifierCustodyMetadata, error) {
	meta := modifierCustodyMetadata{}
	cids, owners, types := map[string]bool{}, map[string]bool{}, map[int32]bool{}
	owners[hash([]byte(raw.Record.Actor))] = true
	accept := func(body []byte, expectedNotice string, ordinary bool) error {
		var m sdkws.MsgData
		if proto.Unmarshal(body, &m) != nil || CheckSupportedMessageContentType(m.ContentType) != ErrUnsupportedModifier || m.Seq < 0 || ordinary && m.Seq != 0 || !identity(m.SendID) || !identity(m.RecvID) {
			return ErrInvalid
		}
		if _, e := DescribeNamespace(&m); e != nil {
			return e
		}
		normal, branch, notice := msgprocessor.GetChatConversationIDByMsg(&m), msgprocessor.GetConversationIDByMsg(&m), msgprocessor.GetNotificationConversationIDByMsg(&m)
		if !identity(normal) || !identity(branch) || !identity(notice) || expectedNotice != "" && notice != expectedNotice {
			return ErrInvalid
		}
		if meta.Count == 0 {
			meta.Normal, meta.Branch, meta.Notification = normal, branch, notice
		}
		for _, cid := range []string{normal, branch, notice} {
			cids[cid] = true
		}
		owners[hash([]byte(m.SendID))] = true
		// Custody owners are original source principals, never a guessed group
		// roster or delivery recipient grant. Direct peer identity is explicit.
		if m.GroupID == "" {
			owners[hash([]byte(m.RecvID))] = true
		}
		types[m.ContentType] = true
		meta.Count++
		return nil
	}
	switch raw.Encoding {
	case OrdinaryModifierEncoding:
		if e := accept(raw.Record.Body, "", true); e != nil {
			return meta, e
		}
	case LegacyModifierBatchEncoding:
		expected := ""
		count := 0
		scan := func(consume func(protowire.Number, []byte) error) error {
			rest := raw.Record.Body
			for len(rest) > 0 {
				number, kind, n := protowire.ConsumeTag(rest)
				if n < 0 || kind != protowire.BytesType || number != 1 && number != 2 {
					return ErrInvalid
				}
				rest = rest[n:]
				body, n := protowire.ConsumeBytes(rest)
				if n < 0 {
					return ErrInvalid
				}
				rest = rest[n:]
				if e := consume(number, body); e != nil {
					return e
				}
			}
			return nil
		}
		if e := scan(func(n protowire.Number, b []byte) error {
			if n == 2 {
				if count != 0 || !identity(string(b)) {
					return ErrInvalid
				}
				expected = string(b)
				count++
			}
			return nil
		}); e != nil || count != 1 {
			return meta, ErrInvalid
		}
		if e := scan(func(n protowire.Number, b []byte) error {
			if n == 1 {
				return accept(b, expected, false)
			}
			return nil
		}); e != nil {
			return meta, e
		}
	default:
		return meta, ErrUnsupportedCustodyScope
	}
	if meta.Count < 1 {
		return meta, ErrInvalid
	}
	for cid := range cids {
		meta.CIDs = append(meta.CIDs, cid)
	}
	sort.Strings(meta.CIDs)
	for owner := range owners {
		meta.Owners = append(meta.Owners, owner)
	}
	sort.Strings(meta.Owners)
	for kind := range types {
		meta.Types = append(meta.Types, kind)
	}
	sort.Slice(meta.Types, func(i, j int) bool { return meta.Types[i] < meta.Types[j] })
	return meta, nil
}
