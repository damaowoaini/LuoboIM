import { useEffect, useState } from "react";
import { getUserLoginRecords } from "@/api/login";
import { useUserStore } from "@/store";
import "./styles.scss";

interface UserLoginRecordListProps {
  userID: string;
}

const UserLoginRecordList = ({ userID }: UserLoginRecordListProps) => {
  const selfInfo = useUserStore((state) => state.selfInfo);
  const [records, setRecords] = useState<API.User.UserLoginRecord[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [currentPage, setCurrentPage] = useState(1);
  const pageSize = 10;

  // 权限检查：只有 level 100 的管理员才能查看登录记录
  const hasPermission = selfInfo.level === 100;

  console.log("[UserLoginRecordList] 权限检查", {
    userID,
    currentUserLevel: selfInfo.level,
    hasPermission,
  });

  useEffect(() => {
    loadRecords();
  }, [userID, currentPage]);

  const loadRecords = async () => {
    try {
      setLoading(true);
      const res = await getUserLoginRecords({
        userID,
        pagination: {
          pageNumber: currentPage,
          showNumber: pageSize,
        },
      });
      if (res.data) {
        setRecords(res.data.records || []);
        setTotal(res.data.total || 0);
      }
    } catch (error) {
      console.error("Failed to load login records:", error);
    } finally {
      setLoading(false);
    }
  };

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

  const handlePageChange = (page: number) => {
    setCurrentPage(page);
  };

  // 权限检查：非管理员不显示
  if (!hasPermission) {
    console.warn("[UserLoginRecordList] 无权限查看登录记录", {
      userID,
      currentUserLevel: selfInfo.level,
    });
    return (
      <div className="no-permission">
        <div className="empty-icon">🔒</div>
        <div>仅管理员可查看登录记录</div>
      </div>
    );
  }

  if (loading && records.length === 0) {
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

