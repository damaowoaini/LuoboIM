package chat

import (
	"context"
	"fmt"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/eerrs"
	"github.com/OpenIMSDK/tools/log"
)

// 登录失败锁定：同账号+同IP、同账号两个维度计数，任一超限即拒绝。
// 只看「账号+IP」会被换 IP 绕过；只看「账号」又容易被人恶意锁号，所以账号维度阈值更高。

type loginLockPolicy struct {
	perAccountIP int64
	perAccount   int64
	window       time.Duration
}

func loginLockPolicyFromConfig() loginLockPolicy {
	c := config.Config.LoginLock
	p := loginLockPolicy{perAccountIP: 10, perAccount: 30, window: 15 * time.Minute}
	if c.MaxFailPerAccountIP > 0 {
		p.perAccountIP = c.MaxFailPerAccountIP
	}
	if c.MaxFailPerAccount > 0 {
		p.perAccount = c.MaxFailPerAccount
	}
	if c.WindowMinutes > 0 {
		p.window = time.Duration(c.WindowMinutes) * time.Minute
	}
	return p
}

func loginFailKeys(userID, ip string) (accountIP, account string) {
	return "user:" + userID + ":" + ip, "user:" + userID
}

func (o *chatSvr) ensureLoginNotLocked(ctx context.Context, userID, ip string) error {
	if o.LoginFailLock == nil {
		return nil
	}
	p := loginLockPolicyFromConfig()
	kIP, kAcc := loginFailKeys(userID, ip)
	if n, err := o.LoginFailLock.Count(ctx, kIP); err != nil {
		log.ZError(ctx, "ensureLoginNotLocked: 读取失败计数出错，放行", err, "userID", userID)
		return nil
	} else if n >= p.perAccountIP {
		return eerrs.ErrLoginLocked.Wrap(fmt.Sprintf("登录失败次数过多，请 %d 分钟后再试", int(p.window.Minutes())))
	}
	if n, err := o.LoginFailLock.Count(ctx, kAcc); err != nil {
		log.ZError(ctx, "ensureLoginNotLocked: 读取失败计数出错，放行", err, "userID", userID)
		return nil
	} else if n >= p.perAccount {
		return eerrs.ErrLoginLocked.Wrap(fmt.Sprintf("该账号登录失败次数过多，请 %d 分钟后再试", int(p.window.Minutes())))
	}
	return nil
}

func (o *chatSvr) recordLoginFailure(ctx context.Context, userID, ip string) {
	if o.LoginFailLock == nil {
		return
	}
	p := loginLockPolicyFromConfig()
	kIP, kAcc := loginFailKeys(userID, ip)
	for _, k := range []string{kIP, kAcc} {
		if _, err := o.LoginFailLock.Incr(ctx, k, p.window); err != nil {
			log.ZError(ctx, "recordLoginFailure: 记录失败计数出错", err, "userID", userID)
		}
	}
}

// clearLoginFailures 登录成功后清掉该账号+IP 的计数；账号维度保留到窗口结束，防止攻击者夹带一次成功登录清零。
func (o *chatSvr) clearLoginFailures(ctx context.Context, userID, ip string) {
	if o.LoginFailLock == nil {
		return
	}
	kIP, _ := loginFailKeys(userID, ip)
	if err := o.LoginFailLock.Reset(ctx, kIP); err != nil {
		log.ZError(ctx, "clearLoginFailures: 清除失败计数出错", err, "userID", userID)
	}
}
