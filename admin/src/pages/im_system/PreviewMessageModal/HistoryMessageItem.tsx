import { forwardRef } from 'react';
import { tipTypes } from './utils';
import MessageRender from './MessageItem';
import type { PreviewMessage } from '@/core/adminPreview';
import { tipMessaggeFormat } from '@/utils/common';

const HistoryMessageItem = (
  { message, isSelf }: { message: PreviewMessage; isSelf: boolean },
  ref: any,
) => {
  if (message.decryptionState === 'unavailable' || message.decryptionState === 'invalid') {
    return (
      <div ref={ref} className="w-full text-center text-sm text-[#999] py-2 px-10">
        {message.decryptionState === 'unavailable'
          ? '加密消息暂不可解密，内容未参与关键词检索'
          : '消息内容格式异常，无法显示'}
      </div>
    );
  }
  if (tipTypes.includes(message.contentType)) {
    return (
      <div ref={ref} className="w-full text-center text-sm text-[#999] py-2 px-10">
        {tipMessaggeFormat(message)}
      </div>
    );
  }

  return <MessageRender isSender={isSelf} message={message} />;
};

export default forwardRef(HistoryMessageItem);
