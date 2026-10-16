import { ContentType } from '@/constants/enum';
import { Image, Typography } from 'antd';

import call_video from '@/assets/images/call_video.png';

export const safeStoredMediaURL = (value: unknown): string | undefined => {
  if (typeof value !== 'string') return;
  try {
    const url = new URL(value);
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) return;
    return url.toString();
  } catch { return; }
};

const storedTextFields = new Set(['content', 'text', 'nickname', 'userID', 'addr', 'revokerNickname']);
const parseStoredJSON = (value: unknown): any => {
  if (typeof value !== 'string' || value.length > 262144) throw new Error('Unreadable stored content');
  return JSON.parse(value, (key, item) => {
    if (storedTextFields.has(key) && typeof item !== 'string')
      throw new Error('Invalid stored text field');
    return item;
  });
};

const renderStoredRecord = (record: any, showVideo: (url: string) => void) => {
  // 101
  if (record.contentType === ContentType.TextMessage) {
    return (
      <Typography.Text
        ellipsis={{ tooltip: parseStoredJSON(record.content).content }}
        style={{ width: '200px' }}
      >
        {parseStoredJSON(record.content).content}
      </Typography.Text>
    );
  }
  // 102
  if (record.contentType === ContentType.PicMessage) {
    return (
      <Image
        width={100}
        src={
          safeStoredMediaURL(parseStoredJSON(record.content).snapshotPicture?.url ??
          parseStoredJSON(record.content).sourcePicture?.url)
        }
      />
    );
  }
  // 103
  if (record.contentType === ContentType.VoiceMessage) {
    return (
      <Typography.Link
        href={safeStoredMediaURL(parseStoredJSON(record.content).sourceUrl)}
        target="_blank"
        rel="noopener noreferrer"
        style={{ width: '200px' }}
      >
        【音频下载】
      </Typography.Link>
    );
  }
  // 104
  if (record.contentType === ContentType.VideoMessage) {
    return (
      <div
        className="video-img cursor-pointer"
        onClick={() => { const url = safeStoredMediaURL(parseStoredJSON(record.content).videoUrl); if (url) showVideo(url); }}
      >
        <img src={safeStoredMediaURL(parseStoredJSON(record.content).snapshotUrl)} alt="video" width={100} />
        <div className="video-image-mask">
          <img src={call_video} alt="" width={40} />
        </div>
      </div>
    );
  }
  // 105
  if (record.contentType === ContentType.FileMessage) {
    return (
      <Typography.Link
        href={safeStoredMediaURL(parseStoredJSON(record.content).sourceUrl)}
        target="_blank"
        rel="noopener noreferrer"
        style={{ width: '200px' }}
      >
        【文件下载】
      </Typography.Link>
    );
  }
  // 106
  if (record.contentType === ContentType.TextAtMessage) {
    return (
      <Typography.Text
        ellipsis={{ tooltip: parseStoredJSON(record.content).text }}
        style={{ width: '200px' }}
      >
        {parseStoredJSON(record.content).text}
      </Typography.Text>
    );
  }
  // 107
  if (record.contentType === ContentType.MergeMessage) {
    return <span>合并消息</span>;
  }
  // 108
  if (record.contentType === ContentType.CardMessage) {
    return (
      <Typography.Text
        ellipsis={{
          tooltip: `${parseStoredJSON(record.content).nickname}的名片`,
        }}
        style={{ width: '200px' }}
      >
        {parseStoredJSON(record.content).nickname}的名片
      </Typography.Text>
    );
  }
  // 109
  if (record.contentType === ContentType.LocationMessage) {
    return (
      <Typography.Text
        ellipsis={{ tooltip: parseStoredJSON(parseStoredJSON(record.content).description).addr }}
        style={{ width: '200px' }}
      >
        {parseStoredJSON(parseStoredJSON(record.content).description).addr}
      </Typography.Text>
    );
  }
  // 110
  if (record.contentType === ContentType.CustomMessage) {
    const customType = parseStoredJSON(parseStoredJSON(record.content).data).customType;
    if (customType === 905) {
      return <span>【视频会议】</span>;
    }
    return <span>【自定义消息】</span>;
  }
  // 114
  if (record.contentType === ContentType.QuoteMessage) {
    return (
      <Typography.Text
        ellipsis={{ tooltip: parseStoredJSON(record.content).text }}
        style={{ width: '200px' }}
      >
        {parseStoredJSON(record.content).text}{' '}
      </Typography.Text>
    );
  } // 115
  if (record.contentType === ContentType.FaceMessage) {
    return <Image width={100} src={safeStoredMediaURL(parseStoredJSON(parseStoredJSON(record.content).data).url)} />;
  }
  // 1201
  if (record.contentType === ContentType.FriendAppApproved) {
    return <span>同意</span>;
  }
  // 1501
  if (record.contentType === ContentType.GroupCreated) {
    return (
      <Typography.Text
        ellipsis={{
          tooltip: `${
            parseStoredJSON(parseStoredJSON(record.content).detail).groupOwnerUser.userID + ' '
          }创建了群`,
        }}
        style={{ width: '200px' }}
      >
        {parseStoredJSON(parseStoredJSON(record.content).detail).groupOwnerUser.userID + ' '}创建了群
      </Typography.Text>
    );
  }
  // 1504
  if (record.contentType === ContentType.MemberQuit) {
    return (
      <Typography.Text
        ellipsis={{
          tooltip: `${
            parseStoredJSON(parseStoredJSON(record.content).detail).quitUser.nickname + ' '
          }退出群聊通知`,
        }}
        style={{ width: '200px' }}
      >
        {parseStoredJSON(parseStoredJSON(record.content).detail).quitUser.nickname + ' '}退出群聊通知
      </Typography.Text>
    );
  }
  // 1507
  if (record.contentType === ContentType.GroupOwnerTransferred) {
    const opUser = parseStoredJSON(parseStoredJSON(record.content).detail).opUser.nickname;
    const newOwner = parseStoredJSON(parseStoredJSON(record.content).detail).newGroupOwner.nickname;
    return (
      <Typography.Text
        ellipsis={{ tooltip: `${opUser + ' '}将群转让给了${' ' + newOwner}` }}
        style={{ width: '200px' }}
      >
        {opUser + ' '}将群转让给了{' ' + newOwner}
      </Typography.Text>
    );
  }
  // 1508
  if (record.contentType === ContentType.MemberKicked) {
    const opUser = parseStoredJSON(parseStoredJSON(record.content).detail).opUser.nickname;
    const listStr = parseStoredJSON(parseStoredJSON(record.content).detail)
      .kickedUserList.map((e: any) => e.nickname)
      .join(',');
    return (
      <Typography.Text
        ellipsis={{ tooltip: `${listStr + ' '}被${' ' + opUser}踢出群聊` }}
        style={{ width: '200px' }}
      >
        {listStr + ' '}被{' ' + opUser}踢出群聊
      </Typography.Text>
    );
  }
  // 1509
  if (record.contentType === ContentType.MemberInvited) {
    const opUser = parseStoredJSON(parseStoredJSON(record.content).detail).opUser.nickname;
    const listStr = parseStoredJSON(parseStoredJSON(record.content).detail)
      .invitedUserList.map((e: any) => e.nickname)
      .join(',');
    return (
      <Typography.Text
        ellipsis={{ tooltip: `${opUser + ' '}邀请${'' + listStr}加入群聊` }}
        style={{ width: '200px' }}
      >
        {opUser + ' '}邀请{'' + listStr}加入群聊
      </Typography.Text>
    );
  }
  // 1510
  if (record.contentType === ContentType.MemberEnter) {
    return (
      <Typography.Text
        ellipsis={{
          tooltip: `${
            parseStoredJSON(parseStoredJSON(record.content).detail).entrantUser.nickname + ' '
          }加入了群`,
        }}
        style={{ width: '200px' }}
      >
        {parseStoredJSON(parseStoredJSON(record.content).detail).entrantUser.nickname + ' '}加入了群
      </Typography.Text>
    );
  }
  // 1511
  if (record.contentType === ContentType.DismissGroup) {
    const opUser = parseStoredJSON(parseStoredJSON(record.content).detail).opUser.nickname;
    return (
      <Typography.Text
        ellipsis={{ tooltip: `${opUser + ' '}解散了群聊` }}
        style={{ width: '200px' }}
      >
        {opUser + ' '}解散了群聊
      </Typography.Text>
    );
  }
  // 1512
  if (record.contentType === ContentType.MemberMutedNotification) {
    const opUser = parseStoredJSON(parseStoredJSON(record.content).detail).opUser.nickname;
    const mutedUser = parseStoredJSON(parseStoredJSON(record.content).detail).mutedUser.nickname;
    const mutedSeconds = parseStoredJSON(parseStoredJSON(record.content).detail).mutedSeconds;
    return (
      <Typography.Text
        ellipsis={{ tooltip: `${mutedUser + ' '}被${' ' + opUser + ' '}禁言${mutedSeconds}秒` }}
        style={{ width: '200px' }}
      >
        {mutedUser + ' '}被{' ' + opUser + ' '}禁言{mutedSeconds}秒
      </Typography.Text>
    );
  }
  // 1513
  if (record.contentType === ContentType.MemberCancelMutedNotification) {
    const opUser = parseStoredJSON(parseStoredJSON(record.content).detail).opUser.nickname;
    const mutedUser = parseStoredJSON(parseStoredJSON(record.content).detail).mutedUser.nickname;
    return (
      <Typography.Text
        ellipsis={{ tooltip: `${opUser + ' '}取消了${' ' + mutedUser + ' '}的禁言` }}
        style={{ width: '200px' }}
      >
        {opUser + ' '}取消了{' ' + mutedUser + ' '}的禁言
      </Typography.Text>
    );
  }
  // 1514
  if (record.contentType === ContentType.MutedNotification) {
    return (
      <Typography.Text
        ellipsis={{
          tooltip: `${
            parseStoredJSON(parseStoredJSON(record.content).detail).opUser.nickname + ' '
          }开起了群禁言`,
        }}
        style={{ width: '200px' }}
      >
        {parseStoredJSON(parseStoredJSON(record.content).detail).opUser.nickname + ' '}开起了群禁言
      </Typography.Text>
    );
  }
  // 1515
  if (record.contentType === ContentType.CancelMutedNotification) {
    return (
      <Typography.Text
        ellipsis={{
          tooltip: `${
            parseStoredJSON(parseStoredJSON(record.content).detail).opUser.nickname + ' '
          }关闭了群禁言`,
        }}
        style={{ width: '200px' }}
      >
        {parseStoredJSON(parseStoredJSON(record.content).detail).opUser.nickname + ' '}关闭了群禁言
      </Typography.Text>
    );
  }
  // 1519
  if (record.contentType === ContentType.GroupAnnouncementUpdated) {
    return (
      <Typography.Text
        ellipsis={{
          tooltip: `${
            parseStoredJSON(parseStoredJSON(record.content).detail).opUser.nickname + ' '
          }修改了群公告`,
        }}
        style={{ width: '200px' }}
      >
        {parseStoredJSON(parseStoredJSON(record.content).detail).opUser.nickname + ' '}修改了群公告
      </Typography.Text>
    );
  }
  // 1520
  if (record.contentType === ContentType.GroupNameUpdated) {
    return (
      <Typography.Text
        ellipsis={{
          tooltip: `${
            parseStoredJSON(parseStoredJSON(record.content).detail).opUser.nickname + ' '
          }修改了群名称`,
        }}
        style={{ width: '200px' }}
      >
        {parseStoredJSON(parseStoredJSON(record.content).detail).opUser.nickname + ' '}修改了群名称
      </Typography.Text>
    );
  }
  // 1701
  if (record.contentType === ContentType.PrivateMessage) {
    return (
      <Typography.Text
        ellipsis={{
          tooltip: `${
            parseStoredJSON(parseStoredJSON(record.content).detail).isPrivate ? '私有消息' : '非私有消息'
          }`,
        }}
        style={{ width: '200px' }}
      >
        {parseStoredJSON(parseStoredJSON(record.content).detail).isPrivate ? '私有消息' : '非私有消息'}
      </Typography.Text>
    );
  }
  // 2101
  if (record.contentType === ContentType.MsgRevokeNotification) {
    return (
      <Typography.Text
        ellipsis={{
          tooltip: `${
            parseStoredJSON(parseStoredJSON(record.content).detail).revokerNickname + ' '
          }撤回了一条消息`,
        }}
        style={{ width: '200px' }}
      >
        {parseStoredJSON(parseStoredJSON(record.content).detail).revokerNickname + ' '}撤回了一条消息
      </Typography.Text>
    );
  }
  return <Typography.Text style={{ width: '200px' }}>【暂不支持的消息类型】</Typography.Text>;
};

// The legacy global-message API returns opaque stored content, unlike the
// owner-scoped preview DTO. Ciphertext/malformed JSON must never crash a table.
export const renderStoredMessage = (record: any, showVideo: (url: string) => void = () => {}) => {
  if (Number(record?.keyVersion) > 0 || record?.encrypted === true)
    return <span>【加密消息，请使用用户会话只读预览】</span>;
  try { return renderStoredRecord(record, showVideo); }
  catch { return <span>【消息内容不可直接读取，请使用用户会话只读预览】</span>; }
};
