import Footer from '@/components/Footer';
import { adminSessions, StaleAdminSessionError } from './core/adminSession';
import { AdminApiError } from './requestErrorConfig';
import { LinkOutlined } from '@ant-design/icons';
import type { Settings as LayoutSettings } from '@ant-design/pro-components';
import { SettingDrawer } from '@ant-design/pro-components';
import type { RequestConfig, RunTimeLayoutConfig, RequestInterceptorAxios } from '@umijs/max';
import { history, Link } from '@umijs/max';
import { v4 as uuidv4 } from 'uuid';
import defaultSettings from '../config/defaultSettings';
import { AvatarDropdown, AvatarName } from './components/RightContent/AvatarDropdown';
import VideoPlayerModal from './components/VideoPlayerModal';
import { errorConfig } from './requestErrorConfig';
import { adminInfo } from './services/server/login';
const isDev = process.env.NODE_ENV === 'development';
const loginPath = '/login';

/**
 * @see  https://umijs.org/zh-CN/plugins/plugin-initial-state
 * */
export async function getInitialState(): Promise<{
  settings?: Partial<LayoutSettings>;
  currentUser?: any;
  videoUrl?: string;
  loading?: boolean;
  fetchUserInfo?: () => Promise<API.CurrentUser | undefined>;
}> {
  const videoUrl = '';
  const fetchUserInfo = async () => {
    if (!adminSessions?.current) {
      history.replace(loginPath);
      return undefined;
    }
    try {
      const { data } = await adminInfo();
      return data;
    } catch (error) {
      history.push(loginPath);
    }
    return undefined;
  };
  // 如果不是登录页面，执行
  const { location } = history;
  if (location.pathname !== loginPath) {
    const currentUser = await fetchUserInfo();
    return {
      fetchUserInfo,
      currentUser,
      settings: defaultSettings as Partial<LayoutSettings>,
    };
  }
  return {
    videoUrl,
    fetchUserInfo,
    settings: defaultSettings as Partial<LayoutSettings>,
  };
}

// ProLayout 支持的api https://procomponents.ant.design/components/layout
export const layout: RunTimeLayoutConfig = ({ initialState, setInitialState }) => {
  return {
    // actionsRender: () => [<Question key="doc" />, <SelectLang key="SelectLang" />],
    actionsRender: () => [<VideoPlayerModal key="video" />],
    avatarProps: {
      src: initialState?.currentUser?.faceURL || '/brand.svg',
      title: <AvatarName />,
      render: (_, avatarChildren) => {
        return <AvatarDropdown>{avatarChildren}</AvatarDropdown>;
      },
    },
    // 水印
    // waterMarkProps: {
    //   content: initialState?.currentUser?.nickname,
    // },
    footerRender: () => <Footer />,
    onPageChange: () => {
      const { location } = history;
      // 如果没有登录，重定向到 login
      if (!initialState?.currentUser && location.pathname !== loginPath) {
        history.push(loginPath);
      }
    },
    layoutBgImgList: [
      {
        src: 'https://mdn.alipayobjects.com/yuyan_qk0oxh/afts/img/D2LWSqNny4sAAAAAAAAAAAAAFl94AQBr',
        left: 85,
        bottom: 100,
        height: '303px',
      },
      {
        src: 'https://mdn.alipayobjects.com/yuyan_qk0oxh/afts/img/C2TWRpJpiC0AAAAAAAAAAAAAFl94AQBr',
        bottom: -68,
        right: -45,
        height: '303px',
      },
      {
        src: 'https://mdn.alipayobjects.com/yuyan_qk0oxh/afts/img/F6vSTbj8KpYAAAAAAAAAAAAAFl94AQBr',
        bottom: 0,
        left: 0,
        width: '331px',
      },
    ],
    links: isDev
      ? [
          <Link key="openapi" to="/umi/plugin/openapi" target="_blank">
            <LinkOutlined />
            <span>OpenAPI 文档</span>
          </Link>,
        ]
      : [],
    menuHeaderRender: undefined,
    // 自定义 403 页面
    // unAccessible: <div>unAccessible</div>,
    // 增加一个 loading 的状态
    childrenRender: (children) => {
      // if (initialState?.loading) return <PageLoading />;
      return (
        <>
          {children}
          <SettingDrawer
            disableUrlParams
            enableDarkTheme
            settings={initialState?.settings}
            onSettingChange={(settings) => {
              setInitialState((preInitialState) => ({
                ...preInitialState,
                settings,
              }));
            }}
          />
        </>
      );
    },
    ...initialState?.settings,
  };
};

/**
 * @name request 配置，可以配置错误处理
 * 它基于 axios 和 ahooks 的 useRequest 提供了一套统一的网络请求和错误处理方案。
 * @doc https://umijs.org/docs/max/request#配置
 */
type AdminLease = ReturnType<NonNullable<typeof adminSessions>['lease']>;
const authHeaderInterceptor: RequestInterceptorAxios = (options) => {
  const base = options.baseURL || window.location.origin;
  const destination = new URL(options.url || '', `${base.replace(/\/$/, '')}/`);
  if (destination.origin !== window.location.origin || options.url?.startsWith('//'))
    throw new AdminApiError(400, '请求地址不属于当前管理服务');
  const headers = { ...options.headers };
  const isAccount = headers.isAccount;
  delete headers.isAccount;
  const session = adminSessions!.current;
  const lease = adminSessions!.lease();
  return {
    ...options,
    signal: options.signal
      ? AbortSignal.any([lease.signal, options.signal as AbortSignal])
      : lease.signal,
    luoboAdminLease: lease,
    headers: {
      ...headers,
      token: (isAccount ? session?.adminToken : session?.imToken) || '',
      operationID: uuidv4(),
    },
  };
};

export const request: RequestConfig = {
  ...errorConfig,
  timeout: 30000,
  // 新增自动添加AccessToken的请求前拦截器
  requestInterceptors: [authHeaderInterceptor],
  responseInterceptors: [
    [
      (response) => {
        const lease = (response.config as { luoboAdminLease?: AdminLease }).luoboAdminLease;
        lease?.release();
        lease?.assertCurrent();
        return response;
      },
      (error: unknown) => {
        const lease = (error as { config?: { luoboAdminLease?: AdminLease } })?.config
          ?.luoboAdminLease;
        lease?.release();
        lease?.assertCurrent();
        throw error;
      },
    ],
    ...(errorConfig.responseInterceptors || []),
  ],
};
