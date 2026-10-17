package unrelation

import "time"

// UserID is empty only for the conversation-wide retention/clear boundary.
// A floor can advance, never decrease through the application API.
type MessageVisibilityFloor struct {
	ID             string    `bson:"_id" json:"id"`
	ConversationID string    `bson:"conversation_id" json:"conversationID"`
	UserID         string    `bson:"user_id" json:"userID"`
	MinSeq         int64     `bson:"min_seq" json:"minSeq"`
	UpdatedAt      time.Time `bson:"updated_at" json:"updatedAt"`
}
