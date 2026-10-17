package favorite

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/apiresp"
)

type retentionSourceProbe struct {
	allowed                     bool
	err                         error
	calls                       int
	actor, conversation, source string
}

func (p *retentionSourceProbe) Retainable(_ context.Context, actor, conversation, source string) (bool, error) {
	p.calls++
	p.actor, p.conversation, p.source = actor, conversation, source
	return p.allowed, p.err
}

func retentionCode(err error) int {
	if err == nil {
		return 0
	}
	return apiresp.ParseError(err).ErrCode
}

func TestRetentionGateRequiresRealVisibleSourceForAllChatKinds(t *testing.T) {
	for kind := 1; kind <= 6; kind++ {
		probe := &retentionSourceProbe{allowed: true}
		gate := &RetentionGate{Source: probe}
		req := Request{UserID: "actor", FavoriteType: kind, SourceID: "message", ConversationID: "si_actor_peer", Content: `{"clientMsgID":"message","attachedInfoElem":{"isPrivateChat":false},"customElem":{"data":"opaque encrypted content"}}`}
		if err := gate.Authorize(context.Background(), "actor", req); err != nil || probe.calls != 1 || probe.actor != "actor" || probe.conversation != req.ConversationID || probe.source != "message" {
			t.Fatal("chat kind skipped exact owner/source authorization", kind, err)
		}
		probe.allowed = false
		if code := retentionCode(gate.Authorize(context.Background(), "actor", req)); code != 1002 {
			t.Fatal("client false flag overrode authoritative private/missing message", kind, code)
		}
		probe.err = errors.New("private database password and message body")
		err := gate.Authorize(context.Background(), "actor", req)
		if retentionCode(err) != 500 || strings.Contains(apiresp.ParseError(err).ErrMsg, "password") {
			t.Fatal("dependency failure leaked details or authorized retention")
		}
	}
	var gate *RetentionGate
	if retentionCode(gate.Authorize(context.Background(), "actor", Request{UserID: "actor", FavoriteType: 1, SourceID: "message", ConversationID: "si_actor_peer"})) != 500 {
		t.Fatal("missing source checker did not fail closed")
	}
	// Legacy standalone file sources were never conversation messages.
	if err := gate.Authorize(context.Background(), "actor", Request{UserID: "actor", FavoriteType: 2, SourceID: "standalone-file", Content: `{"fileName":"report.pdf","contentType":"application/pdf","url":"https://files.test/report"}`}); err != nil {
		t.Fatal("standalone non-chat file compatibility broken", err)
	}
}

func TestRetentionGateRejectsPrivacyAliasesNestedContentAndForgedScopeBeforeRead(t *testing.T) {
	deep := map[string]any{"attachedInfoElem": map[string]any{"isPrivateChat": false}}
	for i := 0; i < 34; i++ {
		deep = map[string]any{"quoteElem": map[string]any{"quoteMessage": deep}}
	}
	deepJSON, _ := json.Marshal(deep)
	for _, tc := range []struct{ name, content, owner, conversation string }{
		{"foreign actor", `{}`, "other", "si_actor_peer"},
		{"missing conversation", `{"clientMsgID":"message"}`, "actor", ""},
		{"substituted client message", `{"clientMsgID":"different"}`, "actor", "si_actor_peer"},
		{"private typed snapshot", `{"attachedInfoElem":{"isPrivateChat":true}}`, "actor", "si_actor_peer"},
		{"private encoded snapshot", `{"attachedInfo":"{\"isPrivateChat\":true}"}`, "actor", "si_actor_peer"},
		{"conflicting representations", `{"attachedInfo":"{\"isPrivateChat\":true}","attachedInfoElem":{"isPrivateChat":false}}`, "actor", "si_actor_peer"},
		{"malformed flag", `{"attachedInfoElem":{"isPrivateChat":"false"}}`, "actor", "si_actor_peer"},
		{"malformed encoded metadata", `{"attachedInfo":"not JSON"}`, "actor", "si_actor_peer"},
		{"private quote", `{"quoteElem":{"quoteMessage":{"attachedInfoElem":{"isPrivateChat":true}}}}`, "actor", "si_actor_peer"},
		{"private merge", `{"mergeElem":{"multiMessage":[{"attachedInfoElem":{"isPrivateChat":true}}]}}`, "actor", "si_actor_peer"},
		{"malformed merge", `{"mergeElem":{"multiMessage":"not a list"}}`, "actor", "si_actor_peer"},
		{"bounded nested inspection", string(deepJSON), "actor", "si_actor_peer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := &retentionSourceProbe{allowed: true}
			gate := &RetentionGate{Source: probe}
			code := retentionCode(gate.Authorize(context.Background(), "actor", Request{UserID: tc.owner, FavoriteType: 1, SourceID: "message", ConversationID: tc.conversation, Content: tc.content}))
			if code != 1002 || probe.calls != 0 {
				t.Fatal("untrusted/unknown private scope reached source or storage", code, probe.calls)
			}
		})
	}
	probe := &retentionSourceProbe{allowed: true}
	gate := &RetentionGate{Source: probe}
	if retentionCode(gate.Authorize(context.Background(), "actor", Request{UserID: "actor", FavoriteType: 1, SourceID: "message", ConversationID: "si_actor_peer", Content: strings.Repeat("x", 128*1024+1)})) != 1001 || probe.calls != 0 {
		t.Fatal("oversized content reached source")
	}
}

