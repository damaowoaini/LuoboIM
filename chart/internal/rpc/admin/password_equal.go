package admin

import (
	"context"
	"github.com/OpenIMSDK/chat/pkg/common/passwordstore"
)

// passwordEqual 常量时间比较，避免通过响应耗时推测密码。
func passwordEqual(stored, given string) bool {
	valid, _, err := passwordstore.Verify(context.Background(), stored, given)
	return err == nil && valid
}
