# Apple SDK-only cloud ARM64

显式 cohort：`apple-sdk-only-cloud-arm64-r1`；底层 SDK-only 合同仍为 `auditable-sdk-apple-arm64-v1`，不指代任何当前 App。

仅 `workflow_dispatch`，标准公共 `macos-15` ARM64。使用已安装 Xcode 16.4/16F6、iOS SDK 18.5 和 Python 3.12.10，缺失即失败，不自动切换。云端实际工具 SHA 属于当次来源，不能冒充本地 host233。

三个固定入口（先由 Root 审核并采用）：

```sh
python3.12 -B ci/apple-sdk/cloud.py --cohort apple-sdk-only-cloud-arm64-r1 --stage readonly
python3.12 -B ci/apple-sdk/cloud.py --cohort apple-sdk-only-cloud-arm64-r1 --stage setup --work "$RUNNER_TEMP/luobo-apple-sdk-r1"
python3.12 -B ci/apple-sdk/cloud.py --cohort apple-sdk-only-cloud-arm64-r1 --stage build --work "$RUNNER_TEMP/luobo-apple-sdk-r1"
```

setup 冷准入 8GiB、owned 3GiB/1200s；仅固定官方 Go/engine、public core commit、h1 校验 Go 模块获取。保留本 job 精确下载归档，独立上传 products，评审者不自动下载大包。engine 尚无预存 ZIP SHA256，按固定 generation/size/官方 MD5+CRC32C、完整 ZIP CRC 和 50 个原 Flutter member SHA 校验，并记录实际 archive SHA。

build 准入 4GiB，持续 floor 1GiB/.2s，单个实际 worker 进程组，1800s；同一 guard 同时测量整个 owned root 6GiB 和 build 子树 3GiB。原 R1 worker/observer/静态 archive/平台和 minOS/18 Swift+ObjC 真编译链接门禁复用，网络全拒，只有 build 子树可写。SIGTERM/SIGINT 在调用 imported guard.run 前显式转为中断，guard 杀完整精确进程组；额度/取消/monitor 异常/最终采样失败优先于任何 passed JSON。启动与 stage-passed durable 写入在该 guard 的 worker 内，guard 自身最后落盘 resource receipt。

HOME 保留机器实际值并只读；GOPATH/GOMODCACHE/GOCACHE/TMPDIR 等全部显式任务路径，GOENV/GOAUTH 关闭，不改系统或用户设置。旧本机 R1 九文件不改；新 apple_core 副本只有 inputs / environment / prepare 三个函数变化。

**尚未执行**：新 12 个 pure controls、获取、Go bind、Swift/ObjC link 或云 workflow。本地原 parser/control 结果复用，不重跑。新控制可由 Root 在 network-deny/private TMP/resource guard 下只运行 `test_cloud.py`。不要调用 cloud stages 来做 pure unit 检查。

实际成功也仅证明新 SDK 的 device+sim arm64/min15 编译链接和二进制门禁，不证明全 App、签名、模拟器/真机运行、媒体或旧 opaque Enterprise 框架等价。保留 gomobile raw Info.plist MinimumOSVersion100 workaround；真实 Mach-O 部署目标另验。Apple 真实 generated dependency cache miss 仍必须失败，不能拿 Android metadata projection 锁替代。
