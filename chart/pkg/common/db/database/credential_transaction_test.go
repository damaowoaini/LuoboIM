package database

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func credentialTestDB(t *testing.T) (ChatDatabaseInterface, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	return NewChatDatabase(db), mock
}

func TestCredentialTransactionPasswordCodeAndOutboxCommitTogether(t *testing.T) {
	for _, failOutbox := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "outbox failure rolls back password and code"}[failOutbox], func(t *testing.T) {
			db, mock := credentialTestDB(t)
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `accounts`").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("UPDATE `verify_codes` SET `used`=\\? WHERE id = \\? AND used = \\?").
				WithArgs(true, uint(42), false).WillReturnResult(sqlmock.NewResult(0, 1))
			job := mock.ExpectExec("INSERT INTO `credential_revocations`")
			if failOutbox {
				job.WillReturnError(errors.New("outbox unavailable"))
				mock.ExpectRollback()
			} else {
				job.WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			err := db.UpdatePasswordAndDeleteVerifyCode(context.Background(), "u1", "0123456789abcdef0123456789abcdef", 42)
			if (err != nil) != failOutbox {
				t.Fatalf("wrong transaction result: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCredentialTransactionReplayedCodeCannotChangePassword(t *testing.T) {
	db, mock := credentialTestDB(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `accounts`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `verify_codes`").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	if err := db.UpdatePasswordAndDeleteVerifyCode(context.Background(), "u1", "0123456789abcdef0123456789abcdef", 42); err == nil {
		t.Fatal("replayed OTP committed password update")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialTransactionLoginConsumesOnSameTransaction(t *testing.T) {
	db, mock := credentialTestDB(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `user_login_records`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE `verify_codes`").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	codeID := uint(42)
	if err := db.LoginRecord(context.Background(), &table.UserLoginRecord{UserID: "u1"}, &codeID); err == nil {
		t.Fatal("replayed code created a successful login record")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialTransactionReplayedCodeCannotRegister(t *testing.T) {
	db, mock := credentialTestDB(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `verify_codes`").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	if err := db.RegisterUser(context.Background(), &table.Register{}, &table.Account{}, &table.Attribute{}, 42); err == nil {
		t.Fatal("replayed OTP registered an account")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
