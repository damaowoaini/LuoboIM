package rtc

import (
	"context"
	"github.com/OpenIMSDK/protocol/constant"
	pb "github.com/OpenIMSDK/protocol/rtc"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"time"
)

func (s *Server) Reconcile(ctx context.Context) error {
	cursor, err := s.rooms.Find(ctx, bson.M{"state": bson.M{"$in": bson.A{roomRinging, roomActive}}, "expires_at": bson.M{"$lte": s.now()}}, options.Find().SetLimit(100))
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	rooms := []Room{}
	if err := cursor.All(ctx, &rooms); err != nil {
		return err
	}
	for _, candidate := range rooms {
		_, err := s.transaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
			room, err := s.load(tx, candidate.RoomID)
			if err != nil {
				return nil, err
			}
			if room.terminal() || room.ExpiresAt.After(s.now()) {
				return nil, nil
			}
			previous := room.Version
			state := roomEnded
			if room.State == roomRinging {
				state = roomTimeout
			}
			if err := s.end(tx, room, state); err != nil {
				return nil, err
			}
			if err := s.save(tx, room, previous); err != nil {
				return nil, err
			}
			if err := s.enqueueMedia(tx, room, "delete_room", "", "", false); err != nil {
				return nil, err
			}
			if room.Kind == "call" {
				inv, err := room.invitation()
				if err != nil {
					return nil, err
				}
				notification := &pb.SignalReq{Payload: &pb.SignalReq_Cancel{Cancel: &pb.SignalCancelReq{Invitation: inv, UserID: room.Host}}}
				if err := s.enqueue(tx, room, room.Host, constant.SignalingNotification, notification, room.recipients(), nil); err != nil {
					return nil, err
				}
			} else {
				if err := s.enqueue(tx, room, room.Host, constant.CustomSignalNotification, &pb.SignalSendCustomSignalReq{RoomID: room.RoomID, CustomInfo: `{"type":"roomClosed","reason":"expired"}`}, room.recipients(), nil); err != nil {
					return nil, err
				}
			}
			return nil, nil
		})
		if err != nil {
			return err
		}
	}
	// An accepted group call can outlive the invitation window. Release only its
	// unanswered users so that a stale ring cannot keep those accounts busy.
	active, err := s.rooms.Find(ctx, bson.M{"kind": "call", "state": roomActive, "participants.status": "invited", "created_at": bson.M{"$lte": s.now().Add(-5 * time.Second)}}, options.Find().SetLimit(100))
	if err != nil {
		return err
	}
	defer active.Close(ctx)
	for active.Next(ctx) {
		var candidate Room
		if err := active.Decode(&candidate); err != nil {
			return err
		}
		_, err := s.transaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
			room, err := s.load(tx, candidate.RoomID)
			if err != nil {
				return nil, err
			}
			inv, err := room.invitation()
			if err != nil {
				return nil, err
			}
			if room.terminal() || s.now().Before(time.Unix(inv.InitiateTime, 0).Add(time.Duration(inv.Timeout)*time.Second)) {
				return nil, nil
			}
			previous := room.Version
			changed := false
			for i := range room.Participants {
				p := &room.Participants[i]
				if p.Status == "invited" {
					p.Status = "timeout"
					p.UpdatedAt = s.now()
					changed = true
					if _, err := s.active.DeleteOne(tx, bson.M{"_id": "user:" + p.UserID, "room_id": room.RoomID}); err != nil {
						return nil, err
					}
				}
			}
			if changed {
				if err := s.save(tx, room, previous); err != nil {
					return nil, err
				}
			}
			return nil, nil
		})
		if err != nil {
			return err
		}
	}
	return active.Err()
}
