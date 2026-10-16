// Copyright © 2023 OpenIM open source community. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dbconn

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/OpenIMSDK/tools/mw/specialerror"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func NewMysqlGormDB() (*gorm.DB, error) {
	cfg := config.Config.Mysql
	if cfg.Username == nil || cfg.Password == nil || cfg.Database == nil ||
		cfg.Address == nil || len(*cfg.Address) == 0 || cfg.LogLevel == nil ||
		cfg.SlowThreshold == nil || cfg.MaxLifeTime == nil ||
		cfg.MaxOpenConn == nil || cfg.MaxIdleConn == nil {
		return nil, fmt.Errorf("incomplete MySQL configuration")
	}
	connection := mysqlDriver.NewConfig()
	connection.User, connection.Passwd = *cfg.Username, *cfg.Password
	connection.Net, connection.Addr, connection.DBName = "tcp", (*cfg.Address)[0], *cfg.Database
	connection.ParseTime, connection.Loc = true, time.Local
	connection.Params = map[string]string{"charset": "utf8mb4"}
	connection.Timeout = 10 * time.Second
	sqlLogger := log.NewSqlLogger(logger.LogLevel(*config.Config.Mysql.LogLevel), true, time.Duration(*config.Config.Mysql.SlowThreshold)*time.Millisecond)
	db, err := openMysqlDatabase(connection, parameterizedLogger{Interface: sqlLogger})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetConnMaxLifetime(time.Second * time.Duration(*config.Config.Mysql.MaxLifeTime))
	sqlDB.SetMaxOpenConns(*config.Config.Mysql.MaxOpenConn)
	sqlDB.SetMaxIdleConns(*config.Config.Mysql.MaxIdleConn)
	return db, nil
}

func mysqlDatabaseIdentifier(name string) (string, error) {
	if name == "" || utf8.RuneCountInString(name) > 64 ||
		strings.TrimSpace(name) != name || strings.ContainsAny(name, "\x00/\\.") {
		return "", fmt.Errorf("invalid MySQL database name")
	}
	return "`" + strings.ReplaceAll(name, "`", "``") + "`", nil
}

func closeMysqlPool(db *gorm.DB) {
	if db != nil {
		if pool, err := db.DB(); err == nil {
			_ = pool.Close()
		}
	}
}

// Existing schemas require access only to that schema. Create it through an
// unselected connection only on MySQL's explicit unknown-database error; an
// authentication or authorization failure must never trigger a wider retry.
func openMysqlDatabase(connection *mysqlDriver.Config, sqlLogger logger.Interface) (*gorm.DB, error) {
	identifier, err := mysqlDatabaseIdentifier(connection.DBName)
	if err != nil {
		return nil, err
	}
	open := func(cfg *mysqlDriver.Config) (*gorm.DB, error) {
		return gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: sqlLogger})
	}
	db, err := open(connection)
	if err == nil {
		return db, nil
	}
	closeMysqlPool(db)
	var failure *mysqlDriver.MySQLError
	if !errors.As(err, &failure) || failure.Number != 1049 {
		return nil, err
	}
	bootstrap := connection.Clone()
	bootstrap.DBName = ""
	initial, err := open(bootstrap)
	if err != nil {
		closeMysqlPool(initial)
		return nil, err
	}
	err = initial.Exec("CREATE DATABASE IF NOT EXISTS " + identifier + " DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci").Error
	closeMysqlPool(initial)
	if err != nil {
		return nil, fmt.Errorf("initialize configured database: %w", err)
	}
	db, err = open(connection)
	if err != nil {
		closeMysqlPool(db)
		return nil, err
	}
	return db, nil
}

func NewGormDB() (*gorm.DB, error) {
	specialerror.AddReplace(gorm.ErrRecordNotFound, errs.ErrRecordNotFound)
	specialerror.AddErrHandler(replaceDuplicateKey)
	return NewMysqlGormDB()
}

func replaceDuplicateKey(err error) errs.CodeError {
	if IsMysqlDuplicateKey(err) {
		return errs.ErrDuplicateKey
	}
	return nil
}

func IsMysqlDuplicateKey(err error) bool {
	if mysqlErr, ok := err.(*mysqlDriver.MySQLError); ok {
		return mysqlErr.Number == 1062
	}
	return false
}
