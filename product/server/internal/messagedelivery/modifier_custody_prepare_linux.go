//go:build linux

package messagedelivery

import (
	"context"
	"errors"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// The same owned Agent first prepares the original namespace metadata, then
// explicitly prepares reviewed modifier custody. No policy means no invented
// deadline/generation/schema. This wrapper publishes no namespace Ready.
type ModifierCustodyMetadataBootstrap struct {
	Base    *NamespaceImportWriter
	Custody *OwnedUnsupportedKafkaCustody
}

func (b *ModifierCustodyMetadataBootstrap) PrepareHeldMetadata(ctx context.Context, hold *ownedcontrol.AdmissionHold, h namespaceimport.Header) error {
	if b == nil || b.Base == nil || ctx == nil || h.Validate() != nil {
		return ErrInvalid
	}
	if b.Custody != nil {
		p := b.Custody.policy
		if p.Header != h || p.HeaderSHA256 != h.SHA256() || p.Database != h.Database || p.Epoch != h.Epoch || p.ActivationSHA256 != h.ActivationSHA256 {
			return ErrConflict
		}
	}
	if e := b.Base.PrepareHeldMetadata(ctx, hold, h); e != nil {
		return e
	}
	if b.Custody == nil {
		return nil
	}
	return b.Custody.PrepareModifierCustody(ctx, hold, b.Custody.policy)
}

// Only a real same-Agent exclusion capability authorizes DDL and the first
// generation. Constructors/CheckReady/Start cannot call this entry. A known
// partially prepared catalog is inspected, not dropped/reset or re-seeded.
func (c *OwnedUnsupportedKafkaCustody) PrepareModifierCustody(ctx context.Context, hold *ownedcontrol.AdmissionHold, p UnsupportedCustodyPolicy) error {
	if c == nil || c.store == nil || ctx == nil || mongo.SessionFromContext(ctx) != nil || custodyDigest(p) != custodyDigest(c.policy) {
		return ErrInvalid
	}
	check := func() error {
		if hold == nil || hold.ValidateExclusion() != nil || hold.HeaderSHA256() != p.HeaderSHA256 || !validSHA(hold.LeaseID()) || !validSHA(hold.RecordSHA256()) {
			return ErrUnprepared
		}
		return c.policyCurrent()
	}
	if e := check(); e != nil {
		return e
	}
	current, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	for _, scope := range p.Ranges {
		deadline, ok := custodyDeadline(scope.DeadlineUTC)
		if !ok || !time.Now().UTC().Before(deadline) {
			return ErrUnsupportedCustodyExpired
		}
	}
	s := c.store
	target, e := NewNamespaceImportWriter(s, p.Header)
	if e != nil {
		return e
	}
	if e = target.CheckImportCatalogReady(current); e != nil {
		return e
	}
	rows, e := s.db.ListCollections(current, bson.M{"name": UnsupportedCustodyCollection})
	if e != nil {
		return e
	}
	count := 0
	for rows.Next(current) {
		var row struct {
			Name    string   `bson:"name"`
			Type    string   `bson:"type"`
			Options bson.Raw `bson:"options"`
			Info    *struct {
				ReadOnly *bool `bson:"readOnly"`
			} `bson:"info"`
		}
		if rows.Decode(&row) != nil || row.Name != UnsupportedCustodyCollection || row.Type != "collection" || row.Info == nil || row.Info.ReadOnly == nil || *row.Info.ReadOnly {
			e = ErrUnprepared
			break
		}
		fields, err := row.Options.Elements()
		if err != nil || len(fields) != 0 {
			e = ErrUnprepared
			break
		}
		count++
	}
	if e == nil {
		e = rows.Err()
	}
	closeErr := rows.Close(current)
	if e != nil || closeErr != nil {
		return errors.Join(e, closeErr)
	}
	if count > 1 {
		return ErrConflict
	}
	if e = check(); e != nil {
		return e
	}
	if count == 0 {
		if e = s.db.CreateCollection(current, UnsupportedCustodyCollection); e != nil {
			return e
		}
	}
	if _, e = s.db.Collection(UnsupportedCustodyCollection).Indexes().CreateOne(current, mongo.IndexModel{Keys: bson.D{{Key: "deadline", Value: 1}}, Options: options.Index().SetName(unsupportedCustodyTTL).SetExpireAfterSeconds(0)}); e != nil {
		return e
	}
	if e = check(); e != nil {
		return e
	}
	if e = s.transaction(current, func(tx context.Context) error {
		if e := check(); e != nil {
			return e
		}
		if e := s.requireTX(tx); e != nil {
			return e
		}
		marker := bson.D{{Key: "_id", Value: unsupportedCustodySchema}, {Key: "kind", Value: unsupportedCustodyKind}, {Key: "epoch", Value: s.epoch}, {Key: "activation_sha256", Value: s.activationSHA}, {Key: "version", Value: int32(1)}}
		// Uses the same exact immutable BSON insert-or-inspect helper as the held
		// namespace bootstrap; it never changes an existing schema or epoch.
		if e := immutableImportDocument(tx, s.db.Collection(UnsupportedCustodyCollection), unsupportedCustodySchema, marker); e != nil {
			return e
		}
		row, e := s.readUnsupportedCustodyGeneration(tx)
		if errors.Is(e, mongo.ErrNoDocuments) {
			if p.AdmissionGeneration != 1 {
				return ErrConflict
			}
			_, e = s.db.Collection(Receipts).InsertOne(tx, unsupportedCustodyGeneration{ID: s.unsupportedCustodyGenerationID(), Kind: unsupportedCustodyGenerationKind, Epoch: s.epoch, Activation: s.activationSHA, HeaderSHA: p.HeaderSHA256, InitialPolicySHA: c.inputSHA, HoldSHA: hold.RecordSHA256(), Generation: 1, Serial: 1})
			return e
		}
		if e != nil {
			return e
		}
		if row.HeaderSHA != p.HeaderSHA256 || row.Generation != p.AdmissionGeneration {
			return ErrConflict
		}
		return nil
	}); e != nil {
		return e
	}
	if e = check(); e != nil {
		return e
	}
	return c.CheckReady(current)
}
