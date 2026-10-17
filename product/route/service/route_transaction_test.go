package service

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"openim-route-service/db"
)

func databaseMock(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	conn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	previous := db.DB
	db.DB, err = gorm.Open(mysql.New(mysql.Config{Conn: conn, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.DB = previous
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		conn.Close()
	})
	return mock
}
func serverRow(current, max int) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "server_id", "status", "max_users", "current_users", "domains"}).AddRow(1, "tenant-a", 1, max, current, "tenant.example")
}
func expectServerLock(mock sqlmock.Sqlmock, current, max int) {
	mock.ExpectQuery("SELECT .*server_config.*FOR UPDATE").WithArgs("tenant-a").WillReturnRows(serverRow(current, max))
	mock.ExpectExec("INSERT INTO `account_route_lock`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT .*account_route_lock.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"account_key"}).AddRow("13812345678"))
}
func expectNoBinding(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT .*user_server_binding").WithArgs("13812345678").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "server_id"}))
	mock.ExpectQuery("SELECT count.*registration_intent").WithArgs("13812345678", "bound").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
}
func expectInvite(mock sqlmock.Sqlmock, server string) {
	mock.ExpectQuery("SELECT .*invite_code.*FOR UPDATE").WithArgs("invite-a").WillReturnRows(sqlmock.NewRows([]string{"id", "code", "server_id", "status", "max_use_count", "used_count"}).AddRow(2, "invite-a", server, 1, 10, 1))
}
func TestBindingAndCountersCommitInOneTransaction(t *testing.T) {
	m := databaseMock(t)
	m.ExpectBegin()
	expectServerLock(m, 4, 10)
	expectNoBinding(m)
	expectInvite(m, "tenant-a")
	m.ExpectExec("INSERT INTO `user_server_binding`").WillReturnResult(sqlmock.NewResult(10, 1))
	m.ExpectExec("UPDATE `invite_code` SET `used_count`=used_count \\+ 1").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("UPDATE `server_config` SET `current_users`=current_users \\+ 1").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	if err := NewRouteService().BindUserToServer("13812345678", "tenant-a", "invite-a"); err != nil {
		t.Fatal(err)
	}
}
func TestCounterFailureRollsBackBinding(t *testing.T) {
	m := databaseMock(t)
	m.ExpectBegin()
	expectServerLock(m, 4, 10)
	expectNoBinding(m)
	expectInvite(m, "tenant-a")
	m.ExpectExec("INSERT INTO `user_server_binding`").WillReturnResult(sqlmock.NewResult(10, 1))
	m.ExpectExec("UPDATE `invite_code`").WillReturnError(errors.New("injected write failure"))
	m.ExpectRollback()
	if err := NewRouteService().BindUserToServer("13812345678", "tenant-a", "invite-a"); err == nil {
		t.Fatal("counter failure did not abort binding")
	}
}
func TestBindingInvitationMustBelongToSameTenant(t *testing.T) {
	m := databaseMock(t)
	m.ExpectBegin()
	expectServerLock(m, 4, 10)
	expectNoBinding(m)
	expectInvite(m, "tenant-b")
	m.ExpectRollback()
	if err := NewRouteService().BindUserToServer("13812345678", "tenant-a", "invite-a"); err == nil {
		t.Fatal("cross-tenant invitation accepted")
	}
}
func TestRepeatedIdenticalBindingDoesNotIncrementCounters(t *testing.T) {
	m := databaseMock(t)
	m.ExpectBegin()
	expectServerLock(m, 10, 10)
	m.ExpectQuery("SELECT .*user_server_binding").WithArgs("13812345678").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "server_id"}).AddRow(3, "13812345678", "tenant-a"))
	m.ExpectCommit()
	if err := NewRouteService().BindUserToServer("13812345678", "tenant-a", "invite-a"); err != nil {
		t.Fatal(err)
	}
}
func TestFullDefaultTenantRejectsNewBinding(t *testing.T) {
	m := databaseMock(t)
	m.ExpectBegin()
	expectServerLock(m, 10, 10)
	expectNoBinding(m)
	m.ExpectRollback()
	if err := NewRouteService().BindUserToServer("13812345678", "tenant-a", ""); err == nil {
		t.Fatal("default-tenant capacity bypassed")
	}
}
func TestUnavailableDatabaseIsNotTreatedAsUnregistered(t *testing.T) {
	m := databaseMock(t)
	m.ExpectQuery("SELECT .*user_server_binding").WithArgs("13812345678").WillReturnError(errors.New("injected unavailable database"))
	_, err := NewRouteService().GetServerByUserID("13812345678")
	if err == nil || errors.Is(err, ErrAccountNotBound) {
		t.Fatal("database outage misreported as missing account")
	}
}