func TestAuthoritativeRetentionUsesExplicitBurnFlagAndPlainQuoteMergeMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, attached, content string
		kind                    int32
		allowed                 bool
	}{
		{"ordinary single chat", "", `{"content":"ordinary"}`, constant.Text, true},
		{"ordinary encrypted payload", `{"isPrivateChat":false}`, "opaque cipher text", constant.Custom, true},
		{"private despite forged client false", `{"isPrivateChat":true}`, `{}`, constant.Text, false},
		{"unknown metadata", `not-json`, `{}`, constant.Text, false},
		{"server quote contains private", `{}`, `{"quoteMessage":{"attachedInfoElem":{"isPrivateChat":true}}}`, constant.Quote, false},
		{"server merge contains private", `{}`, `{"multiMessage":[{"attachedInfo":"{\"isPrivateChat\":true}"}]}`, constant.Merger, false},
		{"opaque custom is not protocol metadata", `{}`, `{"isPrivateChat":true}`, constant.Custom, true},
		{"encrypted quote remains opaque", `{"isPrivateChat":false}`, "opaque cipher text", constant.Quote, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if MessageAllowsRetention(tc.attached, tc.kind, []byte(tc.content)) != tc.allowed {
				t.Fatal("explicit private and ordinary encryption were confused")
			}
		})
	}
}

func TestPublicRetentionQuarantinesKnownLegacyPrivateWithoutMutatingStoredData(t *testing.T) {
	private := Favorite{FavoriteID: "keep-id-for-removal", UserID: "actor", FavoriteType: 1, SourceID: "message", Content: `{"attachedInfoElem":{"isPrivateChat":true},"textElem":{"content":"private body"}}`, Title: "private title", ThumbnailURL: "https://files.test/private", Notes: "private notes", Tags: []string{"private tag"}, Ex: "private ex", CategoryID: "category", CreateTime: 123}
	ordinary := Favorite{FavoriteID: "ordinary", Content: `{"customElem":{"data":"opaque encrypted payload"}}`, Title: "ordinary title"}
	original := map[string]any{"favorites": []Favorite{private, ordinary}, "favoriteInfo": private, "total": int64(2), "hasMore": false}
	filtered := PublicRetentionResult(original).(map[string]any)
	encoded, _ := json.Marshal(filtered)
	for _, secret := range []string{"private body", "private title", "private notes", "private tag", "private ex", "https://files.test/private"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("quarantined response leaked retained content", secret)
		}
	}
	item := filtered["favoriteInfo"].(Favorite)
	if !item.RetentionBlocked || item.FavoriteID != private.FavoriteID || item.CategoryID != private.CategoryID || item.CreateTime != private.CreateTime {
		t.Fatal("quarantine lost delete/classification metadata")
	}
	if original["favoriteInfo"].(Favorite).Content != private.Content || original["favorites"].([]Favorite)[0].Title != "private title" {
		t.Fatal("response filtering silently mutated stored data")
	}
	if filtered["favorites"].([]Favorite)[1].Content != ordinary.Content || filtered["favorites"].([]Favorite)[1].RetentionBlocked {
		t.Fatal("ordinary encrypted favorite was quarantined")
	}
}
