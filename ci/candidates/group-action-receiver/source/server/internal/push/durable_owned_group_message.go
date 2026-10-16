package push

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/pkg/callbackstruct"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"google.golang.org/protobuf/proto"
)

// Private registry version is distinct from ordinary Kafka callback material.
// The same secure file reader binds owned0700/0600/exactSHA/no-link/source fd.
type ownedGroupMessageSpec struct {
	ProfileSHA256 string                `json:"profileSHA256"`
	Base          string                `json:"base"`
	Secret        string                `json:"secret"`
	Before        config.CallBackConfig `json:"before"`
	Modify        config.CallBackConfig `json:"modify"`
	After         config.CallBackConfig `json:"after"`
	SuperGroup    config.CallBackConfig `json:"superGroup"`
	Online        config.CallBackConfig `json:"online"`
}
type ownedGroupMessageRegistry struct {
	Version    string                  `json:"version"`
	Database   string                  `json:"database"`
	Epoch      string                  `json:"epoch"`
	Activation string                  `json:"activationSHA256"`
	Entries    []ownedGroupMessageSpec `json:"entries"`
}
type FrozenOwnedGroupMessageCallbacks struct {
	profile   string
	endpoints map[string]frozenKafkaCallbackEndpoint
	push      *FrozenKafkaPushCallbacks
}

func (e *FrozenOwnedGroupMessageCallbacks) ProfileFingerprint() string {
	if e == nil {
		return ""
	}
	return e.profile
}
func (e *FrozenOwnedGroupMessageCallbacks) Close() error {
	// Worker ownership must cancel+join before Close. No active call can race the
	// zeroization; the managed runtime owns this adapter, not a shared singleton.
	if e != nil {
		if e.push != nil {
			for key, p := range e.push.endpoints {
				for i := range p.secret {
					p.secret[i] = 0
				}
				delete(e.push.endpoints, key)
			}
		}
		for key, p := range e.endpoints {
			for i := range p.secret {
				p.secret[i] = 0
			}
			delete(e.endpoints, key)
		}
	}
	return nil
}
func LoadOwnedGroupMessageCallbacks(ctx context.Context, store *messagedelivery.Store, input OwnedKafkaCallbackFile, database, epoch, activation string, profiles []deliveryprofile.Group) (map[string]*FrozenOwnedGroupMessageCallbacks, error) {
	if ctx == nil || ctx.Err() != nil || database == "" || store == nil || len(profiles) == 0 {
		return nil, ErrOwnedCallbackFile
	}
	required := map[string]deliveryprofile.Group{}
	needPrivate := false
	for _, p := range profiles {
		sha, err := p.Fingerprint()
		if err != nil || p.Transport.Epoch != epoch || p.Transport.ActivationSHA256 != activation {
			return nil, ErrOwnedCallbackFile
		}
		if _, ok := required[sha]; ok {
			continue
		}
		required[sha] = p
		needPrivate = needPrivate || p.BeforeMessage.Binding != "" || p.ModifyMessage.Binding != "" || p.AfterMessage.Binding != "" || p.Transport.BeforeGroupPushBinding != "" || p.Transport.OnlinePushBinding != ""
	}
	var registry ownedGroupMessageRegistry
	if needPrivate {
		raw, err := readOwnedCallbackFile(input)
		if err != nil {
			return nil, ErrOwnedCallbackFile
		}
		defer func() {
			for i := range raw {
				raw[i] = 0
			}
		}()
		if !callbackExactKeys(raw, "version", "database", "epoch", "activationSHA256", "entries") {
			return nil, ErrOwnedCallbackFile
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&registry) != nil || decoder.Decode(new(any)) != io.EOF || registry.Version != "owned-group-message-callbacks-v1" || registry.Database != database || registry.Epoch != epoch || registry.Activation != activation {
			return nil, ErrOwnedCallbackFile
		}
	} else if input.Path != "" || input.ExpectedSHA256 != "" {
		return nil, ErrOwnedCallbackFile
	}
	specs := map[string]ownedGroupMessageSpec{}
	for _, spec := range registry.Entries {
		if _, ok := specs[spec.ProfileSHA256]; ok {
			return nil, ErrOwnedCallbackFile
		}
		if _, ok := required[spec.ProfileSHA256]; !ok {
			return nil, ErrOwnedCallbackFile
		}
		specs[spec.ProfileSHA256] = spec
	}
	out := map[string]*FrozenOwnedGroupMessageCallbacks{}
	fail := func() (map[string]*FrozenOwnedGroupMessageCallbacks, error) {
		for _, engine := range out {
			_ = engine.Close()
		}
		return nil, ErrOwnedCallbackFile
	}
	for sha, p := range required {
		engine := &FrozenOwnedGroupMessageCallbacks{profile: sha, endpoints: map[string]frozenKafkaCallbackEndpoint{}}
		out[sha] = engine
		enabled := p.BeforeMessage.Binding != "" || p.ModifyMessage.Binding != "" || p.AfterMessage.Binding != "" || p.Transport.BeforeGroupPushBinding != "" || p.Transport.OnlinePushBinding != ""
		if !enabled {
			continue
		}
		spec, ok := specs[sha]
		if !ok {
			return fail()
		}
		for _, entry := range []struct {
			kind, logical, command string
			policy                 deliveryprofile.Callback
			conf                   config.CallBackConfig
		}{
			{"before-message", "before-group-message", callbackstruct.CallbackBeforeSendGroupMsgCommand, p.BeforeMessage, spec.Before},
			{"modify-message", "modify-message", callbackstruct.CallbackMsgModifyCommand, p.ModifyMessage, spec.Modify},
			{"after-message", "after-group-message", callbackstruct.CallbackAfterSendGroupMsgCommand, p.AfterMessage, spec.After},
		} {
			if entry.policy.Binding != "" && entry.conf.CallbackTimeOut != entry.policy.TimeoutSeconds {
				return fail()
			}
			endpoint, err := freezeKafkaCallbackEndpoint(database, epoch, entry.logical, entry.command, spec.Base, spec.Secret, entry.policy.Binding, entry.policy.Mode, entry.conf)
			if err != nil {
				return fail()
			}
			if endpoint.binding != "" {
				engine.endpoints[entry.kind] = endpoint
			}
		}
		pushEngine, err := NewFrozenKafkaPushCallbacks(store, database, messagedelivery.KafkaDeliveryPolicy(p.Transport), spec.Base, spec.Secret, spec.SuperGroup, spec.Online)
		if err != nil {
			return fail()
		}
		engine.push = pushEngine
	}
	return out, nil
}
func (e *FrozenOwnedGroupMessageCallbacks) PushCallbacks() *FrozenKafkaPushCallbacks {
	if e == nil {
		return nil
	}
	return e.push
}

