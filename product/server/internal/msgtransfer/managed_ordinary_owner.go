package msgtransfer

import (
	"context"
	"encoding/hex"
	"os"

	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

const ManagedOwnerAnnouncementPathEnv = "LUOBO_MANAGED_OWNER_ANNOUNCEMENT_PATH"
const ManagedOwnerNonceEnv = "LUOBO_MANAGED_OWNER_NONCE"

// Optional only when BOTH values are absent, for the original isolated runtime
// constructor contract. The owned supervisor always supplies the pair. Neither
// this path nor the nonce is an HTTP/RPC permission or an external Ready flag.
func LoadManagedOwnerAnnouncement() (path, nonce string, err error) {
	path, nonce = os.Getenv(ManagedOwnerAnnouncementPathEnv), os.Getenv(ManagedOwnerNonceEnv)
	if path == "" && nonce == "" {
		return "", "", nil
	}
	decoded, e := hex.DecodeString(nonce)
	if e != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != nonce || !ordinaryCanonicalPath(path) || namespaceinventory.CheckFreshPrivatePath(path) != nil {
		return "", "", messagedelivery.ErrUnprepared
	}
	return path, nonce, nil
}

// No process identity is self-attested here. The source-owned supervisor must
// independently bind this announcement to live PID/start/executable/source and
// its private nonce. Step names describe OWNED resources, not completed joins.
func (r *ManagedOrdinaryRuntime) OwnerAnnouncement(ctx context.Context) (ownedcontrol.RuntimeAnnouncement, error) {
	if r == nil || ctx == nil || r.stopLock == nil {
		return ownedcontrol.RuntimeAnnouncement{}, messagedelivery.ErrInvalid
	}
	select {
	case <-ctx.Done():
		return ownedcontrol.RuntimeAnnouncement{}, ctx.Err()
	case <-r.stopLock:
	}
	defer func() { r.stopLock <- struct{}{} }()
	if !r.intakeStarted || r.intake == nil || r.offline == nil || r.complete || r.completed != 0 || r.instance == "" || !r.bootstrap.valid(r.bootstrap.Database) || !groupStartupSHA(r.bootstrap.inputSHA) {
		return ownedcontrol.RuntimeAnnouncement{}, messagedelivery.ErrUnprepared
	}
	steps := make([]string, len(r.steps))
	for i, step := range r.steps {
		steps[i] = step.name
	}
	return ownedcontrol.RuntimeAnnouncement{Version: "managed-ordinary-owner-ready-v1", InstanceID: r.instance, DatabaseSHA256: deliveryprofile.SHA([]byte(r.bootstrap.Database)), Epoch: r.bootstrap.Epoch, ActivationSHA256: r.bootstrap.ActivationSHA256, BootstrapSHA256: r.bootstrap.inputSHA, PolicySHA256: r.bootstrap.PolicySHA256, DrainPath: r.bootstrap.DrainPath, OwnedStopStepNames: steps}, nil
}
