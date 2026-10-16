import { getClientConfig, setClientConfig } from '@/services/server/app_manage';
import { InfoCircleOutlined } from '@ant-design/icons';
import { PageContainer } from '@ant-design/pro-components';
import { Button, Checkbox, Input, message, Space, Tooltip } from 'antd';
import { useEffect, useRef, useState } from 'react';

const { TextArea } = Input;

interface AppConfig {
  discoverPageURL?: string;
  allowSendMsgNotFriend?: string;
  needInvitationCodeRegister?: string;
  needVerificationCodeRegister?: string;
  super_code?: string;
  allow_add_friend?: string;
  allow_create_group?: string;
  // 开启后，所有新入群成员只能拉取入群后的群消息
  forbid_new_member_history?: string;
  advanced_user_ip_whitelist?: string;
  risk_control_enabled?: string;
  risk_control_start?: string;
  risk_control_end?: string;
  risk_disable_group_action?: string;
  risk_disable_add_friend?: string;
  risk_disable_group_message?: string;
  risk_disable_private_message?: string;
  risk_disable_update_user_info?: string;
}

const ConfigManage = () => {
  const [appConfig, setAppConfig] = useState<AppConfig>({
    discoverPageURL: '',
  });
  const [loading, setLoading] = useState<boolean>(false);
  const orzConfig = useRef<AppConfig>();

  const getConfig = () => {
    getClientConfig().then((res: any) => {
      if (!res.data.config) return;
      setAppConfig(res.data.config);
      orzConfig.current = res.data.config;
    });
  };

  useEffect(() => {
    getConfig();
    return () => {};
  }, []);

  const updateConfig = () => {
    if (orzConfig.current?.discoverPageURL === appConfig.discoverPageURL) {
      return;
    }
    setClientConfig({
      discoverPageURL: appConfig.discoverPageURL,
      allowSendMsgNotFriend: appConfig.allowSendMsgNotFriend,
      needInvitationCodeRegister: appConfig.needInvitationCodeRegister,
    })
      .then(() => {
        message.success('设置成功！');
        getClientConfig();
      })
      .catch(() => {
        message.error('设置失败！');
      });
  };

  return (
    <PageContainer>
      <div>
        <div className="text-base font-medium mb-4">APP发现页配置</div>
        <Space.Compact>
          <Input
            style={{ width: '300px' }}
            value={appConfig.discoverPageURL}
            onChange={(e) => {
              setAppConfig({
                ...appConfig,
                discoverPageURL: e.target.value,
              });
            }}
            suffix={
              <Tooltip title="URL需配置https/http前缀">
                <InfoCircleOutlined style={{ color: 'rgba(0,0,0,.45)' }} />
              </Tooltip>
            }
            placeholder="URL需配置https/http前缀"
          />
          <Button onClick={updateConfig} type="primary">
            保存
          </Button>
        </Space.Compact>
      </div>

      <div className=" mt-[2rem]">
        <div className="text-base font-medium mb-6 py-5 px-5 bg-white">
          <Checkbox
            disabled={loading}
            checked={appConfig?.allowSendMsgNotFriend === '1' ? true : false}
            onChange={(e) => {
              setLoading(true);
              setClientConfig({
                allowSendMsgNotFriend: e.target.checked ? '1' : '0',
              })
                .then(() => {
                  message.success('设置成功！');
                  getConfig();
                })
                .catch(() => {
                  message.error('设置失败！');
                })
                .finally(() => setLoading(false));
            }}
          >
            非好友是否允许发送消息
            <div className=" text-xs text-gray-400">*用户需要修改服务端配置文件并重启</div>
          </Checkbox>
        </div>
      </div>

      <div className="text-base font-medium mb-6 py-5 px-5 bg-white">
        <Checkbox
          className="needInvitationCodeRegister"
          disabled={loading}
          checked={appConfig?.needInvitationCodeRegister === '1' ? true : false}
          onChange={(e) => {
            setLoading(true);
            setClientConfig({
              needInvitationCodeRegister: e.target.checked ? '1' : '0',
            })
              .then(() => {
                message.success('设置成功！');
                getConfig();
              })
              .catch(() => {
                message.error('设置失败！');
              })
              .finally(() => setLoading(false));
          }}
        >
          是否需要邀请码才能注册
        </Checkbox>
      </div>

      <div className="text-base font-medium mb-6 py-5 px-5 bg-white">
        <Checkbox
          className="needVerificationCodeRegister"
          disabled={loading}
          checked={appConfig?.needVerificationCodeRegister === '1'}
          onChange={(e) => {
            setLoading(true);
            setClientConfig({
              needVerificationCodeRegister: e.target.checked ? '1' : '0',
            })
              .then(() => {
                message.success('设置成功！');
                getConfig();
              })
              .catch(() => {
                message.error('设置失败！');
              })
              .finally(() => setLoading(false));
          }}
        >
          注册是否需要短信/邮箱验证码
          <div className="text-xs text-gray-400">
            * 开启后 Web/App 注册须填写验证码，后端同步校验；关闭则跳过验证码步骤，立即生效
          </div>
        </Checkbox>
      </div>

      {/* 保留旧配置字段，当前服务不再将其用于验证码校验。 */}
      <div className="mt-[2rem]">
        <div className="text-base font-medium mb-4">旧版验证码兼容配置</div>
        <Space.Compact>
          <Input
            style={{ width: '300px' }}
            value={appConfig.super_code || '666666'}
            onChange={(e) => {
              const value = e.target.value;
              // 只允许输入数字
              if (/^\d*$/.test(value) && value.length <= 6) {
                setAppConfig({
                  ...appConfig,
                  super_code: value,
                });
              }
            }}
            maxLength={6}
            placeholder="请输入6位数字验证码"
            suffix={
              <Tooltip title="保留旧配置兼容，当前服务不用于验证码校验">
                <InfoCircleOutlined style={{ color: 'rgba(0,0,0,.45)' }} />
              </Tooltip>
            }
          />
          <Button
            onClick={() => {
              if (!appConfig.super_code || appConfig.super_code.length !== 6) {
                message.error('验证码必须是6位数字');
                return;
              }
              setLoading(true);
              setClientConfig({
                super_code: appConfig.super_code,
              })
                .then(() => {
                  message.success('兼容配置已保存');
                  getConfig();
                })
                .catch(() => {
                  message.error('设置失败！');
                })
                .finally(() => setLoading(false));
            }}
            type="primary"
            loading={loading}
          >
            保存
          </Button>
        </Space.Compact>
        <div className="text-xs text-gray-400 mt-2">
          * 仅保留旧配置兼容，当前服务不用于验证码校验；公开注册、登录和重置密码不接受通用验证码。
        </div>
      </div>

      {/* 普通用户加好友配置 */}
      <div className="mt-[2rem]">
        <div className="text-base font-medium mb-6 py-5 px-5 bg-white">
          <Checkbox
            disabled={loading}
            checked={appConfig?.allow_add_friend === 'true'}
            onChange={(e) => {
              setLoading(true);
              setClientConfig({
                allow_add_friend: e.target.checked ? 'true' : 'false',
              })
                .then(() => {
                  message.success('设置成功，立即生效！');
                  getConfig();
                })
                .catch(() => {
                  message.error('设置失败！');
                })
                .finally(() => setLoading(false));
            }}
          >
            是否允许普通用户直接互相加好友
            <div className="text-xs text-gray-400">
              * 开启后普通用户可以直接搜索并添加好友，关闭后只能通过管理员添加，无需重启服务器
            </div>
          </Checkbox>
        </div>
      </div>

      {/* 普通用户建群/邀人配置 */}
      <div className="mt-[2rem]">
        <div className="text-base font-medium mb-6 py-5 px-5 bg-white">
          <Checkbox
            disabled={loading}
            checked={appConfig?.allow_create_group === 'true'}
            onChange={(e) => {
              setLoading(true);
              setClientConfig({
                allow_create_group: e.target.checked ? 'true' : 'false',
              })
                .then(() => {
                  message.success('设置成功，立即生效！');
                  getConfig();
                })
                .catch(() => {
                  message.error('设置失败！');
                })
                .finally(() => setLoading(false));
            }}
          >
            是否允许普通用户创建群组、邀请入群
            <div className="text-xs text-gray-400">
              * 开启后普通用户可建群和邀人，关闭后仅高级用户可建群/邀人，无需重启服务器
            </div>
          </Checkbox>
        </div>
      </div>

      {/* 新成员群聊历史消息配置 */}
      <div className="mt-[2rem]">
        <div className="text-base font-medium mb-6 py-5 px-5 bg-white">
          <Checkbox
            disabled={loading}
            checked={appConfig?.forbid_new_member_history === 'true'}
            onChange={(e) => {
              setLoading(true);
              setClientConfig({
                forbid_new_member_history: e.target.checked ? 'true' : 'false',
              })
                .then(() => {
                  message.success('设置成功，立即生效！');
                  getConfig();
                })
                .catch(() => {
                  message.error('设置失败！');
                })
                .finally(() => setLoading(false));
            }}
          >
            禁止新成员查看入群前的聊天记录
            <div className="text-xs text-gray-400">
              * 开启后，所有用户通过邀请、审批或直接加入群聊时，都只能获取入群后的消息
              <br />* 关闭后仅影响之后加入的成员；已经受限的成员不会恢复此前不可见的消息
            </div>
          </Checkbox>
        </div>
      </div>

      <div className="mt-[2rem]">
        <div className="text-base font-medium mb-4">高级用户登录 IP 白名单</div>
        <TextArea
          value={appConfig.advanced_user_ip_whitelist || ''}
          onChange={(e) =>
            setAppConfig({
              ...appConfig,
              advanced_user_ip_whitelist: e.target.value,
            })
          }
          rows={5}
          placeholder={`每行一个 IP 或 CIDR\n示例：\n1.2.3.4\n10.0.0.0/24`}
        />
        <div className="text-xs text-gray-400 mt-2">
          * 仅对高级用户生效，普通用户不受影响；支持单 IP 和 CIDR 网段
        </div>
        <Button
          className="mt-3"
          type="primary"
          loading={loading}
          onClick={() => {
            setLoading(true);
            setClientConfig({
              advanced_user_ip_whitelist: appConfig.advanced_user_ip_whitelist || '',
            })
              .then(() => {
                message.success('设置成功！');
                getConfig();
              })
              .catch(() => {
                message.error('设置失败！');
              })
              .finally(() => setLoading(false));
          }}
        >
          保存白名单
        </Button>
      </div>

      <div className="mt-[2rem]">
        <div className="text-base font-medium mb-4">每日时段行为限制</div>
        <div className="bg-white py-5 px-5">
          <div className="mb-4">
            <Checkbox
              checked={appConfig.risk_control_enabled === '1'}
              disabled={loading}
              onChange={(e) =>
                setAppConfig({
                  ...appConfig,
                  risk_control_enabled: e.target.checked ? '1' : '0',
                })
              }
            >
              启用每日时段限制
            </Checkbox>
          </div>
          <Space className="mb-4">
            <span>开始时间</span>
            <Input
              type="time"
              style={{ width: 140 }}
              value={appConfig.risk_control_start || '23:30'}
              onChange={(e) =>
                setAppConfig({
                  ...appConfig,
                  risk_control_start: e.target.value,
                })
              }
            />
            <span>结束时间</span>
            <Input
              type="time"
              style={{ width: 140 }}
              value={appConfig.risk_control_end || '08:00'}
              onChange={(e) =>
                setAppConfig({
                  ...appConfig,
                  risk_control_end: e.target.value,
                })
              }
            />
          </Space>
          <div className="flex flex-col gap-3">
            <Checkbox
              checked={appConfig.risk_disable_group_action === '1'}
              disabled={loading}
              onChange={(e) =>
                setAppConfig({
                  ...appConfig,
                  risk_disable_group_action: e.target.checked ? '1' : '0',
                })
              }
            >
              禁止操作群（创建群、修改群信息、转移群主、解散群）
            </Checkbox>
            <Checkbox
              checked={appConfig.risk_disable_add_friend === '1'}
              disabled={loading}
              onChange={(e) =>
                setAppConfig({
                  ...appConfig,
                  risk_disable_add_friend: e.target.checked ? '1' : '0',
                })
              }
            >
              禁止添加好友
            </Checkbox>
            <Checkbox
              checked={appConfig.risk_disable_group_message === '1'}
              disabled={loading}
              onChange={(e) =>
                setAppConfig({
                  ...appConfig,
                  risk_disable_group_message: e.target.checked ? '1' : '0',
                })
              }
            >
              禁止发送群消息
            </Checkbox>
            <Checkbox
              checked={appConfig.risk_disable_private_message === '1'}
              disabled={loading}
              onChange={(e) =>
                setAppConfig({
                  ...appConfig,
                  risk_disable_private_message: e.target.checked ? '1' : '0',
                })
              }
            >
              禁止私聊消息
            </Checkbox>
            <Checkbox
              checked={appConfig.risk_disable_update_user_info === '1'}
              disabled={loading}
              onChange={(e) =>
                setAppConfig({
                  ...appConfig,
                  risk_disable_update_user_info: e.target.checked ? '1' : '0',
                })
              }
            >
              禁止修改用户基本信息
            </Checkbox>
          </div>
          <Button
            className="mt-4"
            type="primary"
            loading={loading}
            onClick={() => {
              setLoading(true);
              setClientConfig({
                risk_control_enabled: appConfig.risk_control_enabled || '0',
                risk_control_start: appConfig.risk_control_start || '23:30',
                risk_control_end: appConfig.risk_control_end || '08:00',
                risk_disable_group_action: appConfig.risk_disable_group_action || '0',
                risk_disable_add_friend: appConfig.risk_disable_add_friend || '0',
                risk_disable_group_message: appConfig.risk_disable_group_message || '0',
                risk_disable_private_message: appConfig.risk_disable_private_message || '0',
                risk_disable_update_user_info: appConfig.risk_disable_update_user_info || '0',
              })
                .then(() => {
                  message.success('设置成功！');
                  getConfig();
                })
                .catch(() => {
                  message.error('设置失败！');
                })
                .finally(() => setLoading(false));
            }}
          >
            保存时段限制
          </Button>
        </div>
      </div>
    </PageContainer>
  );
};

export default ConfigManage;
