package rtc

import (
	"context"
	"encoding/json"

	"github.com/OpenIMSDK/protocol/constant"
	pb "github.com/OpenIMSDK/protocol/rtc"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type WebhookEvent struct {
	ID        string `json:"id"`
	Event     string `json:"event"`
	CreatedAt int64  `json:"createdAt"`
	Room      struct {
		Name string `json:"name"`
		SID  string `json:"sid"`
	} `json:"room"`
	Participant struct {
		Identity string `json:"identity"`
		SID      string `json:"sid"`
	} `json:"participant"`
	Track struct {
		SID    string `json:"sid"`
		Source any    `json:"source"`
		Muted  bool   `json:"muted"`
	} `json:"track"`
}

func (s *Server) HandleWebhook(ctx context.Context, body []byte, authorization string) error {
	verifier, ok := s.media.(interface{ VerifyWebhook([]byte, string) error })
	if !ok {
		return deny("RTC webhook verifier unavailable")
	}
	if err := verifier.VerifyWebhook(body, authorization); err != nil {
		return err
	}
	var event WebhookEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return argument("invalid RTC webhook body")
	}
	if event.ID == "" || len(event.ID) > 128 || !validRoomID(event.Room.Name) {
		return argument("invalid RTC webhook identity")
	}
	// JWT expiry authenticates the delivery; the persistent event ID handles
	// provider retries and delayed delivery without an arbitrary event-age cutoff.
	_, err := s.transaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
		seen := s.db.Collection("rtc_webhook_events")
		if found, err := seen.CountDocuments(tx, bson.M{"_id": event.ID}); err != nil {
			return nil, err
		} else if found > 0 {
			return nil, nil
		}
		if _, err := seen.InsertOne(tx, bson.M{"_id": event.ID, "room_id": event.Room.Name, "created_at": event.CreatedAt, "received_at": s.now()}); err != nil {
			return nil, err
		}
		room, err := s.load(tx, event.Room.Name)
		if err != nil {
			if isMissing(err) {
				return nil, nil
			}
			return nil, err
		}
		previous := room.Version
		if room.terminal() {
			if event.Event == "participant_joined" || event.Event == "room_started" {
				if err := s.save(tx, room, previous); err != nil {
					return nil, err
				}
				if err := s.enqueueMedia(tx, room, "delete_room", "", "", false); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}
		switch event.Event {
		case "participant_joined":
			participant := room.participant(event.Participant.Identity)
			if participant == nil || participant.Status != "accepted" && participant.Status != "joined" {
				if err := s.save(tx, room, previous); err != nil {
					return nil, err
				}
				return nil, s.enqueueMedia(tx, room, "remove", event.Participant.Identity, "", false)
			}
			if ok, err := s.lease(tx, "user:"+participant.UserID, room.RoomID, room.ExpiresAt); err != nil {
				return nil, err
			} else if !ok {
				if err := s.save(tx, room, previous); err != nil {
					return nil, err
				}
				return nil, s.enqueueMedia(tx, room, "remove", participant.UserID, "", false)
			}
			if event.CreatedAt != 0 && participant.MediaJoinedAt > event.CreatedAt {
				return nil, nil
			}
			if participant.Status == "joined" && participant.MediaSID == event.Participant.SID {
				return nil, nil
			}
			participant.MediaSID = event.Participant.SID
			participant.MediaJoinedAt = event.CreatedAt
			participant.Status = "joined"
			participant.UpdatedAt = s.now()
			if err := s.save(tx, room, previous); err != nil {
				return nil, err
			}
			inv, _ := room.invitation()
			if err := s.enqueue(tx, room, room.Host, constant.RoomParticipantsConnectedNotification, &pb.SignalOnRoomParticipantConnectedReq{Invitation: inv, Participant: room.metadata(), GroupID: room.GroupID}, room.recipients(), nil); err != nil {
				return nil, err
			}
		case "participant_left", "participant_connection_aborted":
			participant := room.participant(event.Participant.Identity)
			if participant == nil || participant.Status == "left" {
				return nil, nil
			}
			// A delayed disconnect from a replaced media session cannot evict a
			// newer session with the same user identity.
			if participant.MediaSID != "" && event.Participant.SID != participant.MediaSID {
				return nil, nil
			}
			if event.CreatedAt != 0 && participant.MediaJoinedAt > event.CreatedAt {
				return nil, nil
			}
			participant.Status = "left"
			participant.UpdatedAt = s.now()
			if _, err := s.active.DeleteOne(tx, bson.M{"_id": "user:" + participant.UserID, "room_id": room.RoomID}); err != nil {
				return nil, err
			}
			count := 0
			for _, p := range room.Participants {
				if p.Status == "accepted" || p.Status == "joined" {
					count++
				}
			}
			if room.Kind == "call" && (room.GroupID == "" || count == 0) {
				if err := s.end(tx, room, roomEnded); err != nil {
					return nil, err
				}
			}
			if err := s.save(tx, room, previous); err != nil {
				return nil, err
			}
			inv, _ := room.invitation()
			if err := s.enqueue(tx, room, room.Host, constant.RoomParticipantsDisconnectedNotification, &pb.SignalOnRoomParticipantDisconnectedReq{Invitation: inv, Participant: room.metadata(), GroupID: room.GroupID}, room.recipients(), nil); err != nil {
				return nil, err
			}
			if room.terminal() {
				if err := s.enqueueMedia(tx, room, "delete_room", "", "", false); err != nil {
					return nil, err
				}
			}
		case "room_finished":
			// Empty scheduled meetings may be rejoined until their persisted end time.
			if room.Kind == "call" {
				if err := s.end(tx, room, roomEnded); err != nil {
					return nil, err
				}
				if err := s.save(tx, room, previous); err != nil {
					return nil, err
				}
			}
		case "track_published", "track_unpublished":
			participant := room.participant(event.Participant.Identity)
			if participant == nil {
				return nil, deny("unrecognized media participant")
			}
			if err := s.enqueueMedia(tx, room, "permissions", "", "", false); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	return err
}
