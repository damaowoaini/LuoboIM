package user

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	pbuser "github.com/OpenIMSDK/protocol/user"
	"github.com/OpenIMSDK/protocol/wrapperspb"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
)

type commandMemoryDB struct {
	controller.UserDatabase
	commands map[string]*pbuser.CommandInfoResp
	calls    int
	fail     bool
}

func (d *commandMemoryDB) AddUserCommand(_ context.Context, owner string, kind int32, id, value, ex string) error {
	d.calls++
	if d.fail {
		return errors.New("database unavailable")
	}
	d.commands[owner+id] = &pbuser.CommandInfoResp{Type: kind, Uuid: id, Value: value, Ex: ex}
	return nil
}
func (d *commandMemoryDB) UpdateUserCommand(_ context.Context, owner string, kind int32, id string, value, ex *string) error {
	d.calls++
	if d.fail {
		return errors.New("database unavailable")
	}
	command := d.commands[owner+id]
	if command == nil {
		return errors.New("not found")
	}
	if value != nil {
		command.Value = *value
	}
	if ex != nil {
		command.Ex = *ex
	}
	return nil
}
func (d *commandMemoryDB) DeleteUserCommand(_ context.Context, owner string, kind int32, id string) error {
	d.calls++
	delete(d.commands, owner+id)
	return nil
}
func (d *commandMemoryDB) GetUserCommands(_ context.Context, owner string, kind int32) ([]*pbuser.CommandInfoResp, error) {
	d.calls++
	if d.fail {
		return nil, errors.New("database unavailable")
	}
	out := []*pbuser.CommandInfoResp{}
	for key, value := range d.commands {
		if key == owner+value.Uuid && (kind == 0 || value.Type == kind) {
			out = append(out, value)
		}
	}
	return out, nil
}
func TestUserCommandCRUDPreservesPartialUpdatesAndReadback(t *testing.T) {
	db := &commandMemoryDB{commands: map[string]*pbuser.CommandInfoResp{}}
	server := &userServer{UserDatabase: db}
	ctx := context.WithValue(context.Background(), constant.OpUserID, "u1")
	if _, err := server.ProcessUserCommandAdd(ctx, &pbuser.ProcessUserCommandAddReq{UserID: "u1", Type: 1, Uuid: "c1", Value: &wrapperspb.StringValue{Value: "value"}, Ex: &wrapperspb.StringValue{Value: "extra"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.ProcessUserCommandUpdate(ctx, &pbuser.ProcessUserCommandUpdateReq{UserID: "u1", Type: 1, Uuid: "c1", Ex: &wrapperspb.StringValue{Value: ""}}); err != nil {
		t.Fatal(err)
	}
	got, err := server.ProcessUserCommandGet(ctx, &pbuser.ProcessUserCommandGetReq{UserID: "u1", Type: 1})
	if err != nil || len(got.CommandResp) != 1 || got.CommandResp[0].Value != "value" || got.CommandResp[0].Ex != "" {
		t.Fatal("partial update/readback failed", err)
	}
	all, err := server.ProcessUserCommandGetAll(ctx, &pbuser.ProcessUserCommandGetAllReq{UserID: "u1"})
	if err != nil || len(all.CommandResp) != 1 || all.CommandResp[0].Type != 1 {
		t.Fatal("GetAll failed", err)
	}
	if _, err := server.ProcessUserCommandDelete(ctx, &pbuser.ProcessUserCommandDeleteReq{UserID: "u1", Type: 1, Uuid: "c1"}); err != nil {
		t.Fatal(err)
	}
	all, err = server.ProcessUserCommandGetAll(ctx, &pbuser.ProcessUserCommandGetAllReq{UserID: "u1"})
	if err != nil || len(all.CommandResp) != 0 {
		t.Fatal("delete not persisted")
	}
}
func TestUserCommandsAllOperationsRejectAnotherOwnerBeforeDatabase(t *testing.T) {
	for _, actor := range []string{"attacker", ""} {
		db := &commandMemoryDB{}
		server := &userServer{UserDatabase: db}
		ctx := context.WithValue(context.Background(), constant.OpUserID, actor)
		results := []error{}
		_, err := server.ProcessUserCommandAdd(ctx, &pbuser.ProcessUserCommandAddReq{UserID: "victim", Type: 1, Uuid: "id"})
		results = append(results, err)
		_, err = server.ProcessUserCommandUpdate(ctx, &pbuser.ProcessUserCommandUpdateReq{UserID: "victim", Type: 1, Uuid: "id", Value: &wrapperspb.StringValue{Value: "x"}})
		results = append(results, err)
		_, err = server.ProcessUserCommandGet(ctx, &pbuser.ProcessUserCommandGetReq{UserID: "victim", Type: 1})
		results = append(results, err)
		_, err = server.ProcessUserCommandGetAll(ctx, &pbuser.ProcessUserCommandGetAllReq{UserID: "victim"})
		results = append(results, err)
		for _, err := range results {
			if err == nil {
				t.Fatal("cross-owner command operation succeeded")
			}
		}
		if db.calls != 0 {
			t.Fatal("unauthorized operation reached database")
		}
	}
}
func TestUserCommandPersistenceErrorsDoNotReturnSuccess(t *testing.T) {
	db := &commandMemoryDB{fail: true}
	server := &userServer{UserDatabase: db}
	ctx := context.WithValue(context.Background(), constant.OpUserID, "u1")
	_, addErr := server.ProcessUserCommandAdd(ctx, &pbuser.ProcessUserCommandAddReq{UserID: "u1", Type: 1, Uuid: "id"})
	_, updateErr := server.ProcessUserCommandUpdate(ctx, &pbuser.ProcessUserCommandUpdateReq{UserID: "u1", Type: 1, Uuid: "id", Value: &wrapperspb.StringValue{Value: "x"}})
	_, getErr := server.ProcessUserCommandGet(ctx, &pbuser.ProcessUserCommandGetReq{UserID: "u1", Type: 1})
	_, allErr := server.ProcessUserCommandGetAll(ctx, &pbuser.ProcessUserCommandGetAllReq{UserID: "u1"})
	for _, err := range []error{addErr, updateErr, getErr, allErr} {
		if err == nil {
			t.Fatal("storage failure reported success")
		}
	}
}
