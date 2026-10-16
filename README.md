# 萝卜对话 · LuoboIM

基于 OpenIM 的多租户即时通讯项目，当前处于功能重构和逐项验收阶段。
移动端使用 Flutter 重写；Web 逐步重构；后台保留现有管理流程；后端保留消息协议与核心，并调整业务、鉴权和资源归属。

## 源码目录

| 目录 | 用途 |
| --- | --- |
| `app/` | Flutter 移动客户端与测试 |
| `web/` | React Web、Electron 包装与测试 |
| `admin/` | 管理后台 |
| `server/` | OpenIM 消息服务与扩展业务 |
| `chart/` | 账号、管理、朋友圈等业务服务 |
| `route/` | 多租户路由、注册和验证码接入 |
| `packages/flutter_openim_sdk/` | 定制 SDK 的 Dart 和平台包装源码 |
| `migration/` | 旧版本地资料的显式导出与导入辅助 |
| `ci/` | 云端验证工具、固定输入及未合入的候选 |

## 云端测试

[GitHub Actions](https://github.com/damaowoaini/LuoboIM/actions) 在 push、PR 或手动触发时执行，保留成功和失败的日志、测试报告及可下载的 Web 构建。

- Web：完整测试、严格类型检查、桌面类型检查、Vite 构建，以及隔离的群设置候选 before/after 回归。见 [Web CI](ci/README.web-ci.md)。
- Admin：锁定依赖安装、Max setup、核心测试、类型检查和后台构建。见 [后台 CI](ci/README.admin-ci.md)。
- Mobile：两个独立 Linux 任务分别验证当前 548 项测试，以及预约/自定义消息候选 73 项后运行完整 621 项；同时执行 analyze。见 [移动端 CI](ci/mobile/README.md)。这些数字是固定基线和验收要求，实际是否通过以对应 Actions 结果为准。
- Backend：`server`、`chart`、`route` 分别执行 Go race 测试和 vet。依赖实际数据库或外部服务的跳过项会单列，不能计为集成验证通过。

候选保存在 `ci/`，通过隔离覆盖验证，不直接改写当前产品源码。云端结果不等于真机、多端媒体或线上部署验收。

## 配置与发布范围

当前提交采用干净源码快照，保留本仓库初始提交；本地历史、私有配置、数据库、登录凭证、测试日志和旧构建缓存未上传。
服务配置从 `server/config/config.example.yaml`、`chart/config/config.example.yaml` 复制后配置；路由参考 `route/.env.example`。实际凭证通过部署环境提供，勿提交。

定制原生 SDK 的二进制和完整可复现源码尚未完成交付核验，本快照不携带 AAR、iOS framework 或设备产物；默认云任务验证 Dart/Linux 源码，不声明可直接构建发布版 APK/IPA。
Android 16KB 页对齐、原生 SDK 来源/授权、签名、旧版迁移安装、真实设备及完整后台回归仍待完成。功能范围保留移动端 403 项、Web 107 项、后台 89 项契约，详见 `project.json` 的完成标准。

各子目录保留原有 LICENSE 和版权声明；原生第三方组件的授权需要独立核验。
