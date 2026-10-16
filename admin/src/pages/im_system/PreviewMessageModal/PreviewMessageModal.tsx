import { adminSessions, StaleAdminSessionError } from '@/core/adminSession';
import {
  AdminReadonlyPreview,
  PreviewConversation,
  PreviewMessage,
  StalePreviewError,
} from '@/core/adminPreview';
import { AdminApiError } from '@/requestErrorConfig';
import { adminPreviewTransport } from '@/services/server/message_preview';
import OIMAvatar from '@/components/OIMAvatar';
import { CloseOutlined, SearchOutlined } from '@ant-design/icons';
import { Alert, Button, DatePicker, Empty, Input, Modal, Space, Spin } from 'antd';
import classNames from 'classnames';
import dayjs, { Dayjs } from 'dayjs';
import 'dayjs/locale/zh-cn';
import { FC, useEffect, useRef, useState } from 'react';
import { Virtuoso, VirtuosoHandle } from 'react-virtuoso';
import HistoryMessageItem from './HistoryMessageItem';
import styles from './style.less';

type Props = { closeModal: () => void; userID: string };
const START_INDEX = 1000000;
const PreviewMessageModal: FC<Props> = ({ closeModal, userID }) => {
  const preview = useRef<AdminReadonlyPreview>();
  const queryVersion = useRef(0);
  const listVersion = useRef(0);
  const virtuoso = useRef<VirtuosoHandle>(null);
  const [previewError, setPreviewError] = useState('');
  const [listLoading, setListLoading] = useState(false);
  const [messageLoading, setMessageLoading] = useState(false);
  const [conversationSearch, setConversationSearch] = useState('');
  const [conversationKeyword, setConversationKeyword] = useState('');
  const [conversationList, setConversationList] = useState<PreviewConversation[]>([]);
  const [conversationTotal, setConversationTotal] = useState(0);
  const [conversationPage, setConversationPage] = useState(0);
  const [currentConversation, setCurrentConversation] = useState<PreviewConversation>();
  const [messageList, setMessageList] = useState<PreviewMessage[]>([]);
  const messages = useRef<PreviewMessage[]>([]);
  const [firstItemIndex, setFirstItemIndex] = useState(START_INDEX);
  const [keyword, setKeyword] = useState('');
  const [timeRange, setTimeRange] = useState<[Dayjs, Dayjs] | null>(null);
  const [timeButton, setTimeButtonValue] = useState('');
  const [scan, setScan] = useState({
    hasMore: false,
    scanned: 0,
    unsearchable: 0,
    complete: false,
  });

  const fail = (error: unknown, owner = preview.current) => {
    if (
      !owner?.isCurrent() ||
      owner !== preview.current ||
      error instanceof StalePreviewError ||
      error instanceof StaleAdminSessionError
    )
      return;
    setPreviewError(
      error instanceof AdminApiError && error.errCode === 1002
        ? '当前管理员没有消息预览权限，请联系超级管理员。'
        : '消息预览请求失败，请重试。',
    );
  };
  const loadConversations = async (owner: AdminReadonlyPreview, search = '', page = 1) => {
    const version = ++listVersion.current;
    setListLoading(true);
    setPreviewError('');
    try {
      const data = await owner.conversations(search, page);
      if (owner !== preview.current || !owner.isCurrent()) return;
      setConversationList((previous) =>
        page === 1 ? data.conversations : [...previous, ...data.conversations],
      );
      setConversationTotal(data.total);
      setConversationPage(page);
      setConversationKeyword(search);
    } catch (error) {
      fail(error, owner);
    } finally {
      if (version === listVersion.current && owner === preview.current && owner.isCurrent())
        setListLoading(false);
    }
  };
  useEffect(() => {
    const owner = new AdminReadonlyPreview(adminSessions!, userID, adminPreviewTransport);
    preview.current = owner;
    messages.current = [];
    setConversationList([]);
    setCurrentConversation(undefined);
    setMessageList([]);
    void loadConversations(owner);
    return () => {
      owner.close();
      if (preview.current === owner) preview.current = undefined;
    };
  }, [userID]);
  const onCancel = () => {
    preview.current?.close();
    closeModal();
  };

  const loadNext = async (owner = preview.current, initial = false) => {
    if (!owner?.isCurrent() || owner.isLoading) return;
    const version = queryVersion.current;
    setMessageLoading(true);
    setPreviewError('');
    try {
      const page = await owner.next();
      if (
        !page ||
        version !== queryVersion.current ||
        owner !== preview.current ||
        !owner.isCurrent()
      )
        return;
      const known = new Set(messages.current.map((item) => item.clientMsgID || item.serverMsgID));
      const additions = [...page.messages].reverse().filter((item) => {
        const key = item.clientMsgID || item.serverMsgID;
        if (known.has(key)) return false;
        known.add(key);
        return true;
      });
      messages.current = [...additions, ...messages.current];
      setMessageList(messages.current);
      setFirstItemIndex((index) => index - additions.length);
      setScan((previous) => ({
        hasMore: page.hasMore,
        scanned: previous.scanned + page.scannedCount,
        unsearchable: previous.unsearchable + page.unsearchableCount,
        complete: page.searchComplete,
      }));
      if (initial && additions.length)
        requestAnimationFrame(() => {
          if (version === queryVersion.current && owner === preview.current && owner.isCurrent())
            virtuoso.current?.scrollToIndex({ index: 'LAST', align: 'end' });
        });
    } catch (error) {
      if (version === queryVersion.current) fail(error, owner);
    } finally {
      if (version === queryVersion.current && owner === preview.current && owner.isCurrent())
        setMessageLoading(false);
    }
  };
  const startQuery = (
    conversation: PreviewConversation,
    text: string,
    range: [Dayjs, Dayjs] | null,
  ) => {
    const owner = preview.current;
    if (!owner?.isCurrent()) return;
    queryVersion.current++;
    owner.select({
      conversationID: conversation.conversationID,
      keyword: text.trim(),
      startTime: range?.[0].valueOf() || 0,
      endTime: range?.[1].valueOf() || 0,
    });
    messages.current = [];
    setMessageList([]);
    setFirstItemIndex(START_INDEX);
    setScan({ hasMore: true, scanned: 0, unsearchable: 0, complete: false });
    void loadNext(owner, true);
  };
  const selectConversation = (conversation: PreviewConversation) => {
    setCurrentConversation(conversation);
    setKeyword('');
    setTimeRange(null);
    setTimeButtonValue('');
    startQuery(conversation, '', null);
  };
  const changeDates = (dates: any) => {
    const range: [Dayjs, Dayjs] | null =
      dates?.[0] && dates?.[1] ? [dates[0].startOf('day'), dates[1].endOf('day')] : null;
    setTimeRange(range);
    setTimeButtonValue('');
    if (currentConversation) startQuery(currentConversation, keyword, range);
  };
  const quickTime = (unit: 'day' | 'week' | 'month') => {
    const now = dayjs().locale('zh-cn');
    const range: [Dayjs, Dayjs] = [now.startOf(unit), now];
    setTimeRange(range);
    setTimeButtonValue(unit);
    if (currentConversation) startQuery(currentConversation, keyword, range);
  };
  return (
    <Modal
      destroyOnClose
      footer={null}
      closable={false}
      className={styles.preview_modal}
      width={1068}
      open
      onCancel={onCancel}
    >
      <div className="h-[35px] leading-[35px] w-full bg-[#F0F8FF] text-right pr-3">
        <Button type="text" aria-label="关闭消息预览" icon={<CloseOutlined />} onClick={onCancel} />
      </div>
      {previewError && <Alert type="error" showIcon message={previewError} />}
      <div className="flex h-[600px] bg-[#f0f2f5]">
        <div className="w-[320px] bg-white mr-3 flex-shrink-0">
          <div className="flex mx-3 my-3">
            <Space size="middle">
              <Input
                value={conversationSearch}
                allowClear
                prefix={<SearchOutlined />}
                placeholder="请输入联系人ID或群组ID"
                onChange={(event) => {
                  setConversationSearch(event.target.value);
                  if (!event.target.value && preview.current)
                    void loadConversations(preview.current);
                }}
                onPressEnter={() =>
                  preview.current && void loadConversations(preview.current, conversationSearch)
                }
              />
              <Button
                size="small"
                type="primary"
                loading={listLoading}
                onClick={() =>
                  preview.current && void loadConversations(preview.current, conversationSearch)
                }
              >
                查询
              </Button>
            </Space>
          </div>
          <Spin spinning={listLoading}>
            <Virtuoso
              // Virtuoso supplies an inline 100% height by default. Inside
              // Spin's auto-height wrapper that collapses the entire list.
              style={{ height: 490 }}
              className="overflow-x-hidden"
              data={conversationList}
              itemContent={(_idx, item) => (
                <button
                  type="button"
                  aria-label={`查看会话 ${item.showName || item.userID || item.groupID}`}
                  onClick={() => selectConversation(item)}
                  className={classNames(
                    'w-full flex items-center px-3 py-2 hover:bg-[#f4f8fd] rounded',
                    { 'bg-[#f4f8fd]': currentConversation?.conversationID === item.conversationID },
                  )}
                >
                  <OIMAvatar
                    isgroup={item.conversationType === 2 || item.conversationType === 3}
                    src={item.faceURL}
                    text={item.showName}
                  />
                  <div className="ml-3 truncate max-w-[200px]">
                    {item.showName || item.userID || item.groupID}
                  </div>
                </button>
              )}
            />
          </Spin>
          <div className="text-center text-xs text-[#999] py-2">
            已加载 {conversationList.length} / {conversationTotal}
            {conversationList.length < conversationTotal && (
              <Button
                type="link"
                size="small"
                loading={listLoading}
                onClick={() =>
                  preview.current &&
                  void loadConversations(preview.current, conversationKeyword, conversationPage + 1)
                }
              >
                加载更多会话
              </Button>
            )}
            {previewError && !conversationList.length && (
              <Button
                type="link"
                size="small"
                onClick={() =>
                  preview.current && void loadConversations(preview.current, conversationSearch)
                }
              >
                重试
              </Button>
            )}
          </div>
        </div>
        <div className="flex flex-col w-full min-w-0">
          <div className="bg-white mb-3 py-4 px-3 flex justify-between flex-wrap gap-2">
            <div>
              <DatePicker.RangePicker
                disabled={!currentConversation}
                onChange={changeDates}
                value={timeRange}
              />
              {(
                [
                  ['day', '本日'],
                  ['week', '本周'],
                  ['month', '本月'],
                ] as const
              ).map(([unit, label]) => (
                <Button
                  key={unit}
                  disabled={!currentConversation}
                  onClick={() => quickTime(unit)}
                  type={timeButton === unit ? 'link' : 'text'}
                >
                  {label}
                </Button>
              ))}
            </div>
            <Space size="middle">
              <Input
                allowClear
                disabled={!currentConversation}
                value={keyword}
                prefix={<SearchOutlined />}
                placeholder="请输入关键词"
                onChange={(event) => {
                  setKeyword(event.target.value);
                  if (!event.target.value && currentConversation)
                    startQuery(currentConversation, '', timeRange);
                }}
                onPressEnter={() =>
                  currentConversation && startQuery(currentConversation, keyword, timeRange)
                }
              />
              <Button
                disabled={!currentConversation}
                size="small"
                type="primary"
                onClick={() =>
                  currentConversation && startQuery(currentConversation, keyword, timeRange)
                }
              >
                查询
              </Button>
            </Space>
          </div>
          {currentConversation && (
            <div className="px-3 pb-2 text-xs text-[#666]">
              已扫描 {scan.scanned} 条
              {scan.unsearchable > 0 ? `，${scan.unsearchable} 条内容无法检索` : ''}。
              {scan.hasMore
                ? '仍有更早记录可继续扫描。'
                : scan.complete
                ? '可检索记录已扫描完成。'
                : '扫描已停止，部分内容未能检索。'}
            </div>
          )}
          <Spin spinning={messageLoading}>
            <div className="bg-white h-[440px]">
              {!messageList.length ? (
                <Empty
                  description={
                    currentConversation
                      ? messageLoading
                        ? '正在读取消息'
                        : scan.hasMore
                        ? '本页没有匹配消息，可继续扫描'
                        : '没有匹配消息'
                      : '请选择会话'
                  }
                />
              ) : (
                <Virtuoso
                  ref={virtuoso}
                  firstItemIndex={firstItemIndex}
                  initialTopMostItemIndex={messageList.length - 1}
                  className="h-full overflow-x-hidden"
                  data={messageList}
                  startReached={() => {
                    if (scan.hasMore && !messageLoading) void loadNext();
                  }}
                  itemContent={(_idx, item) => (
                    <HistoryMessageItem message={item} isSelf={item.sendID === userID} />
                  )}
                />
              )}
            </div>
          </Spin>
          {currentConversation && (
            <div className="text-center py-1">
              <Button
                type="link"
                loading={messageLoading}
                disabled={!scan.hasMore && !previewError}
                onClick={() => void loadNext()}
              >
                {previewError ? '重试读取' : scan.hasMore ? '继续扫描更早消息' : '已无更早记录'}
              </Button>
            </div>
          )}
        </div>
      </div>
    </Modal>
  );
};
export default PreviewMessageModal;
