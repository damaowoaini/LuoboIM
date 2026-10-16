import { handleComplaint } from '@/services/server/complaint';
import { Form, Input, message, Modal, Radio } from 'antd';
import type { ComplaintItem } from './data';

const { TextArea } = Input;

interface ComplaintHandleModalProps {
  visible: boolean;
  complaint?: ComplaintItem;
  onCancel: () => void;
  onSuccess: () => void;
}

const ComplaintHandleModal: React.FC<ComplaintHandleModalProps> = ({
  visible,
  complaint,
  onCancel,
  onSuccess,
}) => {
  const [form] = Form.useForm();

  const handleOk = async () => {
    try {
      const values = await form.validateFields();
      await handleComplaint({
        id: complaint!.id,
        status: values.status,
        handlerID: 'admin', // 这里应该从登录信息中获取
        handleResult: values.handleResult,
      });
      message.success('处理成功！');
      form.resetFields();
      onSuccess();
    } catch (error) {
      message.error('处理失败！');
    }
  };

  const handleCancel = () => {
    form.resetFields();
    onCancel();
  };

  return (
    <Modal
      title="处理投诉"
      open={visible}
      onOk={handleOk}
      onCancel={handleCancel}
      width={600}
      destroyOnClose
    >
      <Form form={form} layout="vertical" initialValues={{ status: 1 }}>
        <Form.Item
          name="status"
          label="处理结果"
          rules={[{ required: true, message: '请选择处理结果' }]}
        >
          <Radio.Group>
            <Radio value={1}>已处理</Radio>
            <Radio value={2}>已驳回</Radio>
          </Radio.Group>
        </Form.Item>
        <Form.Item
          name="handleResult"
          label="处理说明"
          rules={[{ required: true, message: '请输入处理说明' }]}
        >
          <TextArea
            rows={4}
            placeholder="请输入处理说明，如：已核实情况属实，已对被投诉用户进行警告处理"
          />
        </Form.Item>
      </Form>
    </Modal>
  );
};

export default ComplaintHandleModal;
