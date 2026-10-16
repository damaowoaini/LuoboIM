package groupeffects

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/pkg/callbackstruct"
)

// Endpoint is a trusted runtime configuration snapshot, never a stored URL or
// callback input. Namespace is the service's fixed tenant/database identity.
// HTTP remains a legacy configuration contract; no TLS upgrade is claimed.
type Endpoint struct {
	base, namespace string
	name            CallbackName
	secret          []byte
	timeout         time.Duration
}

func NewEndpoint(base, namespace string, name CallbackName, secret []byte, timeout time.Duration) (*Endpoint, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || (u.Scheme != "https" && u.Scheme != "http") || !validIdentity(namespace) || len(secret) == 0 || len(secret) > 4096 || timeout < time.Second || timeout > 15*time.Second || callbackCommand(name) == "" {
		return nil, ErrInvalid
	}
	clean := strings.TrimRight(u.Path, "/")
	if clean != "" && path.Clean(clean) != clean {
		return nil, ErrInvalid
	}
	u.Path = clean
	return &Endpoint{base: u.String(), namespace: namespace, name: name, secret: append([]byte(nil), secret...), timeout: timeout}, nil
}
func (e *Endpoint) Binding() string {
	if e == nil {
		return ""
	}
	body, _ := json.Marshal([]string{e.base, e.namespace, string(e.name)})
	return digest(body)
}
func callbackCommand(name CallbackName) string {
	switch name {
	case AfterCreate:
		return callbackstruct.CallbackAfterCreateGroupCommand
	case AfterSetInfo:
		return callbackstruct.CallbackAfterSetGroupInfoCommand
	case AfterSetMember:
		return callbackstruct.CallbackAfterSetGroupMemberInfoCommand
	case AfterJoin:
		return callbackstruct.CallbackAfterJoinGroupCommand
	case AfterQuit:
		return callbackstruct.CallbackQuitGroupCommand
	case AfterKick:
		return callbackstruct.CallbackKillGroupCommand
	case AfterDismiss:
		return callbackstruct.CallbackDisMissGroupCommand
	case AfterTransfer:
		return callbackstruct.CallbackTransferGroupOwnerAfter
	}
	return ""
}

