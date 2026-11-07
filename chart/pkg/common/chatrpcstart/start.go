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

package chatrpcstart

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/OpenIMSDK/chat/pkg/common/rpcmiddleware"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"google.golang.org/grpc/status"
	"net"
	"reflect"
	"strconv"
	"strings"
	"syscall"

	"github.com/OpenIMSDK/chat/pkg/common/config"
	chatMw "github.com/OpenIMSDK/chat/pkg/common/mw"
	"github.com/OpenIMSDK/chat/pkg/discovery_register"
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/network"
	"github.com/OpenIMSDK/tools/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// StartupError retains the original startup error chain while exposing only a
// finite diagnostic. It does not authorize readiness or change startup calls.
type StartupError struct {
	stage string
	cause error
}

const startupErrorPrefix = "LUOBO_CHART_STARTUP_V1 "

type startupCause struct {
	Kind        string `json:"kind"`
	MySQLNumber int    `json:"mysqlNumber"`
	Errno       int    `json:"errno"`
	GRPCCode    int    `json:"grpcCode"`
	SDKCode     int    `json:"sdkCode"`
	RedisPrefix string `json:"redisPrefix"`
}
type startupDiagnostic struct {
	Version   string         `json:"version"`
	Stage     string         `json:"stage"`
	Causes    []startupCause `json:"causes"`
	Truncated bool           `json:"truncated"`
}

func startupStage(stage string) string {
	switch stage {
	case "discovery", "register-ip", "rpc-init", "rpc-register", "listen", "serve":
		return stage
	default:
		return "unknown"
	}
}
func unknownStartupCause() startupCause {
	return startupCause{Kind: "unknown", GRPCCode: -1, RedisPrefix: "none"}
}
func unknownStartupDiagnostic(stage string, truncated bool) startupDiagnostic {
	return startupDiagnostic{Version: "chart-startup-error-v1", Stage: startupStage(stage), Causes: []startupCause{unknownStartupCause()}, Truncated: truncated}
}
func startupTypedCause(err error) (startupCause, bool) {
	row := unknownStartupCause()
	switch err {
	case context.Canceled:
		row.Kind = "context-canceled"
		return row, true
	case context.DeadlineExceeded:
		row.Kind = "context-deadline"
		return row, true
	}
	switch value := err.(type) {
	case *mysqlDriver.MySQLError:
		if value != nil {
			row.Kind = "mysql"
			row.MySQLNumber = int(value.Number)
			return row, true
		}
	case syscall.Errno:
		if value > 0 && value <= 4095 {
			row.Kind = "errno"
			row.Errno = int(value)
			return row, true
		}
	case interface{ GRPCStatus() *status.Status }:
		if value := value.GRPCStatus(); value != nil {
			code := int(value.Code())
			if code >= 0 && code <= 16 {
				row.Kind = "grpc"
				row.GRPCCode = code
				return row, true
			}
		}
	case errs.CodeError:
		code := value.Code()
		switch code {
		case errs.ServerInternalError, errs.UnknownCode, errs.ArgsError, errs.NoPermissionError,
			errs.DuplicateKeyError, errs.RecordNotFoundError, errs.UserIDNotFoundError,
			errs.DatabaseError, errs.NetworkError, errs.DataError:
			row.Kind = "sdk"
			row.SDKCode = code
			return row, true
		}
	}
	// The fixed go-redis version's internal protocol error is a string value.
	// Reading its value avoids invoking Error() on arbitrary RedisError interfaces.
	value := reflect.ValueOf(err)
	word := ""
	protocol := false
	if value.IsValid() && value.Kind() == reflect.String && value.Type().Name() == "RedisError" && value.Type().PkgPath() == "github.com/redis/go-redis/v9/internal/proto" {
		word = value.String()
		protocol = true
	} else if value.IsValid() && value.Kind() == reflect.Pointer && !value.IsNil() && value.Type().Elem().PkgPath() == "github.com/redis/go-redis/v9/internal/proto" {
		// These exact v9.23.0 protocol types expose Error solely as their stored
		// server reply. No unknown error implementation is formatted or inspected.
		switch value.Type().Elem().Name() {
		case "LoadingError", "ReadOnlyError", "MovedError", "AskError", "ClusterDownError", "TryAgainError", "MasterDownError", "MaxClientsError", "AuthError", "PermissionError", "ExecAbortError", "OOMError", "NoReplicasError":
			word = err.Error()
			protocol = true
		}
	}
	if protocol {
		if len(word) > 64 {
			word = word[:64]
		}
		if end := strings.IndexByte(word, ' '); end >= 0 {
			word = word[:end]
		}
		row.Kind = "redis"
		row.RedisPrefix = "unknown"
		switch word {
		case "NOAUTH", "WRONGPASS", "NOPERM", "NOSCRIPT", "WRONGTYPE", "LOADING", "READONLY", "OOM", "EXECABORT", "MISCONF", "CLUSTERDOWN", "BUSY", "ERR":
			row.RedisPrefix = word
		}
		return row, true
	}
	return row, false
}
func projectStartupDiagnostic(stage string, err error) (out startupDiagnostic) {
	out = unknownStartupDiagnostic(stage, false)
	defer func() {
		if recover() != nil {
			out = unknownStartupDiagnostic(stage, true)
		}
	}()
	if err == nil {
		return out
	}
	rows := []startupCause{}
	seen := map[startupCause]bool{}
	active := map[error]bool{}
	nodes := 0
	add := func(row startupCause) bool {
		if seen[row] {
			return true
		}
		if len(rows) >= 8 {
			return false
		}
		seen[row] = true
		rows = append(rows, row)
		return true
	}
	var visit func(error, int) bool
	visit = func(current error, depth int) bool {
		if current == nil {
			return true
		}
		nodes++
		if depth > 16 || nodes > 32 || !reflect.TypeOf(current).Comparable() || active[current] {
			return false
		}
		active[current] = true
		defer delete(active, current)
		var children []error
		switch value := current.(type) {
		case interface{ Unwrap() []error }:
			children = value.Unwrap()
			if len(children) > 32 {
				return false
			}
		case interface{ Unwrap() error }:
			children = []error{value.Unwrap()}
		case interface{ Cause() error }:
			children = []error{value.Cause()}
		}
		row, known := startupTypedCause(current)
		if known && !add(row) {
			return false
		}
		hasChild := false
		for _, child := range children {
			if child != nil {
				hasChild = true
				if !visit(child, depth+1) {
					return false
				}
			}
		}
		return known || hasChild || add(unknownStartupCause())
	}
	if !visit(err, 0) {
		return unknownStartupDiagnostic(stage, true)
	}
	if len(rows) > 0 {
		out.Causes = rows
	}
	return out
}
func startupFailure(stage string, cause error) error {
	if cause == nil {
		return nil
	}
	return &StartupError{stage: stage, cause: cause}
}

