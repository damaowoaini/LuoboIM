import {
  deleteSensitiveWord,
  reloadSensitiveWord,
  searchSensitiveWord,
  updateSensitiveWord,
} from '@/services/server/sensitive_word';
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import type { ActionType, ProColumns } from '@ant-design/pro-components';
import { PageContainer, ProTable } from '@ant-design/pro-components';
import { Button, message, Popconfirm, Space, Switch } from 'antd';
import { useRef, useState } from 'react';
import type { SensitiveWordItem } from './data';
import SensitiveWordModal from './SensitiveWordModal';

const SensitiveWordManage = () => {
  const actionRef = useRef<ActionType>();
  const [modalVisible, setModalVisible] = useState(false);
  const [editingWord, setEditingWord] = useState<SensitiveWordItem | undefined>();

  const reload = () => {
    actionRef.current?.reload();
  };

  const handleAdd = () => {
    setEditingWord(undefined);
    setModalVisible(true);
  };

  const handleReload = async () => {
    try {
      await reloadSensitiveWord();
      message.success('缓存刷新成功！');
    } catch (error) {
      message.error('缓存刷新失败！');
    }
  };

  const handleDelete = async (ids: string[]) => {
    try {
      await deleteSensitiveWord(ids);
      message.success('删除成功！');
      reload();
    } catch (error) {
      message.error('删除失败！');
    }
  };

  const handleStatusChange = async (record: SensitiveWordItem, checked: boolean) => {
    try {
      await updateSensitiveWord({
        id: record.id,
        status: checked ? 1 : 0,
      });
      message.success('状态更新成功！');
      reload();
    } catch (error) {
      message.error('状态更新失败！');
    }
  };

  const columns: ProColumns<SensitiveWordItem>[] = [
    {
      key: 'index',
      dataIndex: 'index',
      valueType: 'indexBorder',
      width: 60,
    },
    {
      title: '敏感词',
      dataIndex: 'word',
      key: 'word',
      align: 'center',
      formItemProps: {
        rules: [
          {
            required: true,
            message: '请输入敏感词',
          },
        ],
      },
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      align: 'center',
      width: 120,
      hideInSearch: true,
      render: (_, record) => (
        <Switch
          checked={record.status === 1}
          onChange={(checked) => handleStatusChange(record, checked)}
          checkedChildren="启用"
          unCheckedChildren="禁用"
        />
      ),
    },
    {
      title: '创建时间',
      dataIndex: 'createTime',
      key: 'createTime',
      valueType: 'dateTime',
      hideInSearch: true,
      align: 'center',
      width: 180,
    },
    {
      title: '更新时间',
      dataIndex: 'updateTime',
      key: 'updateTime',
      valueType: 'dateTime',
      hideInSearch: true,
      align: 'center',
      width: 180,
    },
    {
      title: '操作',
      valueType: 'option',
      key: 'option',
      align: 'center',
      width: 100,
      render: (_, record) => (
        <Space>
          <Popconfirm
            title="确定要删除该敏感词吗？"
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
      <ProTable<SensitiveWordItem>
        columns={columns}
        actionRef={actionRef}
        cardBordered
        request={async (params) => {
          const { current, pageSize, word } = params;
          const res = await searchSensitiveWord({
            keyword: word || '',
            pageNumber: current || 1,
            showNumber: pageSize || 20,
          });
          return {
            data: res.data.words || [],
            success: true,
            total: res.data.total || 0,
          };
        }}
        editable={{
          type: 'multiple',
        }}
        columnsState={{
          persistenceKey: 'pro-table-singe-demos',
          persistenceType: 'localStorage',
        }}
        rowKey="id"
        search={{
          labelWidth: 'auto',
        }}
        options={{
          setting: {
            listsHeight: 400,
          },
        }}
        pagination={{
          pageSize: 20,
          showSizeChanger: true,
        }}
        dateFormatter="string"
        headerTitle="敏感词管理"
        toolBarRender={() => [
          <Button key="reload" icon={<ReloadOutlined />} onClick={handleReload}>
            刷新缓存
          </Button>,
          <Button key="button" icon={<PlusOutlined />} onClick={handleAdd} type="primary">
            添加敏感词
          </Button>,
        ]}
      />
      <SensitiveWordModal
        visible={modalVisible}
        editingWord={editingWord}
        onCancel={() => {
          setModalVisible(false);
          setEditingWord(undefined);
        }}
        onSuccess={() => {
          setModalVisible(false);
          setEditingWord(undefined);
          reload();
        }}
      />
    </PageContainer>
  );
};

export default SensitiveWordManage;
