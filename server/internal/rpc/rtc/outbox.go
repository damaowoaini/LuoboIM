package rtc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pb "github.com/OpenIMSDK/protocol/rtc"
	"github.com/OpenIMSDK/tools/log"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/proto"
)

func eventID(room *Room, receiver, action string, payload []byte) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "%s\n%d\n%s\n%s\n", room.RoomID, room.Version, receiver, action)
	hash.Write(payload)
	return hex.EncodeToString(hash.Sum(nil))
}
func (s *Server) enqueue(ctx context.Context, room *Room, sender string, kind int32, payload proto.Message, receivers []string, push []byte) error {
	data, err := proto.Marshal(payload)
	if err != nil {
		return err
	}
	now := s.now()
	for _, receiver := range receivers {
		job := &Outbox{ID: eventID(room, receiver, fmt.Sprint(kind), data), RoomID: room.RoomID, Revision: room.Version, Sender: sender, Receiver: receiver, ContentType: kind, Payload: data, OfflinePush: push, CreatedAt: now, NextAttemptAt: now}
		if _, err := s.outbox.UpdateOne(ctx, bson.M{"_id": job.ID}, bson.M{"$setOnInsert": job}, options.Update().SetUpsert(true)); err != nil {
			return err
		}
	}
	return nil
}
func (s *Server) enqueueMedia(ctx context.Context, room *Room, action, target, stream string, mute bool) error {
	data := []byte(fmt.Sprintf("%s:%s:%s:%t", action, target, stream, mute))
	now := s.now()
	job := &Outbox{ID: eventID(room, "media", action, data), RoomID: room.RoomID, Revision: room.Version, MediaAction: action, Target: target, Stream: stream, Mute: mute, CreatedAt: now, NextAttemptAt: now}
	_, err := s.outbox.UpdateOne(ctx, bson.M{"_id": job.ID}, bson.M{"$setOnInsert": job}, options.Update().SetUpsert(true))
	return err
}
func (s *Server) deliver(ctx context.Context, job *Outbox) error {
	if job.MediaAction != "" {
		if job.MediaAction == "delete_room" {
			return s.media.DeleteRoom(ctx, job.RoomID)
		}
		if job.MediaAction == "remove" {
			return s.media.Remove(ctx, job.RoomID, job.Target)
		}
		room, err := s.load(ctx, job.RoomID)
		if err != nil {
			if isMissing(err) {
				return nil
			}
			return err
		}
		if room.terminal() {
			return nil
		}
		switch job.MediaAction {
		case "permissions":
			for _, p := range room.Participants {
				if p.Status == "accepted" || p.Status == "joined" {
					if err := s.media.Permissions(ctx, room.RoomID, p.UserID, permission(room, p.UserID)); err != nil {
						return err
					}
				}
			}
			return nil
		case "mute":
			return s.media.Mute(ctx, room.RoomID, job.Target, job.Stream, job.Mute)
		}
		return argument("unknown RTC media action")
	}
	if job.ContentType == constant.SignalingNotification {
		req := &pb.SignalReq{}
		if err := proto.Unmarshal(job.Payload, req); err != nil {
			return err
		}
		if req.GetInvite() != nil || req.GetInviteInGroup() != nil {
			room, err := s.load(ctx, job.RoomID)
			if err != nil {
				if isMissing(err) {
					return nil
				}
				return err
			}
			if room.terminal() || !room.ExpiresAt.After(s.now()) || room.participant(job.Receiver) == nil || room.participant(job.Receiver).Status != "invited" {
				return nil
			}
		}
	}
	if s.delivery == nil {
		return fmt.Errorf("RTC delivery is unavailable")
	}
	return s.delivery.Send(ctx, job)
}

// A durable per-room/recipient lane serializes retries across service instances.
// Delivery is at least once; the stable event ID is also the IM ClientMsgID.
func (s *Server) ProcessOutbox(ctx context.Context, limit int) error {
	cursor, err := s.outbox.Find(ctx, bson.M{"delivered_at": nil, "next_attempt_at": bson.M{"$lte": s.now()}}, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(int64(limit)))
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	jobs := []Outbox{}
	if err := cursor.All(ctx, &jobs); err != nil {
		return err
	}
	lanes := s.db.Collection("rtc_delivery_lanes")
	for _, candidate := range jobs {
		laneID := candidate.RoomID + ":" + candidate.Receiver
		if candidate.MediaAction != "" {
			laneID = candidate.RoomID + ":media"
		}
		lease := uuid.NewString()
		now := s.now()
		_, err := lanes.UpdateOne(ctx, bson.M{"_id": laneID, "lease_until": bson.M{"$lte": now}}, bson.M{"$set": bson.M{"lease_token": lease, "lease_until": now.Add(30 * time.Second), "expire_at": now.Add(7 * 24 * time.Hour)}}, options.Update().SetUpsert(true))
		if mongo.IsDuplicateKeyError(err) {
			continue
		}
		if err != nil {
			return err
		}
		filter := bson.M{"room_id": candidate.RoomID, "delivered_at": nil}
		if candidate.MediaAction != "" {
			filter["media_action"] = bson.M{"$ne": ""}
		} else {
			filter["receiver"] = candidate.Receiver
			filter["media_action"] = ""
		}
		var job Outbox
		err = s.outbox.FindOne(ctx, filter, options.FindOne().SetSort(bson.D{{Key: "revision", Value: 1}, {Key: "created_at", Value: 1}, {Key: "_id", Value: 1}})).Decode(&job)
		if err == nil && !job.NextAttemptAt.After(now) {
			attempt, cancel := context.WithTimeout(ctx, 20*time.Second)
			deliveryErr := s.deliver(attempt, &job)
			cancel()
			if deliveryErr == nil {
				expires := now.Add(7 * 24 * time.Hour)
				_, err = s.outbox.UpdateOne(ctx, bson.M{"_id": job.ID, "delivered_at": nil}, bson.M{"$set": bson.M{"delivered_at": now, "expire_at": expires}, "$inc": bson.M{"attempts": 1}})
			} else {
				delay := time.Duration(1<<min(job.Attempts, 6)) * time.Second
				_, err = s.outbox.UpdateOne(ctx, bson.M{"_id": job.ID, "delivered_at": nil}, bson.M{"$set": bson.M{"next_attempt_at": now.Add(delay)}, "$inc": bson.M{"attempts": 1}})
			}
		}
		_, releaseErr := lanes.UpdateOne(ctx, bson.M{"_id": laneID, "lease_token": lease}, bson.M{"$set": bson.M{"lease_until": time.Time{}}})
		if releaseErr != nil {
			return releaseErr
		}
		if err != nil && err != mongo.ErrNoDocuments {
			return err
		}
	}
	return nil
}
func (s *Server) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		attempt, cancel := context.WithTimeout(ctx, 45*time.Second)
		if err := s.Reconcile(attempt); err != nil {
			log.ZWarn(ctx, "RTC timeout reconciliation pending", nil)
		}
		if err := s.ProcessOutbox(attempt, 100); err != nil {
			log.ZWarn(ctx, "RTC delivery reconciliation pending", nil)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
