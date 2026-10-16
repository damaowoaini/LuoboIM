# Flutter 云验证

这组文件只验证源码的 Linux 单元/组件测试与静态分析。当前已采用的
`current230` 与尚未采用的 `booking-custom249` 使用不同工作树、不同 job
和不同结果，组合候选通过也不会自动修改产品源码。

| job | 业务源码 | 可移植输入 | 实际验证要求 |
| --- | ---: | ---: | --- |
| `current230` | 230 | 282 | 完整 548 项 + analyze |
| `booking-custom249` | 249 | 301 | 预约 59 + 自定义消息 16 项，再完整 623 项 + analyze |

548 已在 Linux 云运行 `38845582450` 和 `38848168319` 完整通过，分析零问题。
候选原v1/v2运行失败和未完成记录保留：原pages编译/route清理问题修后，v2在
pages实际操作等待中900秒超时，只有2项通过，没有执行完整621或analyze。
当前v6修正真实I/O与widget时钟夹具，且修复正常确认时Stateless Widget替换导致
resolve被误拒的候选产品guard；保留17原页面用例，新增同element换controller和
ID已不显示的2个负例。本机macOS实际19页面+2路由=21项通过、0跳过；
新的Linux目标75与完整623/analyze仍待本轮云运行，未采用到current230。
只有进程退出0、唯一success reporter footer、完整预期计数和资源门禁共同通过才算成功。

## 上传范围

`inputs.json` 给出每个允许输入的路径、长度和完整 SHA256：

- 当前 App 的 230 个 `lib/test/tool/pubspec/lock/analysis_options` 文件。
- `packages/flutter_openim_sdk` 的 49 个 Dart library 文件、原 `pubspec.yaml`
  和 fixture messenger 源文件，共 51 个；分析 `tool/fixture_main.dart` 需要后者，
  但 CI 不启动该入口。
- `migration/legacy_personal_export/legacy_personal_export.dart`，2,942 B。
- `ci/mobile/candidate/` 的 22 个原封候选文件：替换 3 个路径、新增 19 个。
  materializer 校验 before，既不覆盖 checkout，也不读取本机 `references/`。

282/301 是这次可移植源码闭包；它们不是原 356/375 原生 QA 闭包。原生工程、
CA、QA 构建配置、AAR/XCFramework、账号、令牌、数据库、日志、旧 `.dart_tool`
和 SDK 缓存都不属于上传范围。输入里的历史证据只保留文件名与摘要，不包含
私有归档的内容。Root 创建干净发布快照时须显式纳入上述文件；它们此前多数
未被本地 Git 跟踪，不能仅上传本地已跟踪文件后假定完整。

默认不上传 Android AAR，也没有 Android/iOS 构建 job。原生产四 ABI AAR
为 28,445,312 B，SHA256
`a886c23c73029a9843350bfcd4ccd0b314c3c21623cdb86d5bc0d8fdfc9a5128`，
小于 GitHub 单文件 100 MiB；若以后增加显式 Android debug job，仍需单独确认
该二进制的发布许可、原生输入及 Maven 下载来源。当前单测不证明它可重建。

## 工具链与依赖

从官方 `https://github.com/flutter/flutter.git` 精确抓取 tag `3.47.7`，验证
framework `abaf9c523780a608bd46686fd5e53740a07077f8`、engine
`deb287481e3ce9468f3434937ced4240a70539ca`、Dart `3.13.5`。Root 已从官方
远端核对 tag 指向该 commit；本机 tracked 文件 clean、exact tag 一致。
CI 仍须实际下载对应 engine/Dart 产物。不存在或不匹配时直接失败，不使用
latest、不降级 Dart、不更改现有 pubspec。

先在新目录执行官方 `flutter precache --linux` 和
`flutter pub get --enforce-lockfile`。完整 `app/pubspec.lock` 原字节固定，
后续每阶段前后复查所有输入；不上传或复用本机 pub/Flutter/hook 缓存。
sqlite3 3.5.2 默认 hook 所需的官方 Linux x64 裸 binary 由 setup 下载，
同时核锁包内声明及 SHA256
`b17729184e5a2818055ecbddd5ed6642521bfe6e56aafa472330e483c0e2e0d2`。
只 seed 新工作树 `shared/sqlite3/build/download-b1772918/libsqlite3.so`，
不复制 hook dill/stamp，不换成 system SQLite。

安装下载在 setup；测试与分析在新 Linux network namespace 中运行，只启用
loopback、无外部 route，不连接宿主的 LuoboIM 服务。预约测试可在该隔离
namespace 内创建自己的临时 HTTP fixture。它不是 macOS SBPL 的端口/Unix
socket 等价验证，也不声称验证真实后端或原生桥。空 HOME、config、PUB_CACHE
和 TMP 均为 job 新建，不继承凭证或 proxy 环境。

## 执行与证据

GitHub workflow 默认并行运行两个 cohort。每个 cohort 内严格顺序执行，测试
concurrency 为 1，不启用 VM service、coverage、start-paused 或实验快跑。
bootstrap 与 phase 初始可用磁盘至少 4 GiB；实际验证每 0.2 秒检查 1 GiB
硬底线及源工作树 768 MiB allocated 上限，超时/监控异常均停止精确 child 组。
最终 passed receipt 的写入/fsync 仍处于监控区间，迟到的失败优先于候选成功记录。

工作流只上传 `evidence/`：版本、source/SQLite receipt、setup/测试/分析原日志、
完整测试计数和资源记录；不上传整个工作树、依赖缓存或设备数据。
`ci/mobile-test-tools.py` 另有 6 个工具单元用例，不计入 548/75/623。

下面是可复用入口。6 个工具用例及 current230 云流程已执行，不代表 v6 候选
的 75/623 已完成：

```sh
python3 ci/mobile-test-tools.py
python3 ci/mobile-source.py --cohort current230
python3 ci/mobile-source.py --cohort booking-custom249
bash ci/mobile-setup.sh current230 "$RUNNER_TEMP/mobile-current230"
sudo unshare --net -- bash "$PWD/ci/mobile-netns.sh" \
  "$(id -un)" "$PWD" "$RUNNER_TEMP/mobile-current230" current230
```

产品源码改变时必须明确更新输入 pins 和预期数量；旧 548 不能替代新候选验证。
已完成的本地夹具检查与云单测均不包含原生构建、设备登录、会议创建、消息
发送、签名或上架验收。
