package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/dtm-labs/rockscache"
	"github.com/redis/go-redis/v9"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
)

const (
	blockNum = 100
	// Redis TTL for super-group read/unread structures (HASH / LIST / ZSET); legacy was 72h.
	expirationTime      = time.Hour * 6
	rocksExpirationTime = expirationTime - time.Hour // index key expires slightly earlier
	hsetDataIndexKey    = "$GROUP_READ_DATA_INDEX$"
)

type GetUserIDsFunc func(ctx context.Context) ([]string, error)

type MsgGroupReadCache interface {
	GetMsgNum(ctx context.Context, conversationID string, clientMsgID string) (readNum int64, unreadNum int64, err error)
	MarkGroupMessageRead(ctx context.Context, conversationID string, clientMsgID string, userID string, readTime *time.Time, getUserIDs GetUserIDsFunc) (int32, error)
	PageReadUserList(ctx context.Context, conversationID string, clientMsgID string, read bool, pageNumber int32, showNumber int32) (int64, []*UserReadTime, error)
}

func NewMsgGroupReadCache(rdb redis.UniversalClient, msg unrelation.GroupMsgReadInterface) MsgGroupReadCache {
	return &msgGroupReadCache{rdb: rdb, rcClient: rockscache.NewClient(rdb, rockscache.NewDefaultOptions()), msg: msg}
}

type msgGroupReadCache struct {
	msg      unrelation.GroupMsgReadInterface
	rdb      redis.UniversalClient
	rcClient *rockscache.Client
}

func (m *msgGroupReadCache) hashTagKey(typ string, conversationID string, clientMsgID string) string {
	return "{" + conversationID + "-" + clientMsgID + "}" + typ + ":" + conversationID + "-" + clientMsgID
}

func (m *msgGroupReadCache) getMsgIndexKey(conversationID string, clientMsgID string) string {
	//return "GROUP_READ_MSG_IDX:" + conversationID + ":" + clientMsgID
	return m.hashTagKey("GROUP_READ_MSG_IDX", conversationID, clientMsgID)
}

func (m *msgGroupReadCache) getMsgReadMapKey(conversationID string, clientMsgID string) string {
	//return "GROUP_READ_MSG_HASH:" + conversationID + ":" + clientMsgID
	return m.hashTagKey("GROUP_READ_MSG_HASH", conversationID, clientMsgID)
}

func (m *msgGroupReadCache) getMsgReadListKey(conversationID string, clientMsgID string) string {
	//return "GROUP_READ_MSG_LIST:" + conversationID + ":" + clientMsgID
	return m.hashTagKey("GROUP_READ_MSG_LIST", conversationID, clientMsgID)
}

func (m *msgGroupReadCache) getMsgUnreadListKey(conversationID string, clientMsgID string) string {
	//return "GROUP_UNREAD_MSG_LIST:" + conversationID + ":" + clientMsgID
	return m.hashTagKey("GROUP_UNREAD_MSG_LIST", conversationID, clientMsgID)
}

func (m *msgGroupReadCache) getMsgReadIndex(ctx context.Context, conversationID string, clientMsgID string) (int, error) {
	return m.getMsgBaseIndex(ctx, conversationID, clientMsgID, nil, nil)
}

func (m *msgGroupReadCache) getMsgWriteIndex(ctx context.Context, conversationID string, clientMsgID string, getUserIDs GetUserIDsFunc) (int, error) {
	ttl := 5
	return m.getMsgBaseIndex(ctx, conversationID, clientMsgID, getUserIDs, &ttl)
}

