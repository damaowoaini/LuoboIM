package meetingbooking

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBookingStoredReceiptRejectsUnknownActionHashAndResolvedContradictions(t *testing.T) {
	p := Principal{Tenant: "A", Owner: "owner"}
	id := uuid.NewString()
	good := Intent{Tenant: p.Tenant, Owner: p.Owner, RequestID: id, Action: "create", Hash: strings.Repeat("a", 64), BookingID: uuid.NewString(), AppliedVersion: 1, CreatedAt: time.Unix(1000, 0)}
	if !storedIntentValid(&good, p, id) {
		t.Fatal("valid receipt rejected")
	}
	for name, mutate := range map[string]func(*Intent){"unknown-action": func(v *Intent) { v.Action = "join" }, "nonhex": func(v *Intent) { v.Hash = strings.Repeat("g", 64) }, "uppercase": func(v *Intent) { v.Hash = strings.Repeat("A", 64) }, "badversion": func(v *Intent) { v.AppliedVersion = 0 }, "resolved-with-room": func(v *Intent) { v.Resolved = true; v.Action = "resolve" }, "foreign-owner": func(v *Intent) { v.Owner = "other" }} {
		t.Run(name, func(t *testing.T) {
			v := good
			mutate(&v)
			if storedIntentValid(&v, p, id) {
				t.Fatal("corrupt receipt accepted")
			}
		})
	}
}
func TestBookingStoredRowsUseSameIntegrityForDetailAndList(t *testing.T) {
	p := Principal{Tenant: "A", Owner: "owner"}
	id := uuid.NewString()
	good := Booking{ID: id, Tenant: p.Tenant, Owner: p.Owner, State: "scheduled", Version: 1, Payload: &Payload{MeetingName: "ok", StartTime: 1000, MeetingDuration: 3600, InviteeUserIDs: []string{}}, CreatedAt: time.Unix(1000, 0), UpdatedAt: time.Unix(1000, 0)}
	if !storedBookingValid(&good, p, id) {
		t.Fatal("valid row rejected")
	}
	for name, mutate := range map[string]func(*Booking){"unknown-state": func(v *Booking) { v.State = "accepted" }, "missing-payload": func(v *Booking) { v.Payload = nil }, "cancel-with-payload": func(v *Booking) { v.State = "cancelled"; v.Version = 2 }, "foreign-tenant": func(v *Booking) { v.Tenant = "B" }, "bad-timestamps": func(v *Booking) { v.UpdatedAt = time.Unix(1, 0) }} {
		t.Run(name, func(t *testing.T) {
			v := good
			mutate(&v)
			if storedBookingValid(&v, p, id) {
				t.Fatal("corrupt booking accepted")
			}
		})
	}
}
