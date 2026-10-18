//go:build linux

package ownedcontrol

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"sync"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
)

type OwnedWriter struct {
	Role, Deployment                                   string
	BinarySHA256, SourceSHA256, BootstrapSHA256        string
	RuntimeEpoch, RuntimeActivationSHA256, RuntimeMode string
	Phase                                              string
}
type MongoExclusion struct {
	Identity      namespaceimport.CredentialIdentity
	Admin, Legacy *mongo.Client
	Credential    MongoLegacyCredential
}
type RedisExclusion struct {
	Identity              namespaceimport.CredentialIdentity
	Admin, Legacy         redis.UniversalClient
	AdminUser, LegacyUser string
}
type KafkaExclusion struct {
	Identity   namespaceimport.CredentialIdentity
	Credential KafkaLegacyCredential
}

// OwnedAgent is executable only for the complete declared source-owned graph.
// There is no replace-with-command/self-signed-Ready callback. Old unmanaged
// runtime processes must first obtain real owner integration or remain fenced.
type HeldMetadataBootstrap interface {
	PrepareHeldMetadata(context.Context, *AdmissionHold, namespaceimport.Header) error
}
type OwnedAgent struct {
	mu               sync.Mutex
	header           namespaceimport.Header
	launcher         *Launcher
	writers          []OwnedWriter
	kafka            *KafkaProbe
	mongo            []MongoExclusion
	redis            []RedisExclusion
	kafkaCredentials []KafkaExclusion
	prepared         bool
	metadata         HeldMetadataBootstrap
	keyID            string
	key              ed25519.PrivateKey
	hold             *AdmissionHold
	generation       uint64
	joined           []namespaceimport.JoinedWriter
	revoked          []namespaceimport.RevokedCredential
}