func (m *msgGroupReadCache) getMsgBaseIndex(ctx context.Context, conversationID string, clientMsgID string, getUserIDs GetUserIDsFunc, ttl *int) (int, error) {
	index, err := getCache(ctx, m.rcClient, m.getMsgIndexKey(conversationID, clientMsgID), rocksExpirationTime, func(ctx context.Context) (int, error) {
		index, err := m.msg.GetMsgIndex(ctx, conversationID, clientMsgID)
		if err != nil {
			return 0, err
		}
		var readInfo map[string]*time.Time
		if index > 0 {
			readInfo, err = m.msg.GetMsgRead(ctx, conversationID, index, clientMsgID)
			if err != nil {
				return 0, err
			}
			if readInfo == nil {
				readInfo = make(map[string]*time.Time)
			}
		} else {
			if getUserIDs == nil {
				return index, nil
			}
			userIDs, err := getUserIDs(ctx)
			if err != nil {
				return 0, err
			}
			maxIndex, num, err := m.msg.GetMaxIndex(ctx, conversationID)
			if err != nil {
				return 0, err
			}
			readInfo = make(map[string]*time.Time)
			for _, userID := range userIDs {
				readInfo[userID] = nil
			}
			if num == 0 || num >= blockNum {
				maxIndex++
				err := m.msg.Insert(ctx, &unrelation.GroupMsgReadModel{
					ConversationID: conversationID,
					Index:          maxIndex,
					Msgs: map[string]map[string]*time.Time{
						clientMsgID: readInfo,
					},
				})
				if err != nil {
					return 0, err
				}
			} else {
				if err := m.msg.InsertMsg(ctx, conversationID, maxIndex, clientMsgID, readInfo); err != nil {
					return 0, err
				}
			}
			index = maxIndex
		}
		hashList := make([]any, 0, len(readInfo)*2+2)
		readList := make([]UserReadTime, 0, len(readInfo))
		unreadUserIDs := make([]string, 0, len(readInfo))
		hashList = append(hashList, hsetDataIndexKey, strconv.Itoa(index)) // 必须有一对key-value
		for userID, readTime := range readInfo {
			if readTime == nil {
				hashList = append(hashList, userID, "")
				unreadUserIDs = append(unreadUserIDs, userID)
			} else {
				ts := readTime.UnixMilli()
				hashList = append(hashList, userID, ts)
				readList = append(readList, UserReadTime{UserID: userID, Time: ts})
			}
		}
		sort.Sort(sortReadTimes(readList))
		sort.Strings(unreadUserIDs)
		unreadList := make([]any, 0, len(unreadUserIDs)*2)
		for i, userID := range unreadUserIDs {
			unreadList = append(unreadList, i+1, userID)
		}
		readListArgs := make([]any, 0, len(readList)+1) // 必须有一个
		readListArgs = append(readListArgs, strconv.Itoa(index))
		for _, readTime := range readList {
			data, err := json.Marshal(readTime)
			if err != nil {
				return 0, err
			}
			readListArgs = append(readListArgs, string(data))
		}
		if err := m.setRedisList(ctx, conversationID, clientMsgID, expirationTime, hashList, readListArgs, unreadList); err != nil {
			return 0, err
		}
		return index, nil
	})
	if err != nil {
		return 0, err
	}
	if index > 0 || getUserIDs == nil {
		return index, nil
	}
	if ttl != nil {
		if *ttl <= 0 {
			return 0, errs.Wrap(errors.New("failed to retry"))
		}
		*ttl--
	}
	if err := m.rdb.Del(ctx, m.getMsgIndexKey(conversationID, clientMsgID)).Err(); err != nil {
		return 0, errs.Wrap(err)
	}
	return m.getMsgBaseIndex(ctx, conversationID, clientMsgID, getUserIDs, ttl)
}