func (e *FrozenOwnedGroupMessageCallbacks) RequestBody(ctx context.Context, call messagedelivery.OwnedGroupMessageCall) ([]byte, error) {
	if e == nil || ctx == nil || ctx.Err() != nil || e.profile != call.ProfileSHA256 || call.Message == nil {
		return nil, messagedelivery.ErrInvalid
	}
	endpoint, ok := e.endpoints[call.Stage]
	if !ok || endpoint.binding != call.Callback.Binding || endpoint.mode != call.Callback.Mode {
		return nil, messagedelivery.ErrUnprepared
	}
	m := call.Message
	content := string(m.Content)
	if m.ContentType >= constant.NotificationBegin && m.ContentType <= constant.NotificationEnd {
		var tips sdkws.TipsComm
		_ = proto.Unmarshal(m.Content, &tips)
		content = tips.JsonDetail
	}
	base := callbackstruct.CommonCallbackReq{SendID: m.SendID, ServerMsgID: m.ServerMsgID, CallbackCommand: endpoint.command, ClientMsgID: m.ClientMsgID, OperationID: call.OperationID, SenderPlatformID: m.SenderPlatformID, SenderNickname: m.SenderNickname, SessionType: m.SessionType, MsgFrom: m.MsgFrom, ContentType: m.ContentType, Status: m.Status, CreateTime: m.CreateTime, AtUserIDList: append([]string(nil), m.AtUserIDList...), SenderFaceURL: m.SenderFaceURL, Content: content, Seq: uint32(m.Seq), Ex: m.Ex}
	var body any
	switch call.Stage {
	case "before-message":
		body = callbackstruct.CallbackBeforeSendGroupMsgReq{CommonCallbackReq: base, GroupID: m.GroupID}
	case "after-message":
		body = callbackstruct.CallbackAfterSendGroupMsgReq{CommonCallbackReq: base, GroupID: m.GroupID}
	default:
		return nil, messagedelivery.ErrInvalid
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) > kafkaCallbackRequestLimit {
		return nil, messagedelivery.ErrInvalid
	}
	return encoded, nil
}
func (e *FrozenOwnedGroupMessageCallbacks) Execute(ctx context.Context, call messagedelivery.OwnedGroupMessageCall, dispatch *messagedelivery.OwnedMessageDispatch, body []byte) error {
	if e == nil || dispatch == nil || call.ProfileSHA256 != e.profile {
		return messagedelivery.ErrInvalid
	}
	expected, err := e.RequestBody(ctx, call)
	if err != nil || !bytes.Equal(expected, body) {
		return messagedelivery.ErrInvalid
	}
	endpoint := e.endpoints[call.Stage]
	current, cancel := context.WithTimeout(ctx, endpoint.timeout)
	defer cancel()
	request, err := kafkaCallbackRequest(current, endpoint, call.OperationID, body)
	if err != nil {
		_ = dispatch.NotDispatched(ctx)
		return err
	}
	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext, DisableKeepAlives: true, ResponseHeaderTimeout: endpoint.timeout, TLSHandshakeTimeout: 2 * time.Second, MaxResponseHeaderBytes: 1 << 20, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: endpoint.timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if err = dispatch.BeforeHTTP(current, call, body); err != nil {
		return err
	}
	if err = current.Err(); err != nil {
		if dispatch.NotDispatched(ctx) != nil {
			return messagedelivery.ErrOwnedMessageUnknown
		}
		return err
	}
	// Every result after Do is dispatched/unknown unless an exact response proves
	// rejection/ACK. No HTTP retry/redirect or device-success claim is supplied.
	response, err := client.Do(request)
	if err != nil {
		return dispatch.Finish(ctx, "unknown", digestGateway([]byte("owned callback transport outcome unavailable")))
	}
	reply, readErr := io.ReadAll(io.LimitReader(response.Body, kafkaCallbackBodyLimit+1))
	closeErr := response.Body.Close()
	complete := readErr == nil && closeErr == nil && len(reply) <= kafkaCallbackBodyLimit
	proof, _ := json.Marshal([]any{response.StatusCode, complete, digestGateway(reply)})
	return dispatch.Finish(ctx, kafkaCallbackResponse(response.StatusCode, reply, complete), digestGateway(proof))
}
