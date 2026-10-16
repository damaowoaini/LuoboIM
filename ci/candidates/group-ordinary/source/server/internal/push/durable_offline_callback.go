package push

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/pkg/callbackstruct"
)

type FrozenKafkaOfflineCallback struct {
	store       *messagedelivery.Store
	fingerprint string
	endpoint    frozenKafkaCallbackEndpoint
	closed      atomic.Bool
	mu          sync.RWMutex
	// Only tests replace this physical boundary; production always owns and
	// closes the bounded non-replaying transport constructed below.
	testTransport http.RoundTripper
}

func (e *FrozenKafkaOfflineCallback) PolicyFingerprint() string {
	if e == nil {
		return ""
	}
	return e.fingerprint
}
func (e *FrozenKafkaOfflineCallback) CheckReady(ctx context.Context) error {
	if e == nil || e.store == nil || e.closed.Load() || ctx == nil || e.endpoint.binding == "" || len(e.endpoint.secret) == 0 {
		return messagedelivery.ErrUnprepared
	}
	return e.store.CheckKafkaReady(ctx)
}
func (e *FrozenKafkaOfflineCallback) Close() error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed.CompareAndSwap(false, true) {
		for i := range e.endpoint.secret {
			e.endpoint.secret[i] = 0
		}
	}
	return nil
}
func offlineCallbackBody(input messagedelivery.KafkaOfflineCallbackInput) ([]byte, error) {
	m := input.Message
	if m == nil {
		return nil, messagedelivery.ErrInvalid
	}
	request := callbackstruct.CallbackBeforePushReq{UserStatusBatchCallbackReq: callbackstruct.UserStatusBatchCallbackReq{UserStatusBaseCallback: callbackstruct.UserStatusBaseCallback{CallbackCommand: callbackstruct.CallbackOfflinePushCommand, OperationID: input.OperationID, PlatformID: int(m.SenderPlatformID), Platform: constant.PlatformIDToName(int(m.SenderPlatformID))}, UserIDList: append([]string(nil), input.Users...)}, OfflinePushInfo: m.OfflinePushInfo, ClientMsgID: m.ClientMsgID, SendID: m.SendID, GroupID: m.GroupID, ContentType: m.ContentType, SessionType: m.SessionType, AtUserIDs: append([]string(nil), m.AtUserIDList...), Content: GetContent(m)}
	body, err := json.Marshal(request)
	if err != nil || len(body) > kafkaCallbackRequestLimit {
		return nil, messagedelivery.ErrInvalid
	}
	return body, nil
}