func (m *msgGroupReadCache) setRedisList(ctx context.Context, conversationID string, clientMsgID string, expire time.Duration, hashArgs []any, readArgs []any, unreadArgs []any) error {
	script := `
local hashArgs = {}
for i = 5, ARGV[2]+4 do
    table.insert(hashArgs, ARGV[i])
end
local readArgs = {}
for i = 5+ARGV[2], 4+ARGV[2]+ARGV[3] do
    table.insert(readArgs, ARGV[i])
end
local unreadArgs = {}
for i = 5+ARGV[2]+ARGV[3], #ARGV do
    table.insert(unreadArgs, ARGV[i])
end
redis.call("DEL", KEYS[1], KEYS[2], KEYS[3])
redis.call("HMSET", KEYS[1], unpack(hashArgs))
redis.call("RPUSH", KEYS[2], unpack(readArgs))
if #unreadArgs > 0 then
	redis.call("ZADD", KEYS[3], unpack(unreadArgs))
	redis.call("EXPIRE", KEYS[3], ARGV[1])
end
redis.call("EXPIRE", KEYS[2], ARGV[1])
redis.call("EXPIRE", KEYS[1], ARGV[1])
return 1
`
	keys := []string{m.getMsgReadMapKey(conversationID, clientMsgID), m.getMsgReadListKey(conversationID, clientMsgID), m.getMsgUnreadListKey(conversationID, clientMsgID)}
	args := make([]any, 0, len(hashArgs)+len(readArgs)+len(unreadArgs)+4)
	args = append(args, expire.Seconds(), len(hashArgs), len(readArgs), len(unreadArgs))
	args = append(args, hashArgs...)
	args = append(args, readArgs...)
	args = append(args, unreadArgs...)
	res, err := m.rdb.Eval(ctx, script, keys, args...).Int64()
	if err != nil {
		return errs.Wrap(err)
	}
	if res != 1 {
		return fmt.Errorf("readis lua unknown return value %d", res)
	}
	return nil
}

func (m *msgGroupReadCache) GetReadNum(ctx context.Context, conversationID string, clientMsgID string) (int64, error) {
	res, err := m.rdb.LLen(ctx, m.getMsgReadListKey(conversationID, clientMsgID)).Result()
	if err == nil {
		if res > 0 {
			res--
		}
		return res, nil
	} else if err == redis.Nil {
		return 0, nil
	} else {
		return 0, errs.Wrap(err)
	}
}

func (m *msgGroupReadCache) GetUnreadNum(ctx context.Context, conversationID string, clientMsgID string) (int64, error) {
	res, err := m.rdb.ZCard(ctx, m.getMsgUnreadListKey(conversationID, clientMsgID)).Result()
	if err == nil {
		return res, nil
	} else if err == redis.Nil {
		return 0, nil
	} else {
		return 0, errs.Wrap(err)
	}
}

func (m *msgGroupReadCache) GetMsgNum(ctx context.Context, conversationID string, clientMsgID string) (readNum int64, unreadNum int64, err error) {
	index, err := m.getMsgReadIndex(ctx, conversationID, clientMsgID)
	if err != nil {
		return 0, 0, err
	}
	if index <= 0 {
		return 0, 0, nil
	}
	readNum, err = m.GetReadNum(ctx, conversationID, clientMsgID)
	if err != nil {
		return 0, 0, err
	}
	unreadNum, err = m.GetUnreadNum(ctx, conversationID, clientMsgID)
	if err != nil {
		return 0, 0, err
	}
	return
}

