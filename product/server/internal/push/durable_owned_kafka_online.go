package push

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

type ownedKafkaOnlineSpec struct {
	PolicySHA256 string                `json:"policySHA256"`
	Base         string                `json:"base"`
	Secret       string                `json:"secret"`
	SuperGroup   config.CallBackConfig `json:"superGroup"`
	Online       config.CallBackConfig `json:"online"`
}

type ownedKafkaOnlineRegistry struct {
	Version    string                 `json:"version"`
	Database   string                 `json:"database"`
	Epoch      string                 `json:"epoch"`
	Activation string                 `json:"activationSHA256"`
	Entries    []ownedKafkaOnlineSpec `json:"entries"`
}

// Current AND retained policies use the existing exact callback binding,
// durable attempt, response and unknown-replay rules. No current-config
// substitution is allowed for an older pending policy.
func LoadOwnedKafkaOnlineCallbacks(ctx context.Context, input OwnedKafkaCallbackFile, store *messagedelivery.Store, database, epoch, activation string, policies []messagedelivery.KafkaDeliveryPolicy) (map[string]*FrozenKafkaPushCallbacks, error) {
	if ctx == nil || ctx.Err() != nil || store == nil || database == "" || len(policies) == 0 {
		return nil, ErrOwnedCallbackFile
	}
	required := map[string]messagedelivery.KafkaDeliveryPolicy{}
	seen := map[string]bool{}
	for _, p := range policies {
		sha, err := p.Fingerprint()
		if err != nil || p.Epoch != epoch || p.ActivationSHA256 != activation || seen[sha] {
			return nil, ErrOwnedCallbackFile
		}
		seen[sha] = true
		if p.BeforeGroupPushBinding != "" || p.OnlinePushBinding != "" {
			if _, found := required[sha]; found {
				return nil, ErrOwnedCallbackFile
			}
			required[sha] = p
		}
	}
	if len(required) == 0 {
		if input.Path != "" || input.ExpectedSHA256 != "" {
			return nil, ErrOwnedCallbackFile
		}
		return map[string]*FrozenKafkaPushCallbacks{}, nil
	}
	raw, err := readOwnedCallbackFile(input)
	if err != nil {
		return nil, ErrOwnedCallbackFile
	}
	defer func() {
		for i := range raw {
			raw[i] = 0
		}
	}()
	if !offlineUniqueJSON(raw) || !callbackExactKeys(raw, "version", "database", "epoch", "activationSHA256", "entries") {
		return nil, ErrOwnedCallbackFile
	}
	var roots map[string]json.RawMessage
	var entries []json.RawMessage
	if json.Unmarshal(raw, &roots) != nil || json.Unmarshal(roots["entries"], &entries) != nil {
		return nil, ErrOwnedCallbackFile
	}
	for _, entry := range entries {
		if !callbackExactKeys(entry, "policySHA256", "base", "secret", "superGroup", "online") {
			return nil, ErrOwnedCallbackFile
		}
	}
	var r ownedKafkaOnlineRegistry
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil || d.Decode(new(any)) != io.EOF || r.Version != "ordinary-owned-online-callbacks-v1" || r.Database != database || r.Epoch != epoch || r.Activation != activation || len(r.Entries) != len(required) {
		return nil, ErrOwnedCallbackFile
	}
	out := map[string]*FrozenKafkaPushCallbacks{}
	complete := false
	defer func() {
		if !complete {
			for _, e := range out {
				_ = e.CloseOwned()
			}
		}
	}()
	for _, entry := range r.Entries {
		p, found := required[entry.PolicySHA256]
		if !found || out[entry.PolicySHA256] != nil {
			return nil, ErrOwnedCallbackFile
		}
		e, err := NewFrozenKafkaPushCallbacks(store, database, p, entry.Base, entry.Secret, entry.SuperGroup, entry.Online)
		if err != nil {
			return nil, ErrOwnedCallbackFile
		}
		out[entry.PolicySHA256] = e
	}
	complete = true
	return out, nil
}

// The owner calls this only after the SAME online worker has joined. No shared
// callback object is closed underneath an active delivery operation.
func (e *FrozenKafkaPushCallbacks) CloseOwned() error {
	if e != nil {
		for key, endpoint := range e.endpoints {
			for i := range endpoint.secret {
				endpoint.secret[i] = 0
			}
			delete(e.endpoints, key)
		}
	}
	return nil
}
