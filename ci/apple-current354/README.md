# current354 iOS 原生解析与包装

固定 `ff7bc1f588f935d6315ad0211e68e15e58cd037e`：354 业务源、178 包锁、20 个实现（19 原生）、原 472 输入。原 consumer 的三份文件逐字复用；这不是未来 359 或完整 App 已验收。

手动 workflow 只执行 `readonly → acquire → resolve`。获取段复用 R7 原始静态 core17 和 engine50，核原实际 bind/双锁证据；不执行 Go。独立 SDK shadow 只加入原 core17，保留原件，派生副本恰两份 framework Info.plist 的 MinimumOSVersion 从 100.0 改为 15.0，其他语义与所有静态二进制不变。该适配不代替 R7 Mach-O/R10 链接证据或 App 兼容验收。

固定官方 Flutter3.47.7/tag+commit、engine、Dart3.13.5。首次获取完整工具缓存来源清单，不能称 hermetic。已验 Xcode16.4/16F6 实际工具18需同字节；CocoaPods1.16.2/Ruby/gem 来源在云首次观察并保留，不能预先称已接受。HOME 原值只读；XDG、CP_HOME_DIR/CP_CACHE_DIR/CP_REPOS_DIR、Pub/Clang/Swift 缓存都在 owned root。若宿主已有会覆盖 XDG 的 Flutter/Dart HOME 状态，拒绝而非挪 HOME。

当前工程已经接入 SPM。使用固定 Flutter 的 `build ios --debug --no-codesign --no-pub --config-only --target=lib/main.dart`：官方控制流先真实取 SPM、生成包、安装 fallback Pods，再在实际 App build 前返回。保留真实 Podfile.lock、可能存在的 Package.resolved、完整生成 Package.swift/缓存来源、原/衍生工程和失败快照；仅本地 SPM 图可以没有 Package.resolved，不能编造空锁。旧两 Pod 锁仍不入 472。

WebRTC150.7871.01 独立固定 release ZIP/SHA，与实际 SPM WebRTC.xcframework 每个成员比较。SQLite3.5.2 的 macOS/device/simulator 三个官方 hook 哈希对象仅放入新 shared output，复制零旧 hook dill/stamp；objective_c9.5.0 原 hook/源码保持，只有未来禁网编译实际生成 native asset。解析期不会假称 hook 已被 App 消费。未知 Pod/SPM/native asset 来源仍须 Root 从实际全体产物验收。

`package` 已列出真实 device+simulator arm64、min15、unsigned Xcode 构建命令；`lane.json.approved_native_resolution=null` 在任何命令前硬拒绝，workflow 也没有包装选项。首次解析输出独立 catalogue，保完整原件，catalogue 剔除仅本轮 inode/时间身份，保来源、全锁、工具体身份和实际生成编译源。下一受审 successor 必须绑定该 catalogue 完整 SHA、Root 验收回执，重新获取/解析后精确匹配方能禁网包装；不能依赖已销毁的上一个云工作区。启用前还须补齐实际 App embedding/所有插件资产审计。当前不能把编译命令存在当成已可发布的 App，更不包含签名、安装、GPS/RTC、设备或服务操作。

预算为待审估算：冷20GiB、阶段4GiB起跑，持续1GiB/.2s、全 owned16GiB、每段1800s；复用单 process-group guard，不清共享缓存。原件、失败日志和来源不得回收凑量。新6纯控制仅准备，未执行；旧 R7/R10 与原 consumer 原件不重测、不改身份。
