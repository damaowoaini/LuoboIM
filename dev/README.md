# 本地 Web 与真实后端连接

Web 默认通过同源 `/api` 请求租户路由服务。单独启动 `python -m http.server` 仅能看静态页面，会使服务配置 POST 返回 501，不能作为可登录环境。

先启动本项目实际路由、Chat、IM API 和消息网关及其数据库依赖，再运行：

```sh
node dev/web-preview.mjs --dist /absolute/path/to/verified/dist --route-target http://127.0.0.1:31010 --chat-target http://127.0.0.1:31008 --im-target http://127.0.0.1:31002 --ws-target ws://127.0.0.1:31001 --port 7778
```

复用 `web/node_modules` 中已安装的 Vite，不构建、不自动安装依赖，也不读取 Web `.env`。`--dist` 必须明确指向已构建产物，避免误用旧输出。入口只监听本机；上游只能配置本机 origin，不接受 URL 内凭证、路径或外部地址。

`/api` 保留路径转给路由；`/chat`、`/im`、`/ws`、`/msg_gateway` 去掉入口前缀后转给相应实际服务。租户登录响应中公布的地址也必须真实可访问；这个入口不改写租户响应、不返回模拟配置、不生成登录凭证。

分别验证服务配置返回业务 `errCode: 0`、真实登录、SDK 连接及实际收发消息，才能将环境判定为跑通。服务未启动时保留实际失败，启动入口就绪不代表后端已就绪。

如需本机专用端点适配，可显式增加 `--dev-tenant <已有测试租户ID> --dev-domain <该租户原域名>`。只通过固定 `/__luobo_dev__/endpoints` 返回无凭证的精确身份；未同时配置时返回404，不自动修改业务租户或账号绑定。浏览器需使用包含相应适配的前端版本，生产HTTPS线路规则保持原有行为。
