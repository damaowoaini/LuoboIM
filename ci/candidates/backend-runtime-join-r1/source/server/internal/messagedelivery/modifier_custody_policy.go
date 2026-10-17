package messagedelivery

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	"go.mongodb.org/mongo-driver/mongo"
)

// This operator-owned file authorizes finite raw custody, NEVER modifier
// application, delivery, broker offset reset or a replacement source intent.
type UnsupportedCustodyRange struct {
	Encoding          string `json:"encoding"`
	Topic             string `json:"topic"`
	Partition         int32  `json:"partition"`
	Start             int64  `json:"start"`
	End               int64  `json:"end"`
	SourceProofSHA256 string `json:"sourceProofSHA256"`
	DeadlineUTC       string `json:"deadlineUTC"`
}
type UnsupportedCustodyPolicy struct {
	Version                    string                    `json:"version"`
	Database                   string                    `json:"database"`
	Epoch                      string                    `json:"epoch"`
	ActivationSHA256           string                    `json:"activationSHA256"`
	Header                     namespaceimport.Header    `json:"header"`
	HeaderSHA256               string                    `json:"headerSHA256"`
	AdmissionGeneration        int64                     `json:"admissionGeneration"`
	ClusterConfigurationSHA256 string                    `json:"clusterConfigurationSHA256"`
	ConsumerGroup              string                    `json:"consumerGroup"`
	Ranges                     []UnsupportedCustodyRange `json:"ranges"`
}
type OwnedUnsupportedKafkaCustody struct {
	store               *Store
	policy              UnsupportedCustodyPolicy
	inputPath, inputSHA string
}

func custodyDeadline(value string) (time.Time, bool) {
	t, e := time.Parse(time.RFC3339Nano, value)
	return t, e == nil && strings.HasSuffix(value, "Z") && t.Location() == time.UTC && t.Format(time.RFC3339Nano) == value && !t.IsZero()
}
func (p UnsupportedCustodyPolicy) valid(database, epoch, activation, cluster, group string) bool {
	if p.Version != "owned-unsupported-modifier-custody-v1" || p.Database != database || p.Epoch != epoch || p.ActivationSHA256 != activation || p.ClusterConfigurationSHA256 != cluster || p.ConsumerGroup != group || p.Header.Validate() != nil || p.Header.Database != database || p.Header.Epoch != epoch || p.Header.ActivationSHA256 != activation || p.Header.SHA256() != p.HeaderSHA256 || !validSHA(p.HeaderSHA256) || p.AdmissionGeneration < 1 || p.AdmissionGeneration >= math.MaxInt64-1 || !validUUID(epoch) || !validSHA(activation) || !validSHA(cluster) || !identity(group) || len(p.Ranges) < 1 || len(p.Ranges) > 256 {
		return false
	}
	for i, r := range p.Ranges {
		if r.Encoding != OrdinaryModifierEncoding && r.Encoding != LegacyModifierBatchEncoding || !identity(r.Topic) || len(r.Topic) > 249 || r.Partition < 0 || r.Start < 0 || r.End <= r.Start || !validSHA(r.SourceProofSHA256) {
			return false
		}
		if deadline, ok := custodyDeadline(r.DeadlineUTC); !ok || !deadline.Equal(deadline.Truncate(time.Millisecond)) {
			return false
		}
		for _, before := range p.Ranges[:i] {
			if before.Topic == r.Topic && before.Partition == r.Partition && before.Start < r.End && r.Start < before.End {
				return false
			}
		}
	}
	return true
}
func LoadOwnedUnsupportedKafkaCustody(ctx context.Context, db *mongo.Database, epoch, activation, cluster, group, path, pin string) (*OwnedUnsupportedKafkaCustody, error) {
	if ctx == nil || ctx.Err() != nil || db == nil || !filepath.IsAbs(path) || filepath.Clean(path) != path || !validSHA(pin) {
		return nil, ErrUnprepared
	}
	raw, e := namespaceinventory.ReadPrivateDocument(path, 64<<10)
	if e != nil || hash(raw) != pin {
		return nil, ErrUnprepared
	}
	var p UnsupportedCustodyPolicy
	if namespaceinventory.DecodeStrict(raw, &p) != nil || !p.valid(db.Name(), epoch, activation, cluster, group) {
		return nil, ErrUnprepared
	}
	var keys map[string]json.RawMessage
	if json.Unmarshal(raw, &keys) != nil || len(keys) != 10 {
		return nil, ErrUnprepared
	}
	s, e := NewMembershipBoundStore(db, epoch, activation)
	if e != nil {
		return nil, e
	}
	return &OwnedUnsupportedKafkaCustody{store: s, policy: p, inputPath: path, inputSHA: pin}, nil
}
func (c *OwnedUnsupportedKafkaCustody) policyCurrent() error {
	if c == nil || c.store == nil {
		return ErrUnprepared
	}
	raw, e := namespaceinventory.ReadPrivateDocument(c.inputPath, 64<<10)
	if e != nil || hash(raw) != c.inputSHA {
		return ErrUnprepared
	}
	return nil
}
func (c *OwnedUnsupportedKafkaCustody) sourceRange(r KafkaRecord, now time.Time) (UnsupportedCustodyRange, error) {
	if e := c.policyCurrent(); e != nil {
		return UnsupportedCustodyRange{}, e
	}
	for _, scope := range c.policy.Ranges {
		if scope.Topic == r.Topic && scope.Partition == r.Partition && scope.Start <= r.Offset && r.Offset < scope.End {
			deadline, ok := custodyDeadline(scope.DeadlineUTC)
			if !ok || !now.Before(deadline) {
				return UnsupportedCustodyRange{}, ErrUnsupportedCustodyExpired
			}
			return scope, nil
		}
	}
	return UnsupportedCustodyRange{}, ErrUnsupportedCustodyScope
}
