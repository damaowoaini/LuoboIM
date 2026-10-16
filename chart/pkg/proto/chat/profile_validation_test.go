package chat

import (
	"github.com/OpenIMSDK/protocol/wrapperspb"
	"strings"
	"testing"
	"time"
)

func TestProfileUpdateValidationBoundaries(t *testing.T) {
	valid := []*UpdateUserInfoReq{{UserID: "owner", Nickname: &wrapperspb.StringValue{Value: "萝卜😀"}}, {UserID: "owner", FaceURL: &wrapperspb.StringValue{Value: ""}}, {UserID: "owner", FaceURL: &wrapperspb.StringValue{Value: "https://fixture.invalid/u/avatar.png"}}, {UserID: "owner", Gender: &wrapperspb.Int32Value{Value: 0}}, {UserID: "owner", Birth: &wrapperspb.Int64Value{Value: time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()}}}
	for _, req := range valid {
		if err := req.Check(); err != nil {
			t.Fatal(err)
		}
	}
	invalid := []*UpdateUserInfoReq{{UserID: "owner", Nickname: &wrapperspb.StringValue{Value: " \n"}}, {UserID: "owner", Nickname: &wrapperspb.StringValue{Value: strings.Repeat("😀", 65)}}, {UserID: "owner", Nickname: &wrapperspb.StringValue{Value: "bad\x00name"}}, {UserID: "owner", FaceURL: &wrapperspb.StringValue{Value: "http://example.com/a"}}, {UserID: "owner", FaceURL: &wrapperspb.StringValue{Value: "https://user:secret@example.com/a"}}, {UserID: "owner", FaceURL: &wrapperspb.StringValue{Value: "https://example.com/a#secret"}}, {UserID: "owner", Gender: &wrapperspb.Int32Value{Value: 3}}, {UserID: "owner", AllowBeep: &wrapperspb.Int32Value{Value: 0}}, {UserID: "owner", AllowVibration: &wrapperspb.Int32Value{Value: 3}}, {UserID: "owner", AllowAddFriend: &wrapperspb.Int32Value{Value: -1}}, {UserID: "owner", Birth: &wrapperspb.Int64Value{Value: time.Now().Add(24 * time.Hour).UnixMilli()}}}
	for i, req := range invalid {
		if req.Check() == nil {
			t.Fatalf("invalid boundary accepted %d", i)
		}
	}
}
