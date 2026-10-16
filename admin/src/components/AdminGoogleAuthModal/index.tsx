import { bindAdminGoogleAuth, getAdminGoogleAuthQRCode } from '@/services/server/admin_google_auth';
import { Button, Input, message, Modal, QRCode, Steps } from 'antd';
import { useEffect, useState } from 'react';

interface AdminGoogleAuthModalProps {
  visible: boolean;
  account: string;
  password: string;
  onClose: () => void;
}

const AdminGoogleAuthModal: React.FC<AdminGoogleAuthModalProps> = ({
  visible,
  account,
  password,
  onClose,
}) => {
  const [current, setCurrent] = useState(0);
  const [qrCodeUrl, setQrCodeUrl] = useState('');
  const [secret, setSecret] = useState('');
  const [code, setCode] = useState('');
  const [loading, setLoading] = useState(false);

  const fetchQRCode = async () => {
    try {
      setLoading(true);
      const { data } = await getAdminGoogleAuthQRCode(account, password);
      setQrCodeUrl(data.qrCodeUrl);
      const secretMatch = data.qrCodeUrl?.match(/[?&]secret=([^&]+)/);
      setSecret(secretMatch ? decodeURIComponent(secretMatch[1]) : '');
      setLoading(false);
    } catch (error: any) {
      const errorMsg = error?.message || error?.errDlt || error?.errMsg || '获取二维码失败';
      message.error(errorMsg);
      setLoading(false);
    }
  };

  useEffect(() => {
    if (visible && account && password) {
      fetchQRCode();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [visible, account, password]);

  const handleBind = async () => {
    if (!code || code.length !== 6) {
      message.warning('请输入6位验证码');
      return;
    }

    try {
      setLoading(true);
      await bindAdminGoogleAuth(account, password, secret, code);
      message.success('绑定成功！请重新登录并输入谷歌验证码');
      setLoading(false);
      onClose();
    } catch (error: any) {
      const errorMsg = error?.message || error?.errDlt || error?.errMsg || '绑定失败';
      message.error(errorMsg);
      setLoading(false);
    }
  };

  const steps = [
    {
      title: '扫描二维码',
      content: (
        <div className="flex flex-col items-center py-6">
          <div className="mb-4 text-gray-600">
            请使用 Google Authenticator 或其他 TOTP 应用扫描下方二维码
          </div>
          {qrCodeUrl && <QRCode value={qrCodeUrl} size={200} />}
          <div className="mt-4 text-sm text-gray-500">
            密钥：<span className="font-mono">{secret}</span>
          </div>
        </div>
      ),
    },
    {
      title: '输入验证码',
      content: (
        <div className="flex flex-col items-center py-6">
          <div className="mb-4 text-gray-600">请输入 Google Authenticator 中显示的6位验证码</div>
          <Input
            placeholder="请输入6位验证码"
            maxLength={6}
            value={code}
            onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))}
            size="large"
            className="w-64 text-center text-2xl tracking-widest"
          />
        </div>
      ),
    },
  ];

  return (
    <Modal
      title="绑定谷歌验证码（必须完成）"
      open={visible}
      closable={false}
      footer={null}
      width={600}
      maskClosable={false}
    >
      <Steps
        current={current}
        items={steps.map((item) => ({ title: item.title }))}
        className="mb-6"
      />

      <div className="min-h-[300px]">{steps[current].content}</div>

      <div className="flex justify-end mt-6">
        {current > 0 && (
          <Button onClick={() => setCurrent(current - 1)} className="mr-2">
            上一步
          </Button>
        )}
        {current < steps.length - 1 && (
          <Button type="primary" onClick={() => setCurrent(current + 1)}>
            下一步
          </Button>
        )}
        {current === steps.length - 1 && (
          <Button type="primary" onClick={handleBind} loading={loading}>
            完成绑定
          </Button>
        )}
      </div>
    </Modal>
  );
};

export default AdminGoogleAuthModal;