func NewOwnedAgent(header namespaceimport.Header, launcher *Launcher, writers []OwnedWriter, kafka *KafkaProbe, mongoUsers []MongoExclusion, redisUsers []RedisExclusion, kafkaUsers []KafkaExclusion, metadata HeldMetadataBootstrap, keyID string, key ed25519.PrivateKey) (*OwnedAgent, error) {
	if header.Validate() != nil || launcher == nil || launcher.header != header.SHA256() || kafka == nil || kafka.cluster != header.KafkaClusterID || len(writers) == 0 || len(mongoUsers) == 0 || len(redisUsers) == 0 || len(kafkaUsers) == 0 || metadata == nil || keyID == "" || len(key) != ed25519.PrivateKeySize {
		return nil, ErrOwnedEvidence
	}
	seen := map[string]bool{}
	phases := map[string]bool{}
	for _, w := range writers {
		if w.Role == "" || w.Deployment == "" || seen[w.Role] || !isSHA(w.BinarySHA256) || !isSHA(w.SourceSHA256) || !isSHA(w.BootstrapSHA256) || !validWriterRuntime(w) || w.Phase != "producer" && w.Phase != "consumer" {
			return nil, ErrOwnedEvidence
		}
		seen[w.Role] = true
		phases[w.Phase] = true
	}
	if len(phases) != 2 {
		return nil, ErrOwnedEvidence
	}
	credentialIDs := map[namespaceimport.CredentialIdentity]bool{}
	for _, v := range mongoUsers {
		if v.Identity.Scope != "mongo" || v.Identity.CredentialID != v.Credential.User || v.Identity.Version != v.Credential.UserIDHash || credentialIDs[v.Identity] {
			return nil, ErrOwnedEvidence
		}
		credentialIDs[v.Identity] = true
	}
	for _, v := range redisUsers {
		if v.Identity.Scope != "redis" || v.Identity.CredentialID != v.LegacyUser || !isSHA(v.Identity.Version) || credentialIDs[v.Identity] {
			return nil, ErrOwnedEvidence
		}
		credentialIDs[v.Identity] = true
	}
	for _, v := range kafkaUsers {
		if v.Identity.Scope != "kafka" || v.Identity.CredentialID != v.Credential.User || v.Identity.Version != v.Credential.PrincipalVersion || credentialIDs[v.Identity] {
			return nil, ErrOwnedEvidence
		}
		credentialIDs[v.Identity] = true
	}
	return &OwnedAgent{header: header, launcher: launcher, writers: append([]OwnedWriter(nil), writers...), kafka: kafka, mongo: append([]MongoExclusion(nil), mongoUsers...), redis: append([]RedisExclusion(nil), redisUsers...), kafkaCredentials: append([]KafkaExclusion(nil), kafkaUsers...), metadata: metadata, keyID: keyID, key: append(ed25519.PrivateKey(nil), key...)}, nil
}
func (a *OwnedAgent) services(ctx context.Context) (map[string]*OwnedService, error) {
	rows, e := a.hold.Services()
	if e != nil || len(rows) != len(a.writers) {
		return nil, ErrOwnedEvidence
	}
	byRole := map[string]*OwnedService{}
	for _, s := range rows {
		byRole[s.owner.Role] = s
	}
	for _, w := range a.writers {
		s := byRole[w.Role]
		if s == nil || s.owner.BinarySHA256 != w.BinarySHA256 || s.owner.SourceSHA256 != w.SourceSHA256 || s.owner.BootstrapSHA256 != w.BootstrapSHA256 {
			return nil, ErrOwnedEvidence
		}
		if _, _, e = s.AwaitAnnouncement(ctx); e != nil {
			return nil, e
		}
		v := s.announcement
		if v.DatabaseSHA256 != sha([]byte(a.header.Database)) || v.Epoch != w.RuntimeEpoch || v.ActivationSHA256 != w.RuntimeActivationSHA256 {
			return nil, ErrOwnedEvidence
		}
		if s.serverAnnouncement != nil {
			if s.serverAnnouncement.Mode != w.RuntimeMode {
				return nil, ErrOwnedEvidence
			}
		} else if w.Role != "msgtransfer-managed" || w.RuntimeMode != "required" {
			return nil, ErrOwnedEvidence
		}
	}
	return byRole, nil
}
func (a *OwnedAgent) stopPhase(ctx context.Context, phase string, services map[string]*OwnedService) error {
	for _, w := range a.writers {
		if w.Phase != phase {
			continue
		}
		s := services[w.Role]
		join, e := s.StopAndJoin(ctx)
		if e != nil {
			return e
		}
		identity := namespaceimport.WriterIdentity{Deployment: w.Deployment, Incarnation: join.RuntimeInstanceID, BinarySHA256: w.BinarySHA256, SourceSHA256: w.SourceSHA256}
		found := false
		for _, old := range a.joined {
			if old.Identity == identity {
				found = true
			}
		}
		if !found {
			a.joined = append(a.joined, namespaceimport.JoinedWriter{Identity: identity, StopRecord: "owned-stop:" + join.AnnouncementSHA256, JoinRecord: "owned-join:" + join.DrainSHA256, ProcessExitAt: join.ExitedAt})
		}
	}
	return nil
}
func (a *OwnedAgent) exclude(ctx context.Context, apply bool) error {
	revoked := []namespaceimport.RevokedCredential{}
	for _, v := range a.mongo {
		if a.hold.Validate() != nil {
			return ErrOwnedEvidence
		}
		r, e := RevokeOwnedMongoCredential(ctx, v.Admin, v.Legacy, v.Credential, a.header.Database)
		if e != nil {
			return e
		}
		body, _ := json.Marshal(r)
		revoked = append(revoked, namespaceimport.RevokedCredential{Identity: v.Identity, RevocationRecord: "mongo-live:" + sha(body), RevocationGeneration: 1, State: "revoked"})
	}
	for _, v := range a.redis {
		if a.hold.Validate() != nil {
			return ErrOwnedEvidence
		}
		r, e := RevokeOwnedRedisCredential(ctx, v.Admin, v.Legacy, v.AdminUser, v.LegacyUser)
		if e != nil {
			return e
		}
		body, _ := json.Marshal(r)
		revoked = append(revoked, namespaceimport.RevokedCredential{Identity: v.Identity, RevocationRecord: "redis-live:" + sha(body), RevocationGeneration: 1, State: "revoked"})
	}
	for _, v := range a.kafkaCredentials {
		r, e := a.kafka.ExcludeLegacy(ctx, a.hold, v.Credential, apply)
		if e != nil {
			return e
		}
		body, _ := json.Marshal(r)
		revoked = append(revoked, namespaceimport.RevokedCredential{Identity: v.Identity, RevocationRecord: "kafka-live:" + sha(body), RevocationGeneration: 1, State: "revoked"})
	}
	a.revoked = revoked
	return nil
}
func (a *OwnedAgent) Acquire(ctx context.Context, request namespaceimport.ProbeRequest) (namespaceimport.FenceObservation, error) {
	if a == nil || ctx == nil {
		return namespaceimport.FenceObservation{}, ErrOwnedEvidence
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current, stop := context.WithTimeout(ctx, 55*time.Second)
	defer stop()
	if request.Header != a.header || request.Nonce == "" || request.LeaseID != "" {
		return namespaceimport.FenceObservation{}, ErrOwnedEvidence
	}
	if e := a.checkActualTargets(current); e != nil {
		return namespaceimport.FenceObservation{}, e
	}
	hold, e := a.launcher.Hold(current)
	if e != nil {
		return namespaceimport.FenceObservation{}, e
	}
	if a.hold != nil && a.hold.sha != hold.sha {
		return namespaceimport.FenceObservation{}, ErrOwnedEvidence
	}
	a.hold = hold
	services, e := a.services(current)
	if e != nil {
		return namespaceimport.FenceObservation{}, e
	}
	// Producers join BEFORE offsets are inspected. Consumers remain alive until
	// both real queue planes reach stable broker end == committed NEXT.
	if e = a.stopPhase(current, "producer", services); e != nil {
		return namespaceimport.FenceObservation{}, e
	}
	if _, e = a.kafka.ObserveDrained(current, false, 250*time.Millisecond); e != nil {
		return namespaceimport.FenceObservation{}, e
	}
	if e = a.stopPhase(current, "consumer", services); e != nil {
		return namespaceimport.FenceObservation{}, e
	}
	if e = a.exclude(current, true); e != nil {
		return namespaceimport.FenceObservation{}, e
	}
	drained, e := a.kafka.ObserveDrained(current, true, 250*time.Millisecond)
	if e != nil {
		return namespaceimport.FenceObservation{}, e
	}
	proof, _ := json.Marshal(struct {
		Joined  []namespaceimport.JoinedWriter
		Revoked []namespaceimport.RevokedCredential
		Drained []namespaceimport.DrainedPartition
	}{a.joined, a.revoked, drained})
	a.hold.exclusionSHA = sha(proof)
	a.hold.exclusionAt = time.Now().UTC()
	if e = a.metadata.PrepareHeldMetadata(current, a.hold, a.header); e != nil {
		return namespaceimport.FenceObservation{}, e
	}
	a.prepared = true
	return a.observe(current, request)
}
func (a *OwnedAgent) Observe(ctx context.Context, request namespaceimport.ProbeRequest) (namespaceimport.FenceObservation, error) {
	if a == nil || ctx == nil {
		return namespaceimport.FenceObservation{}, ErrOwnedEvidence
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current, stop := context.WithTimeout(ctx, 55*time.Second)
	defer stop()
	if a.hold == nil || request.Header != a.header || request.LeaseID != a.hold.record.LeaseID {
		return namespaceimport.FenceObservation{}, ErrOwnedEvidence
	}
	if e := a.checkActualTargets(current); e != nil {
		return namespaceimport.FenceObservation{}, e
	}
	if e := a.exclude(current, false); e != nil {
		return namespaceimport.FenceObservation{}, e
	}
	return a.observe(current, request)
}
func (a *OwnedAgent) observe(ctx context.Context, request namespaceimport.ProbeRequest) (namespaceimport.FenceObservation, error) {
	if a.hold.Validate() != nil || len(a.joined) != len(a.writers) || a.generation == ^uint64(0) {
		return namespaceimport.FenceObservation{}, ErrOwnedEvidence
	}
	for _, s := range a.launcher.children {
		select {
		case <-s.done:
			if !s.joined {
				return namespaceimport.FenceObservation{}, ErrOwnedEvidence
			}
		default:
			return namespaceimport.FenceObservation{}, ErrOwnedEvidence
		}
	}
	drained, e := a.kafka.ObserveDrained(ctx, true, 250*time.Millisecond)
	if e != nil {
		return namespaceimport.FenceObservation{}, e
	}
	// Restore explicit writer policy order, independent of stop-phase order.
	joined := make([]namespaceimport.JoinedWriter, 0, len(a.writers))
	for _, w := range a.writers {
		for _, r := range a.joined {
			if r.Identity.Deployment == w.Deployment {
				joined = append(joined, r)
			}
		}
	}
	if len(joined) != len(a.writers) {
		return namespaceimport.FenceObservation{}, ErrOwnedEvidence
	}
	a.generation++
	if a.generation < request.MinimumGeneration {
		return namespaceimport.FenceObservation{}, ErrOwnedEvidence
	}
	now := time.Now().UTC()
	f := namespaceimport.FenceObservation{HeaderSHA256: a.header.SHA256(), Nonce: request.Nonce, LeaseID: a.hold.record.LeaseID, Generation: a.generation, KeyID: a.keyID, IssuedAt: now, ValidUntil: now.Add(45 * time.Second), ExclusionMode: "held-until-explicit-release", AdmissionGuardRecord: "owned-launcher:" + a.hold.sha, AdmissionGuardSHA256: a.hold.sha, Joined: joined, Revoked: append([]namespaceimport.RevokedCredential(nil), a.revoked...), Drained: drained}
	body, e := json.Marshal(f)
	if e != nil {
		return namespaceimport.FenceObservation{}, e
	}
	f.Signature = ed25519.Sign(a.key, body)
	return f, nil
}

func validWriterRuntime(w OwnedWriter) bool {
	if w.Role == "msgtransfer-managed" {
		return w.RuntimeMode == "required" && runtimeMode(w.RuntimeMode, w.RuntimeEpoch, w.RuntimeActivationSHA256)
	}
	return serverRole(w.Role) && runtimeMode(w.RuntimeMode, w.RuntimeEpoch, w.RuntimeActivationSHA256)
}
