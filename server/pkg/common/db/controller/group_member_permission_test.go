package controller

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
)

type permissionMemberStore struct {
	relation.GroupMemberModelInterface
	ctx   context.Context
	group string
	ids   []string
	rows  []*relation.GroupMemberModel
	err   error
}

func (s *permissionMemberStore) FindForPermission(ctx context.Context, group string, ids []string) ([]*relation.GroupMemberModel, error) {
	s.ctx, s.group, s.ids = ctx, group, append([]string(nil), ids...)
	return s.rows, s.err
}

func TestGroupPermissionReaderBypassesCacheAndPreservesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &permissionMemberStore{rows: []*relation.GroupMemberModel{{GroupID: "synthetic-g", UserID: "synthetic-u", RoleLevel: 20}}}
	// A nil cache intentionally makes accidental cache dispatch fail, while
	// the authoritative table reader remains a real controller dependency.
	db := &groupDatabase{groupMemberDB: store}
	rows, err := db.FindGroupMembersForPermission(ctx, "synthetic-g", []string{"synthetic-u"})
	if err != nil || !reflect.DeepEqual(rows, store.rows) || store.ctx != ctx || store.group != "synthetic-g" || !reflect.DeepEqual(store.ids, []string{"synthetic-u"}) {
		t.Fatal("permission reader lost its exact table query/context", err)
	}
	store.err = errors.New("synthetic direct read failure")
	if _, err := db.FindGroupMembersForPermission(ctx, "synthetic-g", []string{"synthetic-u"}); !errors.Is(err, store.err) {
		t.Fatal("permission read failure was swallowed", err)
	}
}
