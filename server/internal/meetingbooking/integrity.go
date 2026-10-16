package meetingbooking

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"time"
)

func storedIntentValid(row *Intent, p Principal, id string) bool {
	if row == nil || row.Tenant != p.Tenant || row.Owner != p.Owner || !validPrincipal(p) || row.RequestID != id || !validUUID(id) || row.CreatedAt.IsZero() {
		return false
	}
	if row.Resolved {
		return row.Action == "resolve" && row.Hash == "" && row.BookingID == "" && row.AppliedVersion == 0
	}
	hash, err := hex.DecodeString(row.Hash)
	if err != nil || len(hash) != 32 || hex.EncodeToString(hash) != row.Hash || !validUUID(row.BookingID) {
		return false
	}
	switch row.Action {
	case "create":
		return row.AppliedVersion == 1
	case "update", "cancel":
		return row.AppliedVersion >= 2 && row.AppliedVersion <= 1000000000
	default:
		return false
	}
}
func storedBookingValid(row *Booking, p Principal, id string) bool {
	if row == nil || row.Tenant != p.Tenant || row.Owner != p.Owner || !validPrincipal(p) || row.ID != id || !validUUID(id) || row.Version < 1 || row.Version > 1000000000 || row.CreatedAt.IsZero() || row.UpdatedAt.Before(row.CreatedAt) {
		return false
	}
	if row.State == "cancelled" {
		return row.Payload == nil && row.Version >= 2
	}
	if row.State != "scheduled" || row.Payload == nil {
		return false
	}
	payload, err := canonical(p, *row.Payload)
	if err != nil || payload.StartTime <= 0 || payload.StartTime > time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC).Unix() {
		return false
	}
	before, _ := json.Marshal(row.Payload)
	after, _ := json.Marshal(payload)
	return bytes.Equal(before, after)
}
