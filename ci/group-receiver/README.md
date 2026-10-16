# 隔离接收侧首次云验证（未运行）

此胶囊不接入任何业务 RPC、不切运行服务、不做迁移或真实推送。源沿用已验 stage1 的927公开输入、12 foundation、15 effects；receiver覆盖41个 Go 文件，仅替换6个既有路径，新增35个，合成989输入。两个 Mgo 文件取 full-wiring-v3 的受保护模型，仅用于真实同事务删除与成员历史；其余14RPC源码不在本胶囊。

根任务复审后复制 `source/server/` 到公开快照 `ci/candidates/group-receiver/source/server/`；本目录4个payload到 `ci/group-receiver/`。不在本地运行。沿用现有 backend-integration prepare/check-ready/collect/cleanup；不能只看 tests 步骤，必须终态核容器精确销毁、Mongo非系统DB为0、Redis无永久/未知残留。

云环境固定 Go1.27.2、GOWORK=off、GOTOOLCHAIN=local、GOMAXPROCS2/GOMEMLIMIT900MiB；仅全新 GitHub-hosted Linux runner，Mongo `127.0.0.1:37017` 的 luoboIntegration 副本集、Redis `127.0.0.1:36379/15`。复用现有官方镜像digest和可写主节点探测，不使用本地凭证。每项运行1GiB/.2s硬门禁，首物化4GiB，workspace独立tmp/modcache/buildcache，stdout/stderr分开，失败保原件，不自动重跑。

`python3 -B ci/group-receiver/run.py --verify` 仅校源。正式 `python3 -B ci/group-receiver/run.py` 串行6包目标测试（预期63top44sub，实际JSON全集权威）及9包vet；每个目标必须运行且零skip。`test_run.py` 仅4个新控制器纯单测源，当前0执行。新名单不把SDK2项源覆盖测试算作服务端结果。

其中21top18sub为原 stage1 effects 回归；新服务端40top加现有消息持久化2个纯正控，共63top。Mongo源测试包含实际seq/receipt/消息/义务同TX、commit reply lostACK/重启、普通消息与通知同CID竞争、删除/清空/retention与迟返barrier、typed Dismiss原成员子集与真实Mgo成员历史。2个Redis目标使用exact3key原子claim/compare cleanup；网关5项只用生成gRPC+bufconn桩，非真实JWT/WS/设备ACK。Dismiss缓存失败是边界adapter注入，不能冒称真实Redis故障链。

已存在 `TestActualKafkaMongoFailureRetainsOffsetAndCacheUntilRecovery` 需要另一个Kafka夹具，本次未选、不算pass、不改成skip。普通消息旧Kafka offset/async消费者整体切换、性能、SDK Seq覆盖、真实推送/原生均需后续验证。

当前不支持的offline/vendor与3类push callback，通过新 groupdelivery guard 在最小lane准入前整体拒绝，未改配置、未静默dummy。该guard和typed义务仍未接14RPC，故运行态不可启用。最终读取到网络发送的撤销间隙、全部旧writer drain/seq种子、MAX_SEQ读取迁移、每个source/runtime输入重绑定仍是明确交付缺口。外部业务after callback未知ACK沿stage1原inspect/确认合同，不自动重发。
