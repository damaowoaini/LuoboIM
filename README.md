# 萝卜对话 · LuoboIM

基于 OpenIM 的多租户即时通讯项目，当前处于功能重构和逐项验收阶段。
移动端使用 Flutter 重写；Web 逐步重构并与移动端统一“钉蓝”视觉体系；后台保留现有管理流程；后端保留消息协议与核心，并调整业务、鉴权和资源归属。

最新后端普通源码位于 [product/](product/README.md)，可直接按模块构建。顶层 `server/`、`chart/`、`route/` 保留为历史验证基线。当前新后端尚未部署，Web 尚未完成与它的登录和收发消息验收。

## 源码目录

| 目录 | 用途 |
| --- | --- |
| `app/` | Flutter 移动客户端与测试 |
| `web/` | React Web、Electron 包装与测试 |
| `admin/` | 管理后台 |
| `product/` | 当前完整后端普通源码、构建计划与来源清单 |
| `server/`、`chart/`、`route/` | 保留原来源的历史验证基线 |
| `packages/flutter_openim_sdk/` | 定制 SDK 的 Dart 和平台包装源码 |
| `migration/` | 旧版本地资料的显式导出与导入辅助 |
| `ci/` | 云端验证工具、固定输入及未合入的候选 |

## 云端测试

[GitHub Actions](https://github.com/damaowoaini/LuoboIM/actions) 按变更批次执行受影响验证。先本地验证，再统一提交推送；未改动的有效结果保留来源复用。已知问题处理完毕后，才进行一次最终完整回归。

- Web 与移动端继续统一钉蓝风格；前端构建或局部测试通过不等于真实服务链路已验收。
- 后端graph 1583/5404877b40bd33ef0c2a001213708c9448e747b873ca390abd252eef04c50947：新Null1top3实际仍失败，有限观察已把三child共同失败定位到fixture cleanup的协议类（未发现其它主断言类别，不能据此宣称主断言通过）。本批仅在新RESP夹具每帧起点Peek(1)，空缓冲EOF/closed-pipe正常收尾；已有字节的截断帧仍原bounded parser拒绝。生产985c、原parser、有限观察和35纯检查主体不变；只重试失败1top3，旧resolver/cachedWait/RTC/probe/list0派，原oracle2top3历史。原16编译/4scene/cleanup/8产物门及chart259/Admin/Web/Native不变，P20两binding=null和split assembly保持。
- 移动端当前原生任务在 Gradle 配置阶段失败，尚未完成当前 APK、真机、旧版资料迁移和完整后台验收。
- 云任务保留各自成功或失败结果；源码发布和构建产物不自动切换运行服务，也不自动取得读写或 Ready 许可。

## 配置与发布范围

当前提交采用干净源码快照，保留本仓库初始提交；本地历史、私有配置、数据库、登录凭证、测试日志和旧构建缓存未上传。
当前后端配置示例位于 `product/server/config/config.example.yaml`、`product/chart/config/config.example.yaml`；路由参考 `product/route/.env.example`。示例仍需按实际环境配置。实际凭证通过部署环境提供，勿提交。

定制原生 SDK 的二进制和完整可复现源码尚未完成交付核验，本快照不携带 AAR、iOS framework 或设备产物；默认云任务验证 Dart/Linux 源码，不声明可直接构建发布版 APK/IPA。
Android 16KB 页对齐、原生 SDK 来源/授权、签名、旧版迁移安装、真实设备及完整后台回归仍待完成。功能范围保留移动端 403 项、Web 107 项、后台 89 项契约，详见 `project.json` 的完成标准。

各子目录保留原有 LICENSE 和版权声明；原生第三方组件的授权需要独立核验。


本批CI只投影原required-push阶段既有布尔/一次调用计数及封闭ACK计数类别，按原七段顺序标记未确认边界，不导原错误、token/key或其hash，不把边界当唯一首错。fa6同完整Go5404及精确Go工具下Null1top3已实际通过，下一轮严格复用该单项事实且0重派；原graph失败、全包/全项目/Runtime/PID/Ready/P20仍未接受。原16编译/4场景/cleanup/8产物门保持，无ELF缓存复用。