func (m *msgGroupReadCache) PageReadUserList(ctx context.Context, conversationID string, clientMsgID string, read bool, pageNumber int32, showNumber int32) (int64, []*UserReadTime, error) {
	index, err := m.getMsgReadIndex(ctx, conversationID, clientMsgID)
	if err != nil {
		return 0, nil, err
	}
	if index <= 0 {
		return 0, nil, nil
	}
	if read {
		num, err := m.GetReadNum(ctx, conversationID, clientMsgID)
		if err != nil {
			return 0, nil, err
		}
		start := int64((pageNumber-1)*showNumber + 1)
		if start > num {
			return num, nil, nil
		}
		end := int64(pageNumber * showNumber)
		res, err := m.rdb.LRange(ctx, m.getMsgReadListKey(conversationID, clientMsgID), start, end).Result()
		if err != nil {
			return 0, nil, err
		}
		readTime := make([]*UserReadTime, 0, len(res))
		for _, jsonStr := range res {
			var urt UserReadTime
			if err := json.Unmarshal([]byte(jsonStr), &urt); err != nil {
				log.ZError(ctx, "PageReadUserList json.Unmarshal failed", err, "jsonStr", jsonStr)
				continue
			}
			readTime = append(readTime, &urt)
		}
		return num, readTime, nil
	} else {
		num, err := m.GetUnreadNum(ctx, conversationID, clientMsgID)
		if err != nil {
			return 0, nil, err
		}
		start := int64((pageNumber - 1) * showNumber)
		if start > num {
			return num, nil, nil
		}
		end := int64(pageNumber*showNumber) - 1
		res, err := m.rdb.ZRange(ctx, m.getMsgUnreadListKey(conversationID, clientMsgID), start, end).Result()
		if err != nil {
			return 0, nil, err
		}
		readTime := make([]*UserReadTime, 0, len(res))
		for _, jsonStr := range res {
			readTime = append(readTime, &UserReadTime{UserID: jsonStr})
		}
		return num, readTime, nil
	}
}

func (m *msgGroupReadCache) MarkGroupMessageRead(ctx context.Context, conversationID string, clientMsgID string, userID string, readTime *time.Time, getUserIDs GetUserIDsFunc) (int32, error) {
	index, err := m.getMsgWriteIndex(ctx, conversationID, clientMsgID, getUserIDs)
	if err != nil {
		return 0, err
	}
	ok, err := m.markUser(ctx, conversationID, clientMsgID, userID, readTime.UnixMilli())
	if err != nil {
		return 0, err
	}
	if ok {
		if err := m.msg.SetMsgRead(ctx, conversationID, index, clientMsgID, userID, readTime); err != nil {
			return 0, err
		}
		return constant.GroupMessageReadMarkNew, nil
	}
	return constant.GroupMessageReadMarked, nil
}

func (m *msgGroupReadCache) markUser(ctx context.Context, conversationID string, clientMsgID string, userID string, readTime int64) (bool, error) {
	data, err := json.Marshal(&UserReadTime{UserID: userID, Time: readTime})
	if err != nil {
		return false, err
	}
	script := `
if redis.call("EXISTS", KEYS[1]) == 0 then
	return 404
end
local value = redis.call("HGET", KEYS[1], ARGV[1])
if value == "" or value == false or value == nil then
	redis.call("HSET", KEYS[1], ARGV[1], ARGV[2])
	redis.call("RPUSH", KEYS[2],  ARGV[3])
	redis.call("ZREM", KEYS[3],  ARGV[1])
	return 1
end
return 0
`
	keys := []string{m.getMsgReadMapKey(conversationID, clientMsgID), m.getMsgReadListKey(conversationID, clientMsgID), m.getMsgUnreadListKey(conversationID, clientMsgID)}
	args := []any{userID, readTime, string(data)}
	res, err := m.rdb.Eval(ctx, script, keys, args...).Int()
	if err != nil {
		return false, errs.Wrap(err)
	}
	if res == 404 {
		return false, errs.Wrap(redis.Nil)
	}
	return res == 1, nil
}

type UserReadTime struct {
	UserID string `json:"userID"`
	Time   int64  `json:"time"`
}

func (u UserReadTime) String() string {
	s := time.UnixMilli(u.Time).Format("2006-01-02 15:04:05.000")
	return fmt.Sprintf("userID: %s, time: %s", u.UserID, s)
}

type sortReadTimes []UserReadTime

func (s sortReadTimes) Len() int {
	return len(s)
}

func (s sortReadTimes) Less(i, j int) bool {
	return s[i].Time < s[j].Time
}

func (s sortReadTimes) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
