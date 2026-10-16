import { deleteComplaint, searchComplaint } from '@/services/server/complaint';
import { EyeOutlined } from '@ant-design/icons';
import type { ActionType, ProColumns } from '@ant-design/pro-components';
import { PageContainer, ProTable } from '@ant-design/pro-components';
import { message, Popconfirm, Space, Tag } from 'antd';
import moment from 'moment';
import { useRef, useState } from 'react';
import ComplaintDetailModal from './ComplaintDetailModal';
import ComplaintHandleModal from './ComplaintHandleModal';
import type { ComplaintItem } from './data';

const ComplaintManage = () => {
  const actionRef = useRef<ActionType>();
  const [detailModalVisible, setDetailModalVisible] = useState(false);
  const [handleModalVisible, setHandleModalVisible] = useState(false);
  const [currentComplaint, setCurrentComplaint] = useState<ComplaintItem | undefined>();

  const reload = () => {
    actionRef.current?.reload();
  };

  const handleDelete = async (ids: string[]) => {
    try {
      await deleteComplaint(ids);
      message.success('删除成功！');
      reload();
    } catch (error) {
      message.error('删除失败！');
    }
  };

  const showDetail = (record: ComplaintItem) => {
    setCurrentComplaint(record);
    setDetailModalVisible(true);
  };

  const showHandle = (record: ComplaintItem) => {
    setCurrentComplaint(record);
    setHandleModalVisible(true);
  };

  const getStatusTag = (status: number) => {
    const statusMap: Record<number, { text: string; color: string }> = {
      0: { text: '待处理', color: 'orange' },
      1: { text: '已处理', color: 'green' },
      2: { text: '已驳回', color: 'red' },
    };
    const item = statusMap[status] || { text: '未知', color: 'default' };
    return <Tag color={item.color}>{item.text}</Tag>;
  };

  const getTypeTag = (type: number) => {
    const typeMap: Record<number, { text: string; color: string }> = {
      1: { text: '用户投诉', color: 'blue' },
      2: { text: '群组投诉', color: 'purple' },
    };
    const item = typeMap[type] || { text: '未知', color: 'default' };
    return <Tag color={item.color}>{item.text}</Tag>;
  };

  const columns: ProColumns<ComplaintItem>[] = [
    {
      key: 'index',
      dataIndex: 'index',
      valueType: 'indexBorder',
      width: 60,
    },
    {
      title: '投诉类型',
      dataIndex: 'complaintType',
      key: 'complaintType',
      align: 'center',
      width: 120,
      valueType: 'select',
      valueEnum: {
        1: { text: '用户投诉' },
        2: { text: '群组投诉' },
      },
      render: (_, record) => getTypeTag(record.complaintType),
    },
    {
      title: '投诉人ID',
      dataIndex: 'complaintUserID',
      key: 'complaintUserID',
      align: 'center',
      width: 150,
    },
    {
      title: '被投诉对象',
      dataIndex: 'targetName',
      key: 'targetName',
      align: 'center',
      hideInSearch: true,
      render: (_, record) => (
        <div>
          <div>{record.targetName || record.targetID}</div>
          <div style={{ fontSize: 12, color: '#999' }}>{record.targetID}</div>
        </div>
      ),
    },
    {
      title: '投诉原因',
      dataIndex: 'reason',
      key: 'reason',
      align: 'center',
      hideInSearch: true,
      ellipsis: true,
      width: 200,
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      align: 'center',
      width: 100,
      valueType: 'select',
      valueEnum: {
        0: { text: '待处理' },
        1: { text: '已处理' },
        2: { text: '已驳回' },
      },
      render: (_, record) => getStatusTag(record.status),
    },
    {
      title: '创建时间',
      dataIndex: 'createTime',
      key: 'createTime',
      valueType: 'dateTime',
      hideInSearch: true,
      align: 'center',
      width: 180,
      render: (_, record) => moment(record.createTime).format('YYYY-MM-DD HH:mm:ss'),
    },
    {
      title: '操作',
      valueType: 'option',
      key: 'option',
      align: 'center',
      width: 180,
      render: (_, record) => (
        <Space>
          <a onClick={() => showDetail(record)}>
            <EyeOutlined /> 详情
          </a>
          {record.status === 0 && <a onClick={() => showHandle(record)}>处理</a>}
          <Popconfirm
            title="确定要删除该投诉吗？"
            onConfirm={() => handleDelete([record.id])}
            okText="确定"
            cancelText="取消"
          >
            <a style={{ color: 'red' }}>删除</a>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <PageContainer>
      <ProTable<ComplaintItem>
        columns={columns}
        actionRef={actionRef}
        cardBordered
        request={async (params) => {
          const { current, pageSize, complaintUserID, complaintType, status } = params;
          const res = await searchComplaint({
            complaintUserID: complaintUserID || '',
            complaintType: complaintType ? Number(complaintType) : undefined,
            status: status !== undefined ? Number(status) : undefined,
            pageNumber: current || 1,
            showNumber: pageSize || 20,
          });
          return {
            data: res.data.complaints || [],
            success: true,
            total: res.data.total || 0,
          };
        }}
        columnsState={{
          persistenceKey: 'complaint-table',
          persistenceType: 'localStorage',
        }}
        rowKey="id"
        search={{
          labelWidth: 'auto',
        }}
        pagination={{
          pageSize: 20,
          showSizeChanger: true,
        }}
        dateFormatter="string"
        headerTitle="投诉管理"
      />
      <ComplaintDetailModal
        visible={detailModalVisible}
        complaint={currentComplaint}
        onCancel={() => {
          setDetailModalVisible(false);
          setCurrentComplaint(undefined);
        }}
      />
      <ComplaintHandleModal
        visible={handleModalVisible}
        complaint={currentComplaint}
        onCancel={() => {
          setHandleModalVisible(false);
          setCurrentComplaint(undefined);
        }}
        onSuccess={() => {
          setHandleModalVisible(false);
          setCurrentComplaint(undefined);
          reload();
        }}
      />
    </PageContainer>
  );
};

export default ComplaintManage;
