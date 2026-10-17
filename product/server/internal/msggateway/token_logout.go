package msggateway

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"

	"github.com/OpenIMSDK/protocol/constant"
	pb "github.com/OpenIMSDK/protocol/msggateway"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
)

func tokenMatches(client *Client, user string, platform int32, digest []byte) bool {
	if client == nil || client.UserID != user || client.PlatformID != int(platform) {
		return false
	}
	hash := sha256.Sum256([]byte(client.token))
	return subtle.ConstantTimeCompare(hash[:], digest) == 1
}

func (s *Server) KickTokenOffline(ctx context.Context, req *pb.KickTokenOfflineReq) (*pb.KickTokenOfflineResp, error) {
	if err := authverify.CheckAccessV3(ctx, req.UserID); err != nil {
		return nil, err
	}
	if err := req.Check(); err != nil {
		return nil, err
	}
	digest, _ := hex.DecodeString(req.TokenHash)
	clients, _, ok := s.platformKickSnapshot(req.UserID, int(req.PlatformID))
	if !ok {
		return &pb.KickTokenOfflineResp{}, nil
	}
	for _, client := range clients {
		if tokenMatches(client, req.UserID, req.PlatformID, digest) {
			if err := client.longConnServer.KickUserConn(client); err != nil {
				return nil, err
			}
		}
	}
	return &pb.KickTokenOfflineResp{}, nil
}

func (ws *WsServer) verifyCurrentSession(client *Client) error {
	tokens, err := ws.cache.GetTokensWithoutError(client.ctx, client.UserID, client.PlatformID)
	if err != nil {
		return err
	}
	if status, ok := tokens[client.token]; !ok || status != constant.NormalToken {
		return errs.ErrTokenNotExist.Wrap("session revoked before registration")
	}
	return nil
}

// Fixed shards bound memory while unrelated users can close sessions independently.
func (ws *WsServer) lockSession(user string) func() {
	var h uint32 = 2166136261
	for i := 0; i < len(user); i++ {
		h = (h ^ uint32(user[i])) * 16777619
	}
	lock := &ws.sessionLocks[h%uint32(len(ws.sessionLocks))]
	lock.Lock()
	return lock.Unlock
}

func (s *Server) platformKickSnapshot(user string, platform int) ([]*Client, bool, bool) {
	if ws, ok := s.LongConnServer.(*WsServer); ok {
		unlock := ws.lockSession(user)
		defer unlock()
	}
	// Each new registration after this snapshot rechecks Redis. Socket close
	// runs outside the lock so unregister processing cannot deadlock on the queue.
	return s.LongConnServer.GetUserPlatformCons(user, platform)
}
