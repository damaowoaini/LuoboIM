package msgtransfer

import (
	"context"
	"os"

	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"go.mongodb.org/mongo-driver/mongo"
)

const OwnedUnsupportedCustodyPathEnv = "LUOBO_UNSUPPORTED_MODIFIER_CUSTODY_PATH"
const OwnedUnsupportedCustodySHAEnv = "LUOBO_UNSUPPORTED_MODIFIER_CUSTODY_SHA256"

func (i *DurableKafkaIntake) loadOwnedUnsupportedCustody(ctx context.Context, db *mongo.Database, epoch, activation string) error {
	path, pin := os.Getenv(OwnedUnsupportedCustodyPathEnv), os.Getenv(OwnedUnsupportedCustodySHAEnv)
	// No modifier capability is an ordinary startup requirement. If one actually
	// appears with no reviewed raw-custody scope, its offset stays unmarked and
	// the existing visible failure/stop mechanism remains in force.
	if path == "" && pin == "" {
		return nil
	}
	if i == nil || db == nil || path == "" || pin == "" || i.topic != config.Config.Kafka.LatestMsgToRedis.Topic {
		return messagedelivery.ErrUnprepared
	}
	cluster := deliveryprofile.Digest(config.Config.Kafka.Addr)
	c, e := messagedelivery.LoadOwnedUnsupportedKafkaCustody(ctx, db, epoch, activation, cluster, config.Config.Kafka.ConsumerGroupID.MsgToRedis, path, pin)
	if e != nil {
		return e
	}
	i.custody = c
	return nil
}

func (m *ManagedOrdinaryKafka) UnsupportedCustodySummary(ctx context.Context) (messagedelivery.UnsupportedCustodySummary, error) {
	if m == nil || m.intake == nil {
		return messagedelivery.UnsupportedCustodySummary{}, messagedelivery.ErrUnprepared
	}
	reader, ok := m.intake.store.(interface {
		UnsupportedCustodySummary(context.Context) (messagedelivery.UnsupportedCustodySummary, error)
	})
	if !ok {
		return messagedelivery.UnsupportedCustodySummary{}, messagedelivery.ErrUnprepared
	}
	return reader.UnsupportedCustodySummary(ctx)
}
