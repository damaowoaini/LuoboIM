# 萝卜对话后端普通源码

这里的 `server/`、`chart/`、`route/` 是独立的普通 Go 模块。源码已经从固定来源完整物化；从这些目录构建不需要运行 CI 胶囊、source loader 或验收程序。

当前选定来源为 graph `1575/a8c2a6c5c14c4f7e927448d54a983888c543cf4e70b6367540eda7f921326665`、chart `258/3ac25b721224e069fd052a067beb18dd139fcf68f6f96dc893604436cef3ecec`、route `33/888864b0dcc644e1f25f7723f2b306bfb97b4c650943186a76b732b0f023fd8a`。`source-manifest.json` 的文件路径相对本目录，记录全部 1881 个源码/公开辅助文件；`build-plan.json` 列出真实 20 个产品入口和两个辅助入口、原构建参数及模块锁。辅助入口不计入 20 个产品服务。

使用 Go 1.27.2，在各模块目录独立构建，并显式设置 `GOWORK=off GOTOOLCHAIN=local GOENV=off GOAUTH=off`。保留原 `server/go.work`、`go.work.sum` 和所有模块锁；server 原本地 protocol 替换模块位于 `server/pkg/protocol`。不要用新的跨模块 workspace、统一协议版本或重新生成锁文件代替这些来源。

构建目标为 Linux amd64。server 沿原构建使用 `CGO_ENABLED=1` 和 `go build -mod=readonly -p=1`；chart/route 使用 `CGO_ENABLED=0`，并保留 `-trimpath -buildvcs=false '-ldflags=-s -w'`。完整入口与输出文件名见 `build-plan.json`。依赖取得后应核原锁未变，再在离线阶段构建并记录工具、源码和二进制 SHA。此处不包含已构建二进制，也没有新的构建或测试通过声明。

Linux ELF 不能直接在 macOS 上运行。当前 required 控制链依赖 Linux 的进程、网络命名空间和原 owner 观察；Mac 上采用同来源 Linux 环境及显式端口转发，不能将 Darwin fallback 当作完整核心的已验等价物。

这是普通源码发布，**尚未部署或切换运行服务**。真实配置、凭据、SQL 策略、既有数据迁移、签名/排除/读写准入和完整服务图仍按原控制合同处理；源码发布不生成 Ready、token 或许可。公开示例和 SQL 文件不是可直接投入现有租户的配置/迁移授权。待验 R8 leaf `1557/a113ab40c4e125caf040446db735f2e7e632eb18f3611d810587d07f79699a84` 不在本来源中；不能用独立 R8 全树覆盖这里的 graph 源码。
