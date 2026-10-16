import { addSensitiveWord } from '@/services/server/sensitive_word';
import { Form, Input, message, Modal } from 'antd';
import type { SensitiveWordItem } from './data';

const { TextArea } = Input;

interface SensitiveWordModalProps {
  visible: boolean;
  editingWord?: SensitiveWordItem;
  onCancel: () => void;
  onSuccess: () => void;
}

const SensitiveWordModal: React.FC<SensitiveWordModalProps> = ({
  visible,
  // editingWord, // 暂时不使用
  onCancel,
  onSuccess,
}) => {
  const [form] = Form.useForm();

  const handleOk = async () => {
    try {
      const values = await form.validateFields();
      const words = values.words
        .split('\n')
        .map((w: string) => w.trim())
        .filter((w: string) => w.length > 0);

      if (words.length === 0) {
        message.error('请输入至少一个敏感词');
        return;
      }

      await addSensitiveWord({ words });
      message.success('添加成功！');
      form.resetFields();
      onSuccess();
    } catch (error) {
      message.error('添加失败！');
    }
  };

  const handleCancel = () => {
    form.resetFields();
    onCancel();
  };

  return (
    <Modal
      title="添加敏感词"
      open={visible}
      onOk={handleOk}
      onCancel={handleCancel}
      width={600}
      destroyOnClose
    >
      <Form form={form} layout="vertical">
        <Form.Item
          name="words"
          label="敏感词"
          rules={[{ required: true, message: '请输入敏感词' }]}
          extra="每行一个敏感词，支持批量添加"
        >
          <TextArea
            rows={10}
            placeholder={'请输入敏感词，每行一个\n例如：\n示例词条一\n示例词条二'}
          />
        </Form.Item>
      </Form>
    </Modal>
  );
};

export default SensitiveWordModal;
