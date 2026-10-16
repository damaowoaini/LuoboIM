package push

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"sort"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

type ownedRoleCallbackEntry struct {
	Profile groupeffects.OwnedRoleAfterProfile `json:"profile"`
	Base    string                             `json:"base"`
	Secret  string                             `json:"secret"`
	Policy  config.CallBackConfig              `json:"policy"`
}
type ownedRoleLegacyBinding struct {
	EffectID            string `json:"effectID"`
	SourceSHA256        string `json:"sourceSHA256"`
	TransportSHA256     string `json:"transportSHA256"`
	EvidenceSHA256      string `json:"evidenceSHA256"`
	DrainEvidenceSHA256 string `json:"drainEvidenceSHA256"`
}
type ownedRoleCallbackRegistry struct {
	Version  string                   `json:"version"`
	Database string                   `json:"database"`
	Epoch    string                   `json:"epoch"`
	Entries  []ownedRoleCallbackEntry `json:"entries"`
	Legacy   []ownedRoleLegacyBinding `json:"legacy"`
}
type ownedRoleEndpoint struct {
	profile  groupeffects.OwnedRoleAfterProfile
	endpoint *groupeffects.Endpoint
}
type FrozenOwnedGroupRoleCallbacks struct {
	entries map[string]ownedRoleEndpoint
	legacy  map[string]ownedRoleLegacyBinding
}

func LoadOwnedGroupRoleCallbacks(ctx context.Context, input OwnedKafkaCallbackFile, database, epoch string) (*FrozenOwnedGroupRoleCallbacks, error) {
	if ctx == nil || ctx.Err() != nil || database == "" {
		return nil, ErrOwnedCallbackFile
	}
	engine := &FrozenOwnedGroupRoleCallbacks{entries: map[string]ownedRoleEndpoint{}, legacy: map[string]ownedRoleLegacyBinding{}}
	fail := func() (*FrozenOwnedGroupRoleCallbacks, error) { _ = engine.Close(); return nil, ErrOwnedCallbackFile }
	var registry ownedRoleCallbackRegistry
	if input.Path != "" || input.ExpectedSHA256 != "" {
		raw, err := readOwnedCallbackFile(input)
		if err != nil {
			return fail()
		}
		defer func() {
			for i := range raw {
				raw[i] = 0
			}
		}()
		if !callbackExactKeys(raw, "version", "database", "epoch", "entries", "legacy") {
			return fail()
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&registry) != nil || decoder.Decode(new(any)) != io.EOF || registry.Version != "owned-group-role-callbacks-v1" || registry.Database != database || registry.Epoch != epoch {
			return fail()
		}
	}
	for _, entry := range registry.Entries {
		profile, err := groupeffects.FreezeOwnedRoleAfter(database, epoch, entry.Profile.Name, entry.Base, entry.Secret, entry.Policy)
		if err != nil || profile != entry.Profile || engine.entries[profile.TransportSHA256].endpoint != nil {
			return fail()
		}
		timeout := 15
		seconds := entry.Policy.CallbackTimeOut
		if seconds > 0 && seconds < 15 {
			timeout = seconds
		}
		endpoint, err := groupeffects.NewEndpoint(entry.Base, database, profile.Name, []byte(entry.Secret), time.Duration(timeout)*time.Second)
		if err != nil {
			return fail()
		}
		engine.entries[profile.TransportSHA256] = ownedRoleEndpoint{profile: profile, endpoint: endpoint}
	}
	// New source callbacks cannot enter the queue until their current exact key
	// channel is loaded. Retained unknowns are never guessed from this map.
	for name, conf := range map[groupeffects.CallbackName]config.CallBackConfig{groupeffects.AfterSetMember: config.Config.Callback.CallbackAfterSetGroupMemberInfo, groupeffects.AfterTransfer: config.Config.Callback.CallbackTransferGroupOwnerAfter} {
		if !conf.Enable {
			continue
		}
		profile, err := groupeffects.FreezeOwnedRoleAfter(database, epoch, name, config.Config.Callback.CallbackUrl, config.Config.Secret, conf)
		if err != nil || engine.entries[profile.TransportSHA256].endpoint == nil {
			return fail()
		}
	}
	for _, binding := range registry.Legacy {
		if !callbackUUID(binding.EffectID) || !callbackSHA(binding.SourceSHA256) || !callbackSHA(binding.EvidenceSHA256) || !callbackSHA(binding.DrainEvidenceSHA256) || engine.entries[binding.TransportSHA256].endpoint == nil || engine.legacy[binding.EffectID].EffectID != "" {
			return fail()
		}
		engine.legacy[binding.EffectID] = binding
	}
	return engine, nil
}
func (e *FrozenOwnedGroupRoleCallbacks) CheckReady(required []string) error {
	if e == nil {
		return ErrOwnedCallbackFile
	}
	for _, sha := range required {
		if e.entries[sha].endpoint == nil {
			return ErrOwnedCallbackFile
		}
	}
	return nil
}
func (e *FrozenOwnedGroupRoleCallbacks) Fingerprints() []string {
	if e == nil {
		return nil
	}
	out := make([]string, 0, len(e.entries))
	for sha := range e.entries {
		out = append(out, sha)
	}
	sort.Strings(out)
	return out
}
func (e *FrozenOwnedGroupRoleCallbacks) Close() error {
	if e != nil {
		for sha, entry := range e.entries {
			entry.endpoint.CloseOwned()
			delete(e.entries, sha)
		}
		for id := range e.legacy {
			delete(e.legacy, id)
		}
	}
	return nil
}
func (e *FrozenOwnedGroupRoleCallbacks) Recover(ctx context.Context, store *groupeffects.Store, ref groupeffects.OwnedRoleCallbackRef) error {
	if e == nil || store == nil || ctx == nil {
		return ErrOwnedCallbackFile
	}
	if ref.Legacy {
		binding, ok := e.legacy[ref.ID]
		if !ok {
			if err := store.QuarantineOwnedRoleCallback(ctx, ref); err != nil {
				return err
			}
			return groupeffects.ErrLegacyOwnedQuarantined
		}
		if binding.SourceSHA256 != ref.SourceSHA256 {
			return ErrOwnedCallbackFile
		}
		entry := e.entries[binding.TransportSHA256]
		if err := store.BindOwnedRoleCallback(ctx, ref, entry.profile, binding.EvidenceSHA256, binding.DrainEvidenceSHA256); err != nil {
			return err
		}
		var err error
		ref, err = store.InspectOwnedRoleCallback(ctx, ref.ID)
		if err != nil {
			return err
		}
	}
	entry := e.entries[ref.TransportSHA256]
	if entry.endpoint == nil {
		return ErrOwnedCallbackFile
	}
	return store.DeliverOwnedRoleCallback(ctx, ref, entry.profile, entry.endpoint)
}

func callbackUUID(v string) bool {
	u, e := uuid.Parse(v)
	return e == nil && u != uuid.Nil && u.String() == v
}
func callbackSHA(v string) bool {
	if len(v) != 64 {
		return false
	}
	for _, r := range v {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
