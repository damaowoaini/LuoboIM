//go:build darwin

package messagedelivery

import (
	"context"
	"go.mongodb.org/mongo-driver/mongo"
)

// Default factory behavior is preserved. Private held startup requires the
// Linux real owned supervisor; Darwin does not fabricate an equivalent proof.
type HistoryReaderStartupBinding struct{}

func (*HistoryReaderStartupBinding) AwaitFinalRuntimeReady(context.Context) error {
	return ErrUnprepared
}
func NewStartupHistoryWindowReader(ctx context.Context, db *mongo.Database, role, path, exactSHA string) (*HistoryWindowReader, *HistoryReaderStartupBinding, error) {
	if path != "" || exactSHA != "" {
		return nil, nil, ErrUnprepared
	}
	reader, e := NewHistoryWindowReaderFromEnvironment(db)
	if e != nil {
		return nil, nil, e
	}
	if e = reader.CheckReady(ctx); e != nil {
		return nil, nil, e
	}
	return reader, nil, nil
}