// Every nested object rejects duplicate/case-fold aliases; canonical response
// keys are also checked by exact JSON re-encoding below. No last-wins parser.
func offlineUniqueJSON(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	var walk func(int) bool
	walk = func(depth int) bool {
		if depth > 48 {
			return false
		}
		token, err := d.Token()
		if err != nil {
			return false
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, err := d.Token()
				s, ok := k.(string)
				if err != nil || !ok || seen[strings.ToLower(s)] {
					return false
				}
				seen[strings.ToLower(s)] = true
				if !walk(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for d.More() {
				if !walk(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim(']')
		}
		return false
	}
	return walk(0) && d.Decode(new(any)) == io.EOF
}
func parseOfflineCallback(status int, raw []byte, complete bool) (string, []string, *sdkws.OfflinePushInfo) {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return "rejected", nil, nil
	}
	if !complete || status < 200 || status >= 300 || !offlineUniqueJSON(raw) {
		return "unknown", nil, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return "unknown", nil, nil
	}
	keys := map[string]bool{"actionCode": true, "errCode": true, "errMsg": true, "errDlt": true, "nextCode": true, "userIDList": true, "offlinePushInfo": true}
	for k := range fields {
		if !keys[k] {
			for canonical := range keys {
				if strings.EqualFold(k, canonical) {
					return "unknown", nil, nil
				}
			}
		}
	}
	if info, ok := fields["offlinePushInfo"]; ok && string(info) != "null" {
		var f map[string]json.RawMessage
		if json.Unmarshal(info, &f) != nil {
			return "unknown", nil, nil
		}
		canonical := map[string]bool{"title": true, "desc": true, "ex": true, "iOSPushSound": true, "iOSBadgeCount": true, "signalInfo": true}
		for k := range f {
			if !canonical[k] {
				for exact := range canonical {
					if strings.EqualFold(k, exact) {
						return "unknown", nil, nil
					}
				}
			}
		}
	}
	var response callbackstruct.CallbackBeforePushResp
	if json.Unmarshal(raw, &response) != nil {
		return "unknown", nil, nil
	}
	if response.ActionCode != 0 || response.NextCode == callbackstruct.Next {
		return "rejected", nil, nil
	}
	return "acknowledged", response.UserIDs, response.OfflinePushInfo
}
func (e *FrozenKafkaOfflineCallback) ExecuteOffline(ctx context.Context, call messagedelivery.KafkaOfflineCall) error {
	if err := e.CheckReady(ctx); err != nil {
		return err
	}
	if call.PolicySHA256 != e.fingerprint {
		return messagedelivery.ErrConflict
	}
	settled, err := e.store.KafkaOfflineCallbackSettled(ctx, call)
	if err != nil || settled {
		return err
	}
	input, err := e.store.KafkaOfflineCallbackInput(ctx, call)
	if err != nil {
		return err
	}
	if input.Binding != e.endpoint.binding || input.FailureMode != e.endpoint.mode {
		return messagedelivery.ErrUnprepared
	}
	body, err := offlineCallbackBody(input)
	if err != nil {
		return err
	}
	current, cancel := context.WithTimeout(ctx, e.endpoint.timeout)
	defer cancel()
	e.mu.RLock()
	if e.closed.Load() {
		e.mu.RUnlock()
		return messagedelivery.ErrUnprepared
	}
	request, err := kafkaCallbackRequest(current, e.endpoint, input.OperationID, body)
	e.mu.RUnlock()
	if err != nil {
		return err
	}
	// HTTP construction/authentication precedes the attempt commit; no hidden
	// OAuth/vendor request or transport retry can happen while preparing a callback.
	physical := &http.Transport{DialContext: (&net.Dialer{Timeout: 2e9}).DialContext, DisableKeepAlives: true, ResponseHeaderTimeout: e.endpoint.timeout, TLSHandshakeTimeout: 2e9, MaxResponseHeaderBytes: 1 << 20, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}}
	defer physical.CloseIdleConnections()
	var roundTripper http.RoundTripper = physical
	if e.testTransport != nil {
		roundTripper = e.testTransport
	}
	client := &http.Client{Transport: roundTripper, Timeout: e.endpoint.timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	dispatch, err := e.store.BeginKafkaOfflineCallback(current, call, input.InputSHA256, digestGateway(body))
	if err != nil || dispatch == nil {
		return err
	}
	if err := current.Err(); err != nil {
		_ = dispatch.NotDispatched(ctx)
		return err
	}
	if err := dispatch.BeforeHTTP(current, call, body); err != nil {
		_ = dispatch.NotDispatched(ctx)
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return dispatch.Finish(ctx, "unknown", digestGateway([]byte("callback transport unconfirmed")), nil, nil)
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, kafkaCallbackBodyLimit+1))
	closeErr := response.Body.Close()
	complete := readErr == nil && closeErr == nil && len(raw) <= kafkaCallbackBodyLimit
	outcome, users, info := parseOfflineCallback(response.StatusCode, raw, complete)
	proof, _ := json.Marshal([]any{response.StatusCode, complete, digestGateway(raw)})
	return dispatch.Finish(ctx, outcome, digestGateway(proof), users, info)
}

// Local operator evidence parser: no endpoint, transport or credentials. The
// dedicated CLI still requires an exact original summary+independent evidence.
func ParseKafkaOfflineCallbackEvidence(raw []byte) ([]string, *sdkws.OfflinePushInfo, error) {
	outcome, users, info := parseOfflineCallback(http.StatusOK, raw, true)
	if outcome != "acknowledged" {
		return nil, nil, messagedelivery.ErrInvalid
	}
	return users, info, nil
}