// Stage returns a closed startup stage; invalid zero values remain unknown.
func (e *StartupError) Stage() string {
	if e == nil {
		return "unknown"
	}
	return startupStage(e.stage)
}

// Unwrap preserves the original errs.Wrap/utils.Wrap1 chain and its cause.
func (e *StartupError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}
func (e *StartupError) Error() string {
	stage := "unknown"
	var cause error
	if e != nil {
		stage = e.stage
		cause = e.cause
	}
	value := projectStartupDiagnostic(stage, cause)
	raw, err := json.Marshal(value)
	if err != nil {
		raw = []byte(`{"version":"chart-startup-error-v1","stage":"unknown","causes":[{"kind":"unknown","mysqlNumber":0,"errno":0,"grpcCode":-1,"sdkCode":0,"redisPrefix":"none"}],"truncated":true}`)
	}
	return startupErrorPrefix + string(raw)
}

func Start(rpcPort int, rpcRegisterName string, prometheusPort int, rpcFn func(client discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) error, options ...grpc.ServerOption) error {
	fmt.Println("start", rpcRegisterName, "server, port: ", rpcPort, "prometheusPort:", prometheusPort, ", OpenIM version: ", config.Version)

	var zkClient discoveryregistry.SvcDiscoveryRegistry
	zkClient, err := discovery_register.NewDiscoveryRegister(config.Config.Envs.Discovery)
	/*
		zkClient, err := openKeeper.NewClient(config.Config.Zookeeper.ZkAddr, config.Config.Zookeeper.Schema,
			openKeeper.WithFreq(time.Hour), openKeeper.WithUserNameAndPassword(config.Config.Zookeeper.Username,
				config.Config.Zookeeper.Password), openKeeper.WithRoundRobin(), openKeeper.WithTimeout(10), openKeeper.WithLogger(log.NewZkLogger()))*/if err != nil {
		return startupFailure("discovery", errs.Wrap(err))
	}
	// defer zkClient.CloseZK()
	defer zkClient.Close()
	zkClient.AddOption(chatMw.AddUserType(), rpcmiddleware.GrpcClient(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	registerIP, err := network.GetRpcRegisterIP(config.Config.Rpc.RegisterIP)
	if err != nil {
		return startupFailure("register-ip", utils.Wrap1(err))
	}
	srv := grpc.NewServer(append(options, rpcmiddleware.GrpcServer())...)
	defer srv.GracefulStop()
	err = rpcFn(zkClient, srv)
	if err != nil {
		return startupFailure("rpc-init", errs.Wrap(err))
	}
	err = zkClient.Register(rpcRegisterName, registerIP, rpcPort, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return startupFailure("rpc-register", utils.Wrap1(err))
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(network.GetListenIP(config.Config.Rpc.ListenIP), strconv.Itoa(rpcPort)))
	if err != nil {
		return startupFailure("listen", utils.Wrap1(err))
	}
	defer listener.Close()
	return startupFailure("serve", utils.Wrap1(srv.Serve(listener)))
}
