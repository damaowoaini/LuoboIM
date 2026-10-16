import { Descriptions, Image, Modal, Tag } from 'antd';
import moment from 'moment';
import type { ComplaintItem } from './data';

interface ComplaintDetailModalProps {
  visible: boolean;
  complaint?: ComplaintItem;
  onCancel: () => void;
}

const ComplaintDetailModal: React.FC<ComplaintDetailModalProps> = ({
  visible,
  complaint,
  onCancel,
}) => {
  if (!complaint) return null;

  // 解析证据字段（后端返回的是 JSON 字符串）
  const parseEvidence = (evidence: string): string[] => {
    try {
      return typeof evidence === 'string' ? JSON.parse(evidence) : evidence;
    } catch {
      return [];
    }
  };

  const evidenceList = parseEvidence(complaint.evidence);

  const getStatusText = (status: number) => {
    const statusMap: Record<number, JSX.Element> = {
      0: <Tag color="orange">待处理</Tag>,
      1: <Tag color="green">已处理</Tag>,
      2: <Tag color="red">已驳回</Tag>,
    };
    return statusMap[status] || <Tag>未知</Tag>;
  };

  const getTypeText = (type: number) => {
    const typeMap: Record<number, JSX.Element> = {
      1: <Tag color="blue">用户投诉</Tag>,
      2: <Tag color="purple">群组投诉</Tag>,
    };
    return typeMap[type] || <Tag>未知</Tag>;
  };

  return (
    <Modal title="投诉详情" open={visible} onCancel={onCancel} footer={null} width={800}>
      <Descriptions column={2} bordered>
        <Descriptions.Item label="投诉类型" span={2}>
          {getTypeText(complaint.complaintType)}
        </Descriptions.Item>
        <Descriptions.Item label="投诉人ID">{complaint.complaintUserID}</Descriptions.Item>
        <Descriptions.Item label="被投诉对象ID">{complaint.targetID}</Descriptions.Item>
        <Descriptions.Item label="被投诉对象名称" span={2}>
          {complaint.targetName || '-'}
        </Descriptions.Item>
        <Descriptions.Item label="投诉原因" span={2}>
          {complaint.reason}
        </Descriptions.Item>
        <Descriptions.Item label="详细描述" span={2}>
          {complaint.description || '-'}
        </Descriptions.Item>
        <Descriptions.Item label="证据" span={2}>
          {evidenceList.length > 0 ? (
            <Image.PreviewGroup>
              {evidenceList.map((url, index) => (
                <Image key={index} width={100} src={url} style={{ marginRight: 8 }} />
              ))}
            </Image.PreviewGroup>
          ) : (
            '-'
          )}
        </Descriptions.Item>
        <Descriptions.Item label="状态">{getStatusText(complaint.status)}</Descriptions.Item>
        <Descriptions.Item label="创建时间">
          {moment(complaint.createTime).format('YYYY-MM-DD HH:mm:ss')}
        </Descriptions.Item>
        {complaint.status !== 0 && (
          <>
            <Descriptions.Item label="处理人ID">{complaint.handlerID || '-'}</Descriptions.Item>
            <Descriptions.Item label="处理时间">
              {complaint.handleTime
                ? moment(complaint.handleTime).format('YYYY-MM-DD HH:mm:ss')
                : '-'}
            </Descriptions.Item>
            <Descriptions.Item label="处理结果" span={2}>
              {complaint.handleResult || '-'}
            </Descriptions.Item>
          </>
        )}
      </Descriptions>
    </Modal>
  );
};

export default ComplaintDetailModal;
