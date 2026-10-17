//go:build linux

package ownedcontrol

import (
	"context"
	"encoding/json"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

// RefreshExclusion rechecks actual target identities, old effective permissions,
// every original joined child and both stable broker queue planes. It renews
// only this held execution capability; it never changes a range's UTC deadline,
// coordinates, source policy, migration ID, raw cut or admission generation.
func (a *OwnedAgent) RefreshExclusion(ctx context.Context) (*AdmissionHold, error) {
	if a == nil || ctx == nil {
		return nil, ErrOwnedEvidence
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current, stop := context.WithTimeout(ctx, 55*time.Second)
	defer stop()
	if a.hold == nil || a.hold.Validate() != nil || !a.prepared || len(a.joined) != len(a.writers) {
		return nil, ErrOwnedEvidence
	}
	if e := a.checkActualTargets(current); e != nil {
		return nil, e
	}
	for _, s := range a.launcher.children {
		select {
		case <-s.done:
			if !s.joined {
				return nil, ErrOwnedEvidence
			}
		default:
			return nil, ErrOwnedEvidence
		}
	}
	if e := a.exclude(current, false); e != nil {
		return nil, e
	}
	drained, e := a.kafka.ObserveDrained(current, true, 250*time.Millisecond)
	if e != nil {
		return nil, e
	}
	proof, e := json.Marshal(struct {
		HeaderSHA, GuardSHA string
		Joined              []namespaceimport.JoinedWriter
		Revoked             []namespaceimport.RevokedCredential
		Drained             []namespaceimport.DrainedPartition
	}{a.header.SHA256(), a.hold.RecordSHA256(), a.joined, a.revoked, drained})
	if e != nil {
		return nil, e
	}
	a.hold.exclusionSHA = sha(proof)
	a.hold.exclusionAt = time.Now().UTC()
	return a.hold, nil
}
