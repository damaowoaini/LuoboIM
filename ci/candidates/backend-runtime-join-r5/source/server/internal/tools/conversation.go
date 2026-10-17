// Copyright © 2023 OpenIM. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tools

import (
	"context"
	"time"

	"github.com/OpenIMSDK/tools/log"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/openimsdk/open-im-server/v3/internal/retention"
)

func (c *MsgTool) ConversationsDestructMsgs() {
	c.conversationsDestructMsgs(mcontext.NewCtx("retention-cron-v1"))
}

func (c *MsgTool) conversationsDestructMsgs(parent context.Context) {
	// No old cron fallback. Activation requires a coordinated writer cutover and
	// explicit preparation of legacy policy metadata before this opt-in exists.
	if c.retentionStore == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	effects := retentionEffects{tool: c}
	if _, err := c.retentionStore.Drain(ctx, effects, 20); err != nil {
		log.ZWarn(ctx, "retention synchronization pending", nil)
	}
	summary, err := retention.Run(ctx, c.retentionStore, time.Now())
	if err != nil {
		log.ZWarn(ctx, "retention scan incomplete", nil)
	}
	log.ZInfo(ctx, "retention scan summary", "visited", summary.Visited, "committed", summary.Committed, "skipped", summary.Skipped, "failed", summary.Failed)
	if _, err := c.retentionStore.Drain(ctx, effects, 20); err != nil {
		log.ZWarn(ctx, "retention synchronization pending", nil)
	}
}

type retentionEffects struct{ tool *MsgTool }

func (e retentionEffects) SyncFloor(ctx context.Context, owner, cid string, floor int64) error {
	// The same committed floor is replayed idempotently. Mongo remains the
	// authority if Redis is unavailable/stale; this never decreases visibility.
	return e.tool.msgDatabase.SetConversationUserMinSeq(ctx, cid, owner, floor)
}
func (e retentionEffects) Notify(ctx context.Context, owner, cid string, seqs []int64) error {
	return e.tool.msgNotificationSender.UserDeleteMsgsNotification(ctx, owner, cid, seqs)
}
