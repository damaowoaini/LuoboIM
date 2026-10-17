import { getUserLatestLoginRecord } from "@/api/login";
import { useOwnedLoginRecords } from "@/core/useOwnedLoginRecords";
import "./styles.scss";

interface UserLoginInfoProps {
  userID: string;
  showInline?: boolean; // 是否内联显示（在列表中）
  onClick?: () => void; // 点击事件回调
}

async function loadLatest(userID: string, _page: number, current: () => boolean) {
  const { data } = await getUserLatestLoginRecord({ userID }, current);
  const record = data?.record ?? null;
  if (record && record.userID !== userID) throw new Error("Login record identity mismatch");
  return record;
}

const UserLoginInfo = ({ userID, showInline = false, onClick }: UserLoginInfoProps) => {
  const { allowed: hasPermission, loading, failed, data: loginInfo } = useOwnedLoginRecords(userID, loadLatest);

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

  if (!hasPermission) return null;
  if (loading) return <div className="login-info-loading">加载中...</div>;
  if (failed) return <div role="status">登录信息暂时无法加载。</div>;
  if (!loginInfo) return null;

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

