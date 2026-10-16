# Flutter 云验证（schema v2）

默认只有 `current257` 一个 cohort：257 个业务源码文件，加 51 个固定 SDK Dart/
pubspec/tool 源和 1 个迁移 helper，共 309 个可移植输入。先执行群头像 28 项与
批量移除 36 项（`test/features/group`，合计目标 64），再完整 688 项及严格 analyze。
这些是必须实际满足的预期计数；当前原子采用草稿尚未执行组合 688，不是通过声明。
采用后为 working257，Root 核验完整云端原件前，accepted230/548 仍是历史验收基线。

## 历史复现

`current230` / `booking-custom249` 不是 schema v2 的别名。需要历史 cohort 时明确
checkout 公共提交 `292bea12ddcf250194beaf90ae62570faea1ab46`，使用该提交的 v1
workflow/inputs/loader：run `38852506276` 的 current230 完整 548/analyze0；候选249
目标76和完整624通过，但分析17项失败，没有被采用。后续独立 v8-r2 修复17项后
本机76/analyze0通过；它不能借用父树624冒充自身完整通过。所有原始失败与私有证据保留。
本次退役 `ci/mobile/candidate/` 的23份重复正文；有效业务源仅在 `app/`，不会静默
把249或230标签映射为257。旧源仍由上述不可变提交及原私有封存追溯。

## 精确输入

`inputs.json` 固定每一文件路径、字节数和完整 SHA256，并分别锁257与309聚合。
其中业务源来自 Root sealed257（booking/custom v8-r2 + 群头像 + 批量移除），
`group_repository.dart` 采用已按 hunk 合并的唯一正文，不覆盖其中任一功能。
Loader 拒绝旧cohort/schema、额外/缺失源、symlink、重复路径、候选正文残留和错误摘要。
只读验证不物化工作树；显式 `--output` 只允许 checkout 外的新 canonical 目录。

```sh
python3 ci/mobile-test-tools.py
python3 ci/mobile-source.py --cohort current257
bash ci/mobile-setup.sh current257 "$RUNNER_TEMP/mobile-current257"
sudo unshare --net -- bash "$PWD/ci/mobile-netns.sh" \
  "$(id -un)" "$PWD" "$RUNNER_TEMP/mobile-current257" current257
```

## 工具链、隔离与完整结果

从官方 `https://github.com/flutter/flutter.git` 精确 fetch tag `3.47.7`，验证本地 tag
和 framework `abaf9c523780a608bd46686fd5e53740a07077f8`、engine
`deb287481e3ce9468f3434937ced4240a70539ca`、Dart `3.13.5`，不浮动/降级。
Setup 是唯一可联网阶段：安装 Linux tester 系统库、官方 engine、原锁定依赖。
`flutter pub get --enforce-lockfile` 后不改 pubspec/lock。SQLite3 3.5.2 Linux x64 裸
binary 核上游包内声明与 SHA256
`b17729184e5a2818055ecbddd5ed6642521bfe6e56aafa472330e483c0e2e0d2`，
只 seed 新 shared output；不上传/复用旧 hook dill、stamp、SDK 或 pub 缓存。

测试与分析严格串行，在新 Linux network namespace 内只启用 loopback，无外部
route、无宿主服务；测试可创建自己的 synthetic HTTP fixture。这不是 macOS SBPL
端口/Unix socket 验证，也不是后端联调。HOME/config/PUB_CACHE/TMP 全部新建，
测试不继承 credential、proxy 或应用 URL。每阶段前后复核所有输入。

初始至少4GiB可用；每0.2秒监测1GiB硬底线和source工作树768MiB allocated cap，
每阶段900秒。concurrency1、关闭VM service，不启用coverage/start-paused/实验快跑。
只有 child exit0、完整 start/done、唯一 success footer、精确64/688、0skip（含hidden）、
0error与资源全部通过才接受。最终receipt写入/fsync仍持续监控，迟到失败优先。
6个Python工具单元用例单列，不计入64/688。workflow仅上传evidence：版本、输入、
SQLite receipt、完整raw测试/分析日志、summary与资源，不上传工作树/依赖/设备数据。

## 运行与发布限制

309 是 Linux 源码单元/组件/分析闭包；不是完整原生375闭包，也不证明真实SDK、
设备登录、消息/群写、预约服务、媒体、签名或上架验收。原Android/iOS native8、
协议52、CA/QA配置、AAR/XCFramework、账号/token、数据库和用户数据都不在本CI
上传范围。默认无Android/iOS build job；原生产4ABI AAR不替换。
将来改源码必须显式更新pins/预期计数，旧548/624不能替代新688。
