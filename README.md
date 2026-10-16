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

- Web：完整测试、严格类型检查、桌面类型检查、Vite 构建，以及隔离的群设置、编辑草稿 before/after 回归。见 [Web CI](ci/README.web-ci.md)。
- Admin：锁定依赖安装、Max setup、核心测试、类型检查和后台构建。见 [后台 CI](ci/README.admin-ci.md)。
- Mobile：当前源码已合并预约、自定义消息、群头像和批量移除。[云端群管理 64 项、完整 688 项和严格 analyze 已通过](https://github.com/damaowoaini/LuoboIM/actions/runs/38854472595)，原 548 项用例全部保留。见 [移动端 CI](ci/mobile/README.md)。这不代替当前版本的原生构建和真机验收。
- Backend：`server`、`chart`、`route` 分别执行 Go race 测试和 vet。另有一次性 Mongo 副本集与 Redis 集成任务，强制执行 54 项事务、资源归属、会话与服务凭证用例；任意必跑项跳过或清理门禁失败都会使任务失败。见 [后端集成 CI](ci/backend-integration.README.md)。其他跳过项单列，不能计为集成验证通过。
- 群权限事务基础候选：独立临时目录验证真实 Mongo、固定驱动 Mock、提交未知结果和缓存恢复机制，尚未接入业务 RPC。见 [验证范围](ci/group-authority-foundation.README.md)；通过也不代表完整并发权限整改完成。

`ci/` 保留隔离候选及已采用修改的历史 before/after。CI 只验证，不自动切换产品源码或运行服务。云端结果不等于真机、多端媒体或线上部署验收。

## 配置与发布范围

当前提交采用干净源码快照，保留本仓库初始提交；本地历史、私有配置、数据库、登录凭证、测试日志和旧构建缓存未上传。
服务配置从 `server/config/config.example.yaml`、`chart/config/config.example.yaml` 复制后配置；路由参考 `route/.env.example`。实际凭证通过部署环境提供，勿提交。

定制原生 SDK 的二进制和完整可复现源码尚未完成交付核验，本快照不携带 AAR、iOS framework 或设备产物；默认云任务验证 Dart/Linux 源码，不声明可直接构建发布版 APK/IPA。
Android 16KB 页对齐、原生 SDK 来源/授权、签名、旧版迁移安装、真实设备及完整后台回归仍待完成。功能范围保留移动端 403 项、Web 107 项、后台 89 项契约，详见 `project.json` 的完成标准。

各子目录保留原有 LICENSE 和版权声明；原生第三方组件的授权需要独立核验。
