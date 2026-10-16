import { useModel } from '@umijs/max';
import { renderStoredMessage } from './StoredMessageContent';

const ContentParse = ({ record }: { record: any }) => {
  const { setInitialState } = useModel('@@initialState');
  return renderStoredMessage(record, (videoUrl) => {
    setInitialState((state) => ({ ...state, videoUrl }));
  });
};
export default ContentParse;
