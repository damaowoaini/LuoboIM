import { getUserLoginRecords } from "@/api/login";
import { useOwnedLoginRecords } from "@/core/useOwnedLoginRecords";
import "./styles.scss";

interface UserLoginRecordListProps {
  userID: string;
}

const pageSize = 10;
async function loadRecords(userID: string, page: number, current: () => boolean) {
  const { data } = await getUserLoginRecords({ userID, pagination: { pageNumber: page, showNumber: pageSize } }, current);
  const records = data?.records ?? [];
  if (!Array.isArray(records) || records.some(record => record.userID !== userID))
    throw new Error("Login record identity mismatch");
  return { records, total: data?.total ?? 0 };
}

const UserLoginRecordList = ({ userID }: UserLoginRecordListProps) => {
  const { allowed: hasPermission, page: currentPage, setPage: handlePageChange, data, loading, failed } =
    useOwnedLoginRecords(userID, loadRecords);
  const records = data?.records ?? [];
  const total = data?.total ?? 0;

  const formatLoginTime = (timestamp: number) => {
    if (!timestamp) return "-";
    const date = new Date(timestamp);
    return date.toLocaleString("zh-CN", {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
  };

  if (!hasPermission) {
    return (
      <div className="no-permission">
        <div className="empty-icon">🔒</div>
        <div>仅管理员可查看登录记录</div>
      </div>
    );
  }

  if (failed) return <div role="status">登录记录暂时无法加载。</div>;

  if (loading) {
    return <div className="login-records-loading">加载中...</div>;
  }

  return (
    <div className="user-login-records">
      <div className="records-header">
        <span className="total-count">共 {total} 条登录记录</span>
      </div>

      {records.length === 0 ? (
        <div className="no-records">
          <div className="empty-icon">📝</div>
          <div>暂无登录记录</div>
        </div>
      ) : (
        <>
          <div className="records-list">
            {records.map((record, index) => (
              <div
                key={`${record.deviceID}-${record.loginTime}-${index}`}
                className={`record-card ${record.isMultiDevice ? "multi-device-card" : ""}`}
              >
                {record.isMultiDevice && (
                  <div className="multi-device-tag">
                    ⚠️ 同设备多账户登录 ({record.otherUserIDs.length} 个)
                  </div>
                )}

                <div className="record-content">
                  <div className="record-row">
                    <div className="record-item">
                      <span className="item-icon">🕐</span>
                      <div className="item-content">
                        <div className="item-label">登录时间</div>
                        <div className="item-value time-value">
                          {formatLoginTime(record.loginTime)}
                        </div>
                      </div>
                    </div>

                    <div className="record-item">
                      <span className="item-icon">📱</span>
                      <div className="item-content">
                        <div className="item-label">设备型号</div>
                        <div className="item-value device-value">
                          {record.deviceModel || record.platform}
                        </div>
                      </div>
                    </div>
                  </div>

                  <div className="record-row">
                    <div className="record-item">
                      <span className="item-icon">🌐</span>
                      <div className="item-content">
                        <div className="item-label">IP 地址</div>
                        <div className="item-value ip-value">{record.ip}</div>
                      </div>
                    </div>

                    <div className="record-item">
                      <span className="item-icon">📍</span>
                      <div className="item-content">
                        <div className="item-label">登录地区</div>
                        <div className="item-value location-value">
                          {record.location || "未知"}
                        </div>
                      </div>
                    </div>
                  </div>
                </div>
              </div>
            ))}
          </div>

          {/* 分页 */}
          {total > pageSize && (
            <div className="pagination">
              <button
                className="page-btn"
                disabled={currentPage === 1}
                onClick={() => handlePageChange(currentPage - 1)}
              >
                ← 上一页
              </button>

              <span className="page-info">
                {currentPage} / {Math.ceil(total / pageSize)}
              </span>

              <button
                className="page-btn"
                disabled={currentPage >= Math.ceil(total / pageSize)}
                onClick={() => handlePageChange(currentPage + 1)}
              >
                下一页 →
              </button>
            </div>
          )}
        </>
      )}
    </div>
  );
};

export default UserLoginRecordList;

