//go:build js && wasm

package indexdb

import (
	"context"
	"github.com/openimsdk/openim-sdk-core/v3/wasm/exec"
)

func (i *LocalChatLogs) CaptureOwnReadTimeBinding(ctx context.Context, dataDir string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	result, err := exec.Exec(i.loginUserID, dataDir)
	if err != nil {
		return err
	}
	binding, ok := result.(string)
	if !ok || binding == "" {
		return exec.ErrType
	}
	i.ownReadTimeBinding = binding
	return nil
}

func (i *LocalChatLogs) ApplyOwnReadTimeDB(ctx context.Context, conversationID, request string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if i.ownReadTimeBinding == "" {
		return "", exec.ErrType
	}
	result, err := exec.Exec(conversationID, request, i.ownReadTimeBinding, i.loginUserID)
	if err != nil {
		return "", err
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	receipt, ok := result.(string)
	if !ok || receipt == "" {
		return "", exec.ErrType
	}
	return receipt, nil
}

func (i *LocalChatLogs) CaptureOwnReadTimeScopeDB(ctx context.Context, conversationID string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if i.ownReadTimeBinding == "" {
		return "", exec.ErrType
	}
	result, err := exec.Exec(conversationID, i.ownReadTimeBinding, i.loginUserID)
	if err != nil {
		return "", err
	}
	value, ok := result.(string)
	if !ok || value == "" {
		return "", exec.ErrType
	}
	return value, nil
}
