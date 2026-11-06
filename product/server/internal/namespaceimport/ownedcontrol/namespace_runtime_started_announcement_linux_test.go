//go:build linux

package ownedcontrol

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

// The retained original child and owner come from the existing local lifecycle
// fixture. No database, service graph, admission, signal or Ready claim is made.
func TestNamespaceRuntimeStartedAnnouncementPreservesOriginalContextAndStrictOwner(t *testing.T) {
	for _, kind := range []string{"deadline", "cancelled"} {
		t.Run(kind+"-returns-original-error-and-retains-owner", func(t *testing.T) {
			child, _, signal := cleanupSignalChild(t, 0)
			original := child.owner
			var ctx context.Context
			var cancel context.CancelFunc
			if kind == "deadline" {
				ctx, cancel = context.WithTimeout(context.Background(), 35*time.Millisecond)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
				cancel()
			}
			defer cancel()
			observation := NamespaceRuntimeCallObservation{}
			_, err := awaitNamespaceRuntimeStartedAnnouncement(ctx, NamespaceRuntimeProcess{Service: child}, ObservedNamespaceRuntime{}, NamespaceRuntimeRequirement{Role: original.Role}, namespaceimport.Header{}, &observation)
			if err != ctx.Err() || errors.Is(err, ErrOwnedEvidence) || child.owner != original || child.joined || observation.Role != original.Role || observation.Predicate != "started-announcement-await-context" {
				t.Fatal("original context or retained owner/observation changed", err, observation)
			}
			select {
			case <-child.done:
				t.Fatal("announcement wait stopped the original child")
			default:
			}
			if _, err := os.Stat(signal); !os.IsNotExist(err) {
				t.Fatal("announcement wait signaled child", err)
			}
		})
	}
	t.Run("actual-announcement-keeps-identity-and-server-mode-fences", func(t *testing.T) {
		child, _, _ := cleanupSignalChild(t, 0)
		child.owner.BootstrapSHA256 = strings.Repeat("b", 64)
		h := namespaceimport.Header{Database: "synthetic-started-owner", Epoch: "11111111-1111-4111-8111-111111111111", ActivationSHA256: strings.Repeat("a", 64)}
		ann := ServerRuntimeAnnouncement{Version: "owned-server-owner-ready-v1", Role: child.owner.Role, Mode: "required", InstanceID: "22222222-2222-4222-8222-222222222222", DatabaseSHA256: sha([]byte(h.Database)), Epoch: h.Epoch, ActivationSHA256: h.ActivationSHA256, BootstrapSHA256: child.owner.BootstrapSHA256, PolicySHA256: strings.Repeat("c", 64), DrainPath: filepath.Join(filepath.Dir(child.announcementPath), "drain.json"), OwnedStopStepNames: []string{"synthetic-retained-owner"}}
		pin, err := WriteServerRuntimeAnnouncement(child.announcementPath, child.owner.OwnerNonce, ann)
		if err != nil {
			t.Fatal(err)
		}
		row := ObservedNamespaceRuntime{Role: child.owner.Role, PID: child.owner.PID, StartToken: child.owner.StartToken, BootID: child.owner.BootID, OwnerNonceSHA256: sha([]byte(child.owner.OwnerNonce)), Registration: NamespaceRuntimeRegistration{InstanceID: ann.InstanceID}}
		requirement := NamespaceRuntimeRequirement{Role: child.owner.Role, BootstrapSHA256: ann.BootstrapSHA256}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		observation := NamespaceRuntimeCallObservation{}
		actual, err := awaitNamespaceRuntimeStartedAnnouncement(ctx, NamespaceRuntimeProcess{Service: child}, row, requirement, h, &observation)
		if err != nil || actual != pin || observation.Predicate != "started-announcement-evidence" {
			t.Fatal("actual original announcement rejected", err, observation)
		}
		for _, kind := range []string{"pid", "start-token", "boot-id", "nonce", "instance", "database", "epoch", "activation", "bootstrap", "managed-with-server-announcement"} {
			changedRow, changedRequirement, changedHeader := row, requirement, h
			switch kind {
			case "pid":
				changedRow.PID++
			case "start-token":
				changedRow.StartToken += "changed"
			case "boot-id":
				changedRow.BootID += "changed"
			case "nonce":
				changedRow.OwnerNonceSHA256 = strings.Repeat("f", 64)
			case "instance":
				changedRow.Registration.InstanceID = "33333333-3333-4333-8333-333333333333"
			case "database":
				changedHeader.Database += "changed"
			case "epoch":
				changedHeader.Epoch = "33333333-3333-4333-8333-333333333333"
			case "activation":
				changedHeader.ActivationSHA256 = strings.Repeat("f", 64)
			case "bootstrap":
				changedRequirement.BootstrapSHA256 = strings.Repeat("f", 64)
			case "managed-with-server-announcement":
				changedRequirement.Role = "msgtransfer-managed"
			}
			if _, err := awaitNamespaceRuntimeStartedAnnouncement(ctx, NamespaceRuntimeProcess{Service: child}, changedRow, changedRequirement, changedHeader, &observation); !errors.Is(err, ErrOwnedEvidence) {
				t.Fatal("original identity/server-mode mismatch escaped", kind, err)
			}
		}
	})
}
