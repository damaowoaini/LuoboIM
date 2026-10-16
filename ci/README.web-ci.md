# Web CI

从仓库根执行 `node ci/web-ci.mjs verify-inputs` 仅核输入；`node ci/web-ci.mjs` 在全新 CI checkout 执行安装及验证。Node 固定 **24.21.0**，输出必须是尚不存在的 `ci-output/web/`。不要在持有本地业务会话的工作区运行完整命令。

流程：`npm ci --ignore-scripts` → 显式 `web/tool/prepare-sdk.cjs` 精确哈希补丁 → 完整 Vitest（采用 direct-edit 后当前最低 658）→ Web strict 类型检查 → 原 desktop 类型检查 → Web Vite 构建 → 冻结 direct-edit 历史 before/after（各 33）。单 worker、无文件并行；不运行 Electron、浏览器下载、SDK native 构建、服务、部署或凭证写入。测试和类型失败后仍尝试独立构建及历史回归验证。安装/SDK 准备失败则停止依赖它们的步骤。

`ci/candidates/web-direct-edit/` 保留冻结 `7404505c…` 的 7 份原字节（61,668B）。其两份 after 产品源现已原字节采用；三份回归文件放在 `web/src/core/groupDirectEditScope/`，只将每个 test 的 fixture 导入提前，不设全局 setup/mock。`web-ci.inputs.json` v2 分别固定 current-after、采用后测试和历史 baseline。旧 beforeCurrent 两 SHA 没有放开：它们以 `baselineProvenance.productBefore` 保留，并核对冻结 before overlay；current-after 同时核当前产品与冻结 after。16 个真实 caller/SDK 配置 pins 未改。

历史 baseline 来自 [ed88107 / run 38845582472](https://github.com/damaowoaini/LuoboIM/actions/runs/38845582472)，云端实际 before9 正控通过/24 AssertionError、after33 全通过。当前采用意味着群资料/昵称/验证设置的 owner、导航、Drawer 和最新角色复核，以及头像上传续接/最新选择/待确认互斥进入产品源码；已发起的上传与写入不被宣称撤销，也没有队列、自动重放或服务器 last-writer 保证。

before 的非零退出不能直接算通过：必须完整 33 项、9 个原正控通过、无 skip/only/todo、无加载/钩子/未处理错误，而且失败均为 AssertionError。它是预期断言失败的诊断，原 JUnit 中保留红色条目。after 必须 33 项全部通过。`summary.json` 与 `stages.junit.xml` 给出阶段是否符合各自合同，原 JSON/JUnit/log/audit 不被改成绿色。

GitHub 触发为 push、pull_request、workflow_dispatch，只有 contents:read，不用 pull_request_target、secrets、OIDC、self-hosted runner 或跨 PR 共享缓存。报告始终尝试先上传，成功构建另上传 dist 与逐文件 SHA；不上传 npm cache/TMP/整个工作树。若 GitHub 自身制品上传或 runner 中断失败，保留 Actions 步骤日志，不能保证该次制品可下载。bundle 是审阅产物，不代表后端/Go-WASM/Electron 的实际端到端验收。

锁中 Linux x64 的 rolldown 1.2.14、lightningcss 1.33.0 的 GNU/musl optional entries 已静态核齐；该锁未使用 esbuild 本体，未为 CI 换版本。现锁 1,128 entries 只有 117 含 integrity/resolved，既有完整性元数据缺口仍保留；安装重建以对应云运行结果为准，不把本机 node_modules 当锁安装证明。

Actions 使用官方发布 commit 固定：[checkout v7.0.1](https://github.com/actions/checkout/commit/3d3c42e5aac5ba805825da76410c181273ba90b1)、[setup-node v7.0.0](https://github.com/actions/setup-node/commit/820762786026740c76f36085b0efc47a31fe5020)、[upload-artifact v7.0.0](https://github.com/actions/upload-artifact/commit/bbbca2ddaa5d8feaa63e36b76fdaad77386f024f)。此任务不提交或推送，由主任务维护远端。

首轮云运行的 full625 已通过，但候选 before/after 因冻结 fixture 注册晚于 SDK 导入，在收集阶段 Worker 未定义、各 0 用例，属于加载失败。隔离 CI config 把同一冻结 fixture 设为 setupFiles，使既有边界 mock 在真实 hook/caller 导入前注册；该修正后来已在上述云端 run 完整验证。产品完整 suite 中新三文件只用局部 fixture-first 导入，不把这些 mock 注入其它 625 用例。新 full658 与 bundle 由下一云端运行验证；未切本机运行 dist。
