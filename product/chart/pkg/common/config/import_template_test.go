package config_test

import (
	"bytes"
	"testing"

	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/xlsx"
	"github.com/OpenIMSDK/chat/pkg/common/xlsx/model"
)

func TestEmbeddedImportTemplateParsesOneCompleteUser(t *testing.T) {
	original := append([]byte(nil), config.ImportTemplate...)
	var empty []model.User
	if err := xlsx.ParseAll(bytes.NewReader(config.ImportTemplate), &empty); err != nil {
		t.Fatalf("parse embedded empty template: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("embedded template must have no sample accounts: got %d", len(empty))
	}
	book, err := xlsx.Open(bytes.NewReader(config.ImportTemplate))
	if err != nil {
		t.Fatalf("open embedded template: %v", err)
	}
	defer func() {
		if err := book.Close(); err != nil {
			t.Errorf("close workbook: %v", err)
		}
	}()
	sheet := (model.User{}).SheetName()
	if index, err := book.GetSheetIndex(sheet); err != nil || index < 0 {
		t.Fatalf("model worksheet must exist: index=%d err=%v", index, err)
	}
	want := model.User{
		UserID: "template-regression-user", Nickname: "Template regression",
		FaceURL: "https://example.invalid/template.png", Birth: "2001-02-03", Gender: "1",
		AreaCode: "+86", PhoneNumber: "13800000000", Email: "template@example.invalid",
		Account: "template-regression-account", Password: "Synthetic-Template-Only-Password",
	}
	headers := []string{"nickname", "face_url", "birth", "gender", "area_code", "phone_number", "email", "account", "password", "user_id"}
	values := []string{want.Nickname, want.FaceURL, want.Birth, want.Gender, want.AreaCode, want.PhoneNumber, want.Email, want.Account, want.Password, want.UserID}
	for i, header := range headers {
		got, err := book.GetCellValue(sheet, xlsx.GetAxis(i+1, 1))
		if err != nil || got != header {
			t.Fatalf("template header %d differs: %q err=%v", i+1, got, err)
		}
		if err := book.SetCellStr(sheet, xlsx.GetAxis(i+1, 2), values[i]); err != nil {
			t.Fatalf("fill template column %d: %v", i+1, err)
		}
	}
	filled, err := book.WriteToBuffer()
	if err != nil {
		t.Fatalf("serialize filled template: %v", err)
	}
	var users []model.User
	if err := xlsx.ParseAll(bytes.NewReader(filled.Bytes()), &users); err != nil {
		t.Fatalf("parse filled template with production parser: %v", err)
	}
	if len(users) != 1 || users[0] != want {
		t.Fatalf("expected one complete model.User, got %#v", users)
	}
	if !bytes.Equal(config.ImportTemplate, original) {
		t.Fatal("filling the in-memory workbook changed the embedded template")
	}
}
