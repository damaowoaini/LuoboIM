package push

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"os"
	"path/filepath"
	"testing"
)

func TestOwnedBusinessAfterRegistryRequiresCurrentAndRetainedSigningChannels(t *testing.T) {
	saved, secret := config.Config.Callback, config.Config.Secret
	t.Cleanup(func() { config.Config.Callback = saved; config.Config.Secret = secret })
	config.Config.Callback.CallbackAfterSetGroupMemberInfo = config.CallBackConfig{}
	config.Config.Callback.CallbackTransferGroupOwnerAfter = config.CallBackConfig{}
	epoch, db := uuid.NewString(), "synthetic_owned_role_callbacks"
	empty, err := LoadOwnedGroupRoleCallbacks(context.Background(), OwnedKafkaCallbackFile{}, db, epoch)
	if err != nil || empty.CheckReady(nil) != nil {
		t.Fatal("disabled callback did not allow an exact empty registry", err)
	}
	_ = empty.Close()
	conf := config.CallBackConfig{Enable: true, CallbackTimeOut: 0}
	config.Config.Callback.CallbackAfterSetGroupMemberInfo = conf
	config.Config.Callback.CallbackUrl = "https://synthetic.invalid"
	config.Config.Secret = "synthetic-current-channel"
	if engine, err := LoadOwnedGroupRoleCallbacks(context.Background(), OwnedKafkaCallbackFile{}, db, epoch); err == nil {
		_ = engine.Close()
		t.Fatal("enabled current After lost its required private channel")
	}
	current, err := groupeffects.FreezeOwnedRoleAfter(db, epoch, groupeffects.AfterSetMember, config.Config.Callback.CallbackUrl, config.Config.Secret, conf)
	if err != nil {
		t.Fatal(err)
	}
	old, err := groupeffects.FreezeOwnedRoleAfter(db, epoch, groupeffects.AfterSetMember, config.Config.Callback.CallbackUrl, "synthetic-retained-channel", conf)
	if err != nil || old.ConfigurationSHA256 != current.ConfigurationSHA256 || old.TransportSHA256 == current.TransportSHA256 {
		t.Fatal("key rotation remained ambiguous", err)
	}
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("private root unavailable")
	}
	registry := ownedRoleCallbackRegistry{Version: "owned-group-role-callbacks-v1", Database: db, Epoch: epoch, Entries: []ownedRoleCallbackEntry{{Profile: current, Base: config.Config.Callback.CallbackUrl, Secret: config.Config.Secret, Policy: conf}, {Profile: old, Base: config.Config.Callback.CallbackUrl, Secret: "synthetic-retained-channel", Policy: conf}}, Legacy: []ownedRoleLegacyBinding{}}
	raw, _ := json.Marshal(registry)
	path := filepath.Join(dir, "callbacks.json")
	if os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("private registry unavailable")
	}
	engine, err := LoadOwnedGroupRoleCallbacks(context.Background(), OwnedKafkaCallbackFile{Path: path, ExpectedSHA256: digestGateway(raw)}, db, epoch)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if engine.CheckReady([]string{current.TransportSHA256, old.TransportSHA256}) != nil || len(engine.Fingerprints()) != 2 {
		t.Fatal("retained channel was replaced by current config")
	}
	if engine.CheckReady([]string{digestGateway([]byte("missing retained channel"))}) == nil {
		t.Fatal("unavailable retained channel did not stop readiness")
	}
}