// DeliverCallback performs at most one HTTP.Do after one acknowledged durable
// claim. It never follows a redirect, ignores an error because FailedContinue is
// configured, or treats HTTP status/permission errors as a pre-dispatch refusal.
// A crash in Dispatching stays inspectable until explicit drained confirmation.
func (s *Store) DeliverCallback(ctx context.Context, id string, revision int64, payloadSHA string, endpoint *Endpoint) error {
	if endpoint == nil || ctx == nil {
		return ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	if err := s.CheckReady(current); err != nil {
		cancel()
		return err
	}
	if endpoint.namespace != s.db.Name() {
		cancel()
		return ErrInvalid
	}
	r, err := s.read(current, id)
	var rolePolicy *RoleAfterPolicy
	if err == nil {
		rolePolicy, err = s.roleAfterPolicy(current, id)
	}
	cancel()
	if err != nil {
		return err
	}
	if rolePolicy != nil && endpoint.timeout != roleCallbackTimeout(rolePolicy.TimeoutSeconds) {
		return ErrInvalid
	}
	if r.Kind != AfterCallback || r.State != Pending || r.Revision != revision || r.PayloadSHA256 != payloadSHA {
		return ErrConflict
	}
	var p CallbackPayload
	if json.Unmarshal(r.Payload, &p) != nil || p.Name != endpoint.name || p.EndpointBinding != endpoint.Binding() {
		return ErrInvalid
	}
	// Configuration mismatch is checked BEFORE claiming an attempt. It causes
	// no HTTP request and does not consume/rewrite an unrelated pending intent.
	dispatch, err := s.BeginCallback(ctx, id, revision, payloadSHA)
	if err != nil {
		return err
	}
	return dispatch.deliver(ctx, endpoint, p)
}
func (d *Dispatch) deliver(parent context.Context, endpoint *Endpoint, p CallbackPayload) error {
	finish := func(outcome Outcome, hash string, continueOnTransportFailure ...bool) error {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer cancel()
		allow := len(continueOnTransportFailure) == 1 && continueOnTransportFailure[0] && d.roleAfter != nil && d.roleAfter.FailedContinue
		return d.finish(ctx, outcome, hash, allow)
	}
	if err := parent.Err(); err != nil {
		if finish(NotDispatched, "") != nil {
			return ErrUnconfirmed
		}
		return err
	}
	if p.Name != endpoint.name || p.EndpointBinding != endpoint.Binding() {
		if finish(NotDispatched, "") != nil {
			return ErrUnconfirmed
		}
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, endpoint.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.base+"/"+callbackCommand(p.Name), bytes.NewReader(p.Body))
	if err != nil {
		if finish(NotDispatched, "") != nil {
			return ErrUnconfirmed
		}
		return ErrInvalid
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		if finish(NotDispatched, "") != nil {
			return ErrUnconfirmed
		}
		return ErrUnavailable
	}
	stamp, nonceHex := strconv.FormatInt(time.Now().Unix(), 10), hex.EncodeToString(nonce[:])
	mac := hmac.New(sha256.New, endpoint.secret)
	mac.Write([]byte(stamp + "\n" + nonceHex + "\n" + callbackCommand(p.Name) + "\n"))
	mac.Write(p.Body)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("X-OpenIM-Callback-Timestamp", stamp)
	req.Header.Set("X-OpenIM-Callback-Nonce", nonceHex)
	req.Header.Set("X-OpenIM-Callback-Signature", hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("X-OpenIM-Effect-ID", d.row.ID)
	req.Header.Set(constant.OperationID, p.TraceOperationID)
	// Each effect gets one fresh connection, no automatic POST retry or redirect.
	// There is deliberately no Idempotency-Key header: the remote endpoint has
	// not promised deduplication. Stable Effect-ID is audit linkage only.
	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext, DisableKeepAlives: true, ResponseHeaderTimeout: endpoint.timeout, TLSHandshakeTimeout: 2 * time.Second, MaxResponseHeaderBytes: MaxPayload, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: endpoint.timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		_ = finish(Unknown, "", true)
		return ErrUnconfirmed
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxPayload+1))
	closeErr := resp.Body.Close()
	responseSHA := ""
	if readErr == nil && len(body) <= MaxPayload {
		responseSHA = digest(body)
	}
	if readErr != nil || closeErr != nil || len(body) > MaxPayload || resp.StatusCode < 200 || resp.StatusCode >= 300 || !callbackACK(body) {
		// Preserve the original failedContinue transport/JSON boundary, while
		// 401/403 and a syntactically valid callback rejection still block.
		transportFailure := readErr != nil || closeErr != nil || len(body) > MaxPayload || resp.StatusCode < 200 || resp.StatusCode >= 300 || !json.Valid(body)
		allow := transportFailure && resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden
		_ = finish(Unknown, responseSHA, allow)
		return ErrUnconfirmed
	}
	return finish(Acknowledged, responseSHA)
}

func callbackACK(body []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		name = strings.ToLower(name)
		if err != nil || !ok || seen[name] {
			return false
		}
		seen[name] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return false
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') || decoder.Decode(new(any)) != io.EOF {
		return false
	}
	var ack callbackstruct.CommonCallbackResp
	if json.Unmarshal(body, &ack) != nil {
		return false
	}
	return ack.ActionCode == 0 && ack.ErrCode == 0 && ack.NextCode != callbackstruct.Next
}

// CloseOwned is only for a source-owned adapter after its worker has joined.
func (e *Endpoint) CloseOwned() {
	if e != nil {
		for i := range e.secret {
			e.secret[i] = 0
		}
		e.secret = nil
	}
}
