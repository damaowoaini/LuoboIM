# 萝卜对话地图选点页

独立、无 npm 依赖的 Node 22+ 服务；页面供 App 的官方 WebView 使用。
它只返回候选位置，由 App 的原生按钮负责一次定位和最终发送。
不读取 IM 会话、Cookie、账号配置或令牌，不申请浏览器定位权限。

部署时用本项目高德 Web(JSAPI) Key，并在高德控制台限制为实际地图域名。
`LUOBO_MAP_ORIGIN` 设置为该独立 HTTPS origin，Key 和安全码通过私有环境文件或服务管理器注入。
`.env.example` 仅列变量，服务不自动加载磁盘环境文件；填好后不要提交私有配置。
缺配置时 `/picker/config` 返回明确错误，不加载高德脚本。

启动命令：`node server.mjs`。进程仅监听 `127.0.0.1:8089`，前置 TLS 代理可参考
`deploy/nginx.conf.example`，必须替换域名、证书路径，并覆盖 Host 和 X-Forwarded-Proto。
不要把该端口直接暴露到公网，不要与 IM 的认证 origin 共用。
TLS 证书和域名由部署环境提供，不随仓库签发或上传。

App 构建配置使用固定 `LUOBO_LOCATION_PICKER_URL=https://实际地图域名/picker` 和显式初始地图中心；
页面地址不接受账号设置里的任意 URL。初始中心不代表用户位置。
没有有效配置、地图尚未 ready 或页面发生错误时，App 不应开放定位与发送。

服务只代理固定高德坐标转换、地点搜索等路径，目标始终为 `restapi.amap.com`。
安全码仅在服务端附加；不转发调用者的 Cookie/Authorization，不跟随上游重定向，
限制请求/响应大小、并发、速率和时限。无需自定义地图与通用 HTTP 代理。
页面关闭会作废候选和未完成转换，后到转换不能覆盖新的手工选点。
搜索失败保留手工选点；名称来自用户或实际 POI，不把原始坐标编造成地址。

需要部署验收：实际 Key 授权、JSAPI 的 CSP/资源加载、地图点击/拖动/搜索、
GPS 官方转换、Android/iOS WebView 权限行为和原生发送闭环。
源码/边界测试不证明这些真实服务和设备条件已通过。
尤其不能把 Permissions-Policy 或 Android 的拒绝回调称为旧 iOS 系统硬拒绝的证明。
当前页面不自动兼容旧未标注坐标系的位置消息。

参考：[高德安全码代理](https://lbs.amap.com/api/javascript-api-v2/guide/abc/jscode)、
[官方坐标转换](https://lbs.amap.com/api/javascript-api-v2/guide/transform/convertfrom)。
