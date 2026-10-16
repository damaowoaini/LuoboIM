import { adminSessions } from '@/core/adminSession';
import {batchSendReport,freezeBatchDraft,sameBatchDraft,unknownBatchReport,type BatchDraft,type BatchSendReport} from '@/core/batchSendResult';
import type { UserListItem } from '@/pages/business_system/user_manage/data';
import { batchSend } from '@/services/server/message_manage';
import { getUserList } from '@/services/server/user_manage';
import type { ActionType, ProColumns } from '@ant-design/pro-components';
import { PageContainer, ProTable } from '@ant-design/pro-components';
import { Alert, Button, Checkbox, FormInstance, Input, message, Modal, Tag } from 'antd';
import { useEffect, useMemo, useRef, useState } from 'react';

import { DefaultAvatar, defaultAvatarStr } from '@/utils/avatar';
import styles from './style.less';

export type SelectedRowDataItem = Record<
  number,
  {
    keys: React.Key[];
    items: UserListItem[];
  }
>;

const SendMessage = () => {
  const actionRef = useRef<ActionType>();
  const formRef = useRef<FormInstance>();
  // const [fileList, setFileList] = useState([] as any);
  const [selectedRowData, setSelectedRowData] = useState({} as SelectedRowDataItem);
  const [allSend, setAllSend] = useState(false);
  // const latestSelectData = useLatest(selectedRowData);
  const [current, setCurrent] = useState(1);
  const [content, setContent] = useState('');
  const [sending, setSending] = useState(false);
  const inFlight = useRef(false);
  const mounted=useRef(true);
  const [attempt,setAttempt]=useState<{draft:Readonly<BatchDraft>;report?:BatchSendReport}>();
  const attempted=useRef<Readonly<BatchDraft>>(),previousUnknown=useRef<Readonly<BatchDraft>>();
  useEffect(()=>{
    mounted.current=true;
    const unsubscribe=adminSessions?.onChange(()=>{attempted.current=undefined;previousUnknown.current=undefined;setAttempt(undefined);setContent('');setSelectedRowData({});setAllSend(false);setSending(false);});
    return()=>{mounted.current=false;unsubscribe?.();};
  },[]);

  const columns: ProColumns<UserListItem>[] = useMemo(
    () => [
      {
        title: '用户头像',
        dataIndex: 'faceURL',
        valueType: 'avatar',
        width: 64,
        key: 'faceURL',
        hideInSearch: true,
        align: 'center',
        render(_, record) {
          return <DefaultAvatar faceURL={record.faceURL} nickname={record.nickname} />;
        },
      },
      {
        title: '昵称',
        key: 'nickname',
        dataIndex: 'nickname',
        width: 128,
        hideInSearch: true,
        align: 'left',
        render(_, record) { return <span className={styles.recipient_nickname} title={record.nickname}>{record.nickname}</span>; },
      },
      {
        title: '用户ID',
        key: 'userID',
        dataIndex: 'userID',
        width: 150,
        fixed: 'right',
        align: 'left',
        render(_, record) { return <span className={styles.recipient_id}>{record.userID}</span>; },
        // formItemProps: (form, config) => {
        //   return { ...config, label: null, name: 'userID', style: { marginBottom: 0 } };
        // },
        // renderFormItem: () => {
        //   return (
        //     <>
        //       <div className="font-medium ml-3 mb-4">选择接收用户</div>
        //       <Input
        //         // value={value}
        //         onChange={(e) => formRef.current?.setFieldValue('userID', e.target.value)}
        //         className={styles.search_input}
        //         prefix={<SearchOutlined />}
        //         placeholder="输入用户ID或昵称"
        //         bordered={false}
        //       />
        //     </>
        //   );
        // },
        // editable: false,
      },
    ],
    [],
  );

  // const getPicInfo = (file: RcFile): Promise<HTMLImageElement> => {
  //   return new Promise((resolve) => {
  //     const _URL = window.URL || window.webkitURL;
  //     const img = new Image();
  //     img.onload = function () {
  //       resolve(img);
  //     };
  //     img.src = _URL.createObjectURL(file);
  //   });
  // };

  // const uploadButton = (
  //   <div>
  //     <PlusOutlined />
  //     <div style={{ marginTop: 8 }}>Upload</div>
  //   </div>
  // );

  // const customRequest = (options: UploadRequestOption) => {
  //   switchUpload(options)
  //     .then(async ({ data }) => {
  //       const tmpList = [...fileList];
  //       const { width, height } = await getPicInfo(options.file as RcFile);
  //       tmpList.push({
  //         ...(options.file as RcFile),
  //         uuid: (options.file as RcFile).uid,
  //         url: data.URL,
  //         width,
  //         height,
  //       });
  //       setFileList(tmpList);
  //     })
  // };

  const onSelectChange = (keys: React.Key[], items: UserListItem[]) => {
    const tmpData = { ...selectedRowData };
    tmpData[actionRef.current!.pageInfo!.current] = {
      keys,
      items,
    };
    setSelectedRowData(tmpData);
  };

  const getItems = useMemo(
    () =>
      Object.values(selectedRowData)
        .map((data) => data.items)
        .flat(),
    [selectedRowData],
  );

  const onCancelSelect = (value: string) => {
    const tmpData = { ...selectedRowData };
    // eslint-disable-next-line guard-for-in
    for (const key in tmpData) {
      const keyIdx = tmpData[key].keys.findIndex((k) => k === value);
      const itemdIdx = tmpData[key].items.findIndex((item) => item.userID === value);
      if (keyIdx !== -1 && itemdIdx !== -1) {
        tmpData[key].keys.splice(keyIdx, 1);
        tmpData[key].items.splice(itemdIdx, 1);
      }
    }
    setSelectedRowData(tmpData);
    setCurrent(current + 1);
    setTimeout(() => setCurrent(actionRef.current!.pageInfo!.current));
  };

  const send2User = () => {
    // let pictureElem;
    // if (fileList.length > 0) {
    //   pictureElem = {
    //     sourcePicture: {
    //       ...fileList[0],
    //     },
    //     bigPicture: {
    //       ...fileList[0],
    //     },
    //     snapshotPicture: {
    //       ...fileList[0],
    //     },
    //   };
    // }

    if (inFlight.current||attempted.current) return;
    if (!content.trim()) { message.warning('请输入消息内容'); return; }
    const recipients = [...new Set(getItems.map(item => item.userID))];
    if (!allSend && !recipients.length) { message.warning('请选择接收用户'); return; }
    const generation = adminSessions?.generation;
    const payload = freezeBatchDraft({
      sendID: adminSessions?.current?.imUserID || '',
      isSendAll: allSend,
      recvIDList: recipients,
      text: content,
    });
    if(previousUnknown.current&&sameBatchDraft(previousUnknown.current,payload)){
      message.warning('这与上次结果未确认的整批草稿一致，请先核实消息记录，不能直接重发整批。');return;
    }
    const submit = async () => {
      if (inFlight.current || attempted.current || !mounted.current || generation !== adminSessions?.generation) return;
      inFlight.current = true;
      attempted.current=payload;setAttempt({draft:payload});
      setSending(true);
      try {
        const {data}=await batchSend(payload);
        if(mounted.current&&generation===adminSessions?.generation){
          const report=batchSendReport(data,payload);setAttempt({draft:payload,report});
          if(report.state==='confirmed')message.success(report.notice);else message.warning(report.notice);
        }
      } catch {
        if(mounted.current&&generation===adminSessions?.generation){
          const report=unknownBatchReport(payload);setAttempt({draft:payload,report});message.warning(report.notice);
        }
      } finally {
        inFlight.current = false;
        if (mounted.current&&generation === adminSessions?.generation) setSending(false);
      }
    };
    if (payload.isSendAll || recipients.length > 1) {
      Modal.confirm({
        title: payload.isSendAll ? '向所有已注册用户发送消息？' : `向${recipients.length}位用户发送消息？`,
        content: <div style={{ whiteSpace:'pre-wrap', overflowWrap:'anywhere', maxHeight:240, overflow:'auto' }}>{payload.text}</div>,
        okText: '确认发送', cancelText: '取消', onOk: submit,
      });
    } else void submit();
  };

  return (
    <PageContainer>
      {attempt&&<Alert style={{marginBottom:20}} showIcon type={attempt.report?.state==='confirmed'?'success':'warning'}
        message={attempt.report?.notice||'正在发送此草稿，请勿重复提交'}
        description={<div>
          <p>原接收范围：{attempt.draft.isSendAll?'所有已注册用户':`${attempt.draft.recvIDList.length}位指定用户`}。本页仅在内存保留本次草稿和回执，刷新后请先核实消息记录。</p>
          <p style={{whiteSpace:'pre-wrap',overflowWrap:'anywhere'}}>原消息：{attempt.draft.text}</p>
          {!!attempt.report?.confirmed.length&&<p>已确认用户：{attempt.report.confirmed.map(item=>item.recvID).join('、')}</p>}
          {!!attempt.report?.unconfirmedIDs.length&&<p style={{overflowWrap:'anywhere'}}>未确认用户：{attempt.report.unconfirmedIDs.join('、')}。请先查询记录，不能把服务端错误当作可安全重发证明。</p>}
          {attempt.report&&<Button onClick={()=>{
            const generation=adminSessions?.generation;
            Modal.confirm({title:'已核实上次结果，开始编辑新的发送草稿？',content:'此操作不会重发。请先核实上次未确认对象的消息记录；原文保留供编辑，原整批未确认草稿仍禁止直接重复发送。',okText:'开始编辑',cancelText:'继续核实',onOk:()=>{
              if(!mounted.current||generation!==adminSessions?.generation)return;
              previousUnknown.current=attempt.report?.state==='confirmed'?undefined:attempt.draft;attempted.current=undefined;setAttempt(undefined);
            }});
          }}>核实后编辑新的发送草稿</Button>}
        </div>}/>}
      <div className={styles.send_layout_container}>
      <div className={styles.send_layout}>
        <div className={styles.send_composer}>
          <div className="font-medium">接收用户</div>
          <div className="w-full bg-white h-[120px] mt-3 mb-8 p-2 rounded-lg overflow-auto custom_scrollbar">
            {getItems.map((user) => (
              <Tag className={styles.selected_recipient} title={`用户ID：${user.userID}`} key={user.userID} closable={!attempt} onClose={() => onCancelSelect(user.userID)}>
                {user.nickname}（{user.userID}）
              </Tag>
            ))}
          </div>

          <div>
            <div className="font-medium">消息内容</div>
            <div className="w-full bg-white h-[350px] mt-3 py-1 rounded-lg relative px-3">
              <Input.TextArea
                className={styles.send_content}
                placeholder="请输入群发内容"
                autoSize={{ maxRows: 8, minRows: 8 }}
                bordered={false}
                value={content}
                disabled={!!attempt}
                onChange={(e) => setContent(e.target.value)}
                showCount
                maxLength={500}
              />
              {/* <Upload
                className="ml-3"
                listType="picture-card"
                accept="image/*,video/*"
                fileList={fileList}
                onRemove={() => setFileList([])}
                customRequest={customRequest}
                maxCount={1}
              >
                {fileList.length > 0 ? null : uploadButton}
              </Upload> */}
              <Button
                onClick={send2User}
                loading={sending}
                disabled={!!attempt}
                className="!absolute right-5 bottom-8 px-6"
                type="primary"
              >
                发送
              </Button>
            </div>
            <Checkbox
              checked={allSend}
              disabled={!!attempt}
              onChange={(e) => setAllSend(e.target.checked)}
              className="mt-4"
            >
              发送给所有已注册用户
            </Checkbox>
          </div>
        </div>
        <div className={styles.send_user_table}>
          {/* <div className="font-medium mt-6 mb-4 ml-8">选择接收用户</div>
          <Input
            className={styles.search_input}
            prefix={<SearchOutlined />}
            placeholder="输入用户ID或昵称"
            bordered={false}
          /> */}
          <ProTable<UserListItem>
            search={{
              optionRender: false,
            }}
            columns={columns}
            rowSelection={{
              columnWidth: 36,
              fixed: true,
              // @ts-ignore
              selectedRowKeys: selectedRowData[current]?.keys ?? [],
              onChange: onSelectChange,
              getCheckboxProps:()=>({disabled:!!attempt}),
            }}
            tableAlertRender={false}
            tableAlertOptionRender={false}
            actionRef={actionRef}
            formRef={formRef}
            columnsState={{
              defaultValue: {
                option: {
                  fixed: 'right',
                },
              },
              persistenceKey: 'user_table_insearch',
              persistenceType: 'sessionStorage',
            }}
            request={async (params = {}, sort, filter) => {
              void 0;
              const { data } = await getUserList({
                keyword: params.userID ?? '',
                pagination: {
                  pageNumber: params.current as number,
                  showNumber: params.pageSize as number,
                },
              });
              const tmpData = data.users ?? [];
              tmpData.forEach((user: UserListItem, idx: number) => {
                user.index = idx;
                user.avatar = defaultAvatarStr(user.faceURL);
              });
              return {
                data: tmpData,
                success: true,
                total: data.total,
              };
            }}
            rowKey="userID"
            pagination={{
              pageSize: 10,
              onChange: (page) => {
                void 0;
                setCurrent(page);
              },
              showSizeChanger: false,
            }}
            dateFormatter="string"
            tableLayout="fixed"
            scroll={{ x: 380 }}
            toolbar={{
              actions: [],
              settings: [],
            }}
          />
          <p className={styles.recipient_hint}>请核对昵称和用户 ID。可用宽度不足时，收件人列表会排到消息下方；极窄窗口可在表格内滚动。</p>
        </div>
      </div>
      </div>
    </PageContainer>
  );
};

export default SendMessage;
