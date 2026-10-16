# 独立 JVM 上下文 source18 重投影 R1

这是 `android-rich-jvm-context-projection-r1`，保留 v1 原件。只独立重投影两配置/18来源，不运行 App、Go、SDK 或 Android 构建；原 R12 CLI、catalog 和 consumer 均不改。

原 `db185c` 生产 catalog 的 variant 只有 `owner/attributes/capabilities/external` 四键。本入口额外携带已接受 R12 原始完整图 `original-graph.json`（`9c1a08f4…`），绑定其 edge/component/artifact 的原始 variant。只允许有限16个 POM 加入精确三个 String 属性：usage=`java-runtime`、libraryelements=`jar`、category=`library`。首配置12项已在失败原件观察；BouncyCastle第二配置4项仅是固定官方 Gradle 源码预测，实际完整18仍待验证。两个已发布 `.module` 的四键全部不变。旧属性/owner/capabilities/external、请求/父边/选中版本、18 ID/文件名/长度/SHA及元数据来源均不准漂移；未知键也拒绝。

固定 producer 原始 serializer 本就输出 `display`。R1只将这项非授权的 raw 差异单独保留在 `raw_display_deltas_pending_Root`，不把它加入生产四键 catalog，不以本次图自授权。不再允许任意 variant。原本地合成 POM 的 display 确实从 default 变为 runtime；该夹具结果不推广为生产 variant 许可。

online仍由原 finite3/KMP broker 限定 Maven Central 来源并保留真实 HTTP body；offline 用独立 netns重解，要求完整两次raw图字节相同。任何新 catalog/native consumer 后继均须另经 Root 实际验收。原 metadata/18 JAR hash、force/useTarget/新selector拒绝不变；选择原因的文本说明仍仅原始诊断，不是新增来源授权。

真实 HOME不变；JVM缓存/tmp/Git显式隔离。保冷8GiB/阶段4GiB/持续1GiB/.2s/owned4GiB及失败证据；只释放本job已验下载压包，不删共享缓存。线上固定origin是软件约束，不能声称OS断网；offline才启网络namespace。大18-body仅云端保留。

仅准备3项纯控制，全部 UNRUN；旧41/52、已绿两项本地Gradle对照均不重复。当前App/native、线上来源18及完整offline均无新增通过结论。
