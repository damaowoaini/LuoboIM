# current354 iOS 原生解析与包装

固定 `ff7bc1f588f935d6315ad0211e68e15e58cd037e`：354 业务源、178 包锁、20 个实现（19 原生）、原 472 输入。原 consumer 的三份文件逐字复用；这不是未来 359 或完整 App 已验收。

手动 workflow 只执行 `readonly → acquire → resolve`。获取段复用 R7 原始静态 core17 和 engine50，核原实际 bind/双锁证据；不执行 Go。独立 SDK shadow 只加入原 core17，保留原件，派生副本恰两份 framework Info.plist 的 MinimumOSVersion 从 100.0 改为 15.0，其他语义与所有静态二进制不变。该适配不代替 R7 Mach-O/R10 链接证据或 App 兼容验收。

固定官方 Flutter3.47.7/tag+commit、engine、Dart3.13.5。首次获取完整工具缓存来源清单，不能称 hermetic。已验 Xcode16.4/16F6 实际工具18需同字节；CocoaPods1.17.0/Ruby/gem 来源在云首次观察并保留，不能预先称已接受。HOME 原值只读；XDG、CP_HOME_DIR/CP_CACHE_DIR/CP_REPOS_DIR、Pub/Clang/Swift 缓存都在 owned root。若宿主已有会覆盖 XDG 的 Flutter/Dart HOME 状态，拒绝而非挪 HOME。

当前工程已经接入 SPM。使用固定 Flutter 的 `build ios --debug --no-codesign --no-pub --config-only --target=lib/main.dart`：官方控制流先真实取 SPM、生成包、安装 fallback Pods，再在实际 App build 前返回。保留真实 Podfile.lock、可能存在的 Package.resolved、完整生成 Package.swift/缓存来源、原/衍生工程和失败快照；仅本地 SPM 图可以没有 Package.resolved，不能编造空锁。旧两 Pod 锁仍不入 472。

WebRTC150.7871.01 独立固定 release ZIP/SHA，与实际 SPM WebRTC.xcframework 每个成员比较。SQLite3.5.2 的 macOS/device/simulator 三个官方 hook 哈希对象仅放入新 shared output，复制零旧 hook dill/stamp；objective_c9.5.0 原 hook/源码保持，只有未来禁网编译实际生成 native asset。解析期不会假称 hook 已被 App 消费。未知 Pod/SPM/native asset 来源仍须 Root 从实际全体产物验收。

`package` 已列出真实 device+simulator arm64、min15、unsigned Xcode 构建命令；`lane.json.approved_native_resolution=null` 在任何命令前硬拒绝，workflow 也没有包装选项。首次解析输出独立 catalogue，保完整原件，catalogue 剔除仅本轮 inode/时间身份，保来源、全锁、工具体身份和实际生成编译源。下一受审 successor 必须绑定该 catalogue 完整 SHA、Root 验收回执，重新获取/解析后精确匹配方能禁网包装；不能依赖已销毁的上一个云工作区。启用前还须补齐实际 App embedding/所有插件资产审计。当前不能把编译命令存在当成已可发布的 App，更不包含签名、安装、GPS/RTC、设备或服务操作。

预算为待审估算：冷20GiB、阶段4GiB起跑，持续1GiB/.2s、全 owned16GiB、每段1800s；复用单 process-group guard，不清共享缓存。原件、失败日志和来源不得回收凑量。原包装6与consumer3纯控制已由Root实际通过且源码未改，复用旧结果；本R1新增2个工具选择纯控制仅准备、未执行。旧 R7/R10 原件不重测、不改身份。

R1 只修 preinstalled Pod 入口发现：有限读取 `/opt/homebrew/bin/pod` 与官方镜像兼容入口 `/usr/local/bin/pod`，不执行 `which`、不扫描 PATH/HOME。两别名若指向同一 canonical 文件可接受，指向两个不同文件、断链、未知布局或用户路径则拒绝。仅接受 Homebrew Ruby gem `bin/pod` 或 CocoaPods formula `libexec/bin/pod`，脚本绝对 Ruby shebang 必须解析为受控 Homebrew Ruby；原 CocoaPods1.16.2 和 config.rb SHA 不放宽。官方镜像源码证明兼容入口的设计，未证明本次 runner 上实际安装版本。

首次 acquire 在任何 Pod 调用前记录 `evidence/selected-cocoapods.json`，固定 canonical 脚本/Ruby 的内容、mode、dev/inode 和系统 gem root；GEM_HOME/GEM_PATH 仅该系统 gem root，HOME 原值不改，用户 gem/RUBYOPT 不继承。acquire 完成绑定 receipt SHA；resolve/package 仅读原 receipt 并重新核对，绝不重新挑另一工具，后两阶段 sandbox 禁止改 selection/acquire receipt。PATH 只含 task Flutter、已选 Pod/Ruby 目录及系统基础目录。加载 gems 仍逐源码保留并拒用户/未知来源。首次工具源记录不等于 Root 原生来源验收。

R1 保持 current354/472/178/20、ff7bc1f 与 `approved_native_resolution=null`；不转成未来公告359，不安装任何 gem/brew，不启签名或包装。源码准备未运行2新控制、Ruby/Pod、readonly或云任务。

R2：run38983283646 已真实选择兼容入口并执行 `pod --version` 得1.17.0，旧1.16.2门槛拒绝，尚未获取App源/core/resolve。此后继显式只接受该次已观察的Pod/Ruby path、bytes、SHA与mode（不绑定易变inode），不接受版本范围。1.16.2/R1全部原件保留。本批新增1身份控制仅准备，原选择器2和包装6/consumer3已绿复用，均未重复执行。

1.17.0官方tag固定commit5493bfd201a3be9325123cfa59142760afe198d7；config.rb与旧版本仍逐字相同9929B/SHA9213f900…，原CP_HOME/CP_CACHE/CP_REPOS环境硬边界没有放宽。新增官方bin/pod、lib/cocoapods.rb、gem_version.rb与config.rb四源硬pin，在首次版本调用前核已选system gem安装体；加载后仍收集全部gem源码并核CocoaPods/Core精确1.17.0。后续resolve/package只沿原selectedreceipt重核同一工具/官方四源，不能替换工具。官方源码匹配和加载依赖证明仍待后续受审真实观察，不把本次--version当原生解析或App成功。

R2仍是历史current354/472/178/20，approval=null、源ref ff7bc1f、coreCA/签名/预算均不变；App已发布359并不会把旧354工具/链接证据升级为359。359/477后继单列计划，不在本R2调用任何获取/解析或包装。
