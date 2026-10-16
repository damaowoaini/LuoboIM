import { useEffect, useState } from "react";
import { getUserLatestLoginRecord } from "@/api/login";
import { useUserStore } from "@/store";
import "./styles.scss";

interface UserLoginInfoProps {
  userID: string;
  showInline?: boolean; // 是否内联显示（在列表中）
  onClick?: () => void; // 点击事件回调
}

const UserLoginInfo = ({ userID, showInline = false, onClick }: UserLoginInfoProps) => {
  console.log("[UserLoginInfo] 组件初始化", { userID, showInline });
  const selfInfo = useUserStore((state) => state.selfInfo);
  const [loginInfo, setLoginInfo] = useState<API.User.UserLoginRecord | null>(null);
  const [loading, setLoading] = useState(true);

  // 权限检查：只有 level 100 的管理员才能查看登录信息
  const hasPermission = selfInfo.level === 100;

  console.log("[UserLoginInfo] 权限检查", {
    userID,
    currentUserLevel: selfInfo.level,
    hasPermission,
  });

  const loadLoginInfo = async () => {
    try {
      setLoading(true);
      console.log("[UserLoginInfo] 开始获取登录信息:", userID);
      const res = await getUserLatestLoginRecord({ userID });
      console.log("[UserLoginInfo] 获取登录信息成功:", res);
      if (res.data?.record) {
        console.log("[UserLoginInfo] 登录记录:", res.data.record);
        setLoginInfo(res.data.record);
      } else {
        console.warn("[UserLoginInfo] 没有登录记录数据，清空状态");
        // 【修复】当后端返回空数据时，清空之前的登录信息状态
        setLoginInfo(null);
      }
    } catch (error) {
      console.error("[UserLoginInfo] Failed to load login info:", error);
      // 【修复】发生错误时也要清空状态，避免显示错误的数据
      setLoginInfo(null);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    // 【修复】切换用户时立即清空旧数据，避免显示上一个用户的信息
    setLoginInfo(null);
    setLoading(true);
    loadLoginInfo();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userID]);

  const formatLoginTime = (timestamp: number) => {
    if (!timestamp) return "-";
    const date = new Date(timestamp);
    return date.toLocaleString("zh-CN", {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
    });
  };

  console.log("[UserLoginInfo] 渲染状态:", { userID, loading, hasLoginInfo: !!loginInfo });

  // 权限检查：非管理员不显示
  if (!hasPermission) {
    console.warn("[UserLoginInfo] 无权限查看登录信息", {
      userID,
      currentUserLevel: selfInfo.level,
    });
    return null;
  }

  if (loading) {
    return <div className="login-info-loading">加载中...</div>;
  }

  if (!loginInfo) {
    console.warn("[UserLoginInfo] 没有登录信息，组件不显示", userID);
    return null;
  }

  console.log("[UserLoginInfo] 准备渲染登录信息:", loginInfo);

  // 内联显示模式（在聊天头部）
  if (showInline) {
    return (
      <div 
        className={`login-info-inline ${loginInfo.isMultiDevice ? "multi-device" : ""} ${onClick ? "clickable" : ""}`}
        onClick={(e) => {
          if (onClick) {
            e.stopPropagation();
            onClick();
          }
        }}
        title="点击查看完整登录记录"
      >
        {loginInfo.isMultiDevice && <span className="warning-icon">⚠️</span>}
        <span className="device-model">{loginInfo.deviceModel || loginInfo.platform}</span>
        <span className="ip-address">{loginInfo.ip}</span>
        <span className="location">{loginInfo.location}</span>
      </div>
    );
  }

  // 完整显示模式（在详情页中）
  return (
    <div className={`user-login-info ${loginInfo.isMultiDevice ? "multi-device-warning" : ""}`}>
      <div className="login-info-header">
        <h3>最新登录信息</h3>
        {loginInfo.isMultiDevice && (
          <span className="multi-device-badge">
            ⚠️ 同设备多账户登录
          </span>
        )}
      </div>

      <div className="login-info-content">
        <div className="info-row">
          <span className="info-label">设备型号:</span>
          <span className="info-value">{loginInfo.deviceModel || "-"}</span>
        </div>

        <div className="info-row">
          <span className="info-label">平台:</span>
          <span className="info-value">{loginInfo.platform}</span>
        </div>

        <div className="info-row">
          <span className="info-label">IP 地址:</span>
          <span className="info-value">{loginInfo.ip}</span>
        </div>

        <div className="info-row">
          <span className="info-label">登录地区:</span>
          <span className="info-value">{loginInfo.location || "-"}</span>
        </div>

        <div className="info-row">
          <span className="info-label">登录时间:</span>
          <span className="info-value">{formatLoginTime(loginInfo.loginTime)}</span>
        </div>

        {loginInfo.isMultiDevice && loginInfo.otherUserIDs.length > 0 && (
          <div className="info-row multi-device-users">
            <span className="info-label">同设备其他账户:</span>
            <span className="info-value">
              {loginInfo.otherUserIDs.length} 个账户使用了相同设备
            </span>
          </div>
        )}
      </div>
    </div>
  );
};

export default UserLoginInfo;

