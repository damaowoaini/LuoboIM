package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

func clientMessageSeqKey(conversationID, clientID string) string {
	value, _ := json.Marshal([2]string{conversationID, clientID})
	digest := sha256.Sum256(value)
	return "MESSAGE_CLIENT_SEQ:" + hex.EncodeToString(digest[:])
}

// This is a locator only. Callers must re-read the actual message and enforce
// its owner, sequence visibility, conversation and client ID before using it.
func (c *msgCache) GetClientMessageSeqs(ctx context.Context, conversationID string, clientIDs []string) (map[string]int64, error) {
	pipe := c.rdb.Pipeline()
	commands := make(map[string]*redis.StringCmd, len(clientIDs))
	for _, id := range clientIDs {
		commands[id] = pipe.Get(ctx, clientMessageSeqKey(conversationID, id))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, err
	}
	result := make(map[string]int64, len(clientIDs))
	for id, command := range commands {
		seq, err := command.Int64()
		if err == redis.Nil {
			continue
		}
		if err != nil || seq < 1 {
			return nil, fmt.Errorf("invalid client message sequence locator")
		}
		result[id] = seq
	}
	return result, nil
}
