# Web CI

从仓库根执行 `node ci/web-ci.mjs verify-inputs` 仅核输入；`node ci/web-ci.mjs` 在全新 CI checkout 执行安装及验证。Node 固定 **24.21.0**，输出必须是尚不存在的 `ci-output/web/`。不要在持有本地业务会话的工作区运行完整命令。

流程：`npm ci --ignore-scripts` → 显式 `web/tool/prepare-sdk.cjs` 精确哈希补丁 → 完整 Vitest（当前最低 625）→ Web strict 类型检查 → 原 desktop 类型检查 → Web Vite 构建 → 未采用 direct-edit 候选 before/after（各 33）。单 worker、无文件并行；不运行 Electron、浏览器下载、SDK native 构建、服务、部署或凭证写入。测试和类型失败后仍尝试独立构建及候选验证。安装/SDK 准备失败则停止依赖它们的步骤。

`ci/candidates/web-direct-edit/` 仅含冻结 `7404505c…` 的 7 份原字节（61,668B），没有复制到产品 `web/src`。`web-ci.inputs.json` 记录每文件 SHA、原 before 与当前产品的匹配关系及真实 caller pins。portable config 是单独 CI 适配器，原冻结配置/清单/历史均未改。产品未来采用候选或修改被钉住的 caller 时，需显式审查后更新或移除此临时候选门禁。

before 的非零退出不能直接算通过：必须完整 33 项、9 个原正控通过、无 skip/only/todo、无加载/钩子/未处理错误，而且失败均为 AssertionError。它是预期断言失败的诊断，原 JUnit 中保留红色条目。after 必须 33 项全部通过。`summary.json` 与 `stages.junit.xml` 给出阶段是否符合各自合同，原 JSON/JUnit/log/audit 不被改成绿色。

GitHub 触发为 push、pull_request、workflow_dispatch，只有 contents:read，不用 pull_request_target、secrets、OIDC、self-hosted runner 或跨 PR 共享缓存。报告始终尝试先上传，成功构建另上传 dist 与逐文件 SHA；不上传 npm cache/TMP/整个工作树。若 GitHub 自身制品上传或 runner 中断失败，保留 Actions 步骤日志，不能保证该次制品可下载。bundle 是审阅产物，不代表后端/Go-WASM/Electron 的实际端到端验收。

锁中 Linux x64 的 rolldown 1.2.14、lightningcss 1.33.0 的 GNU/musl optional entries 已静态核齐；该锁未使用 esbuild 本体，未为 CI 换版本。现锁 1,128 entries 只有 117 含 integrity/resolved，既有完整性元数据缺口仍保留；`npm ci` 是否能从公共 registry 完整重建尚待首次 Linux 运行证明，不把本机 node_modules 当锁安装证明。

Actions 使用官方发布 commit 固定：[checkout v7.0.1](https://github.com/actions/checkout/commit/3d3c42e5aac5ba805825da76410c181273ba90b1)、[setup-node v7.0.0](https://github.com/actions/setup-node/commit/820762786026740c76f36085b0efc47a31fe5020)、[upload-artifact v7.0.0](https://github.com/actions/upload-artifact/commit/bbbca2ddaa5d8feaa63e36b76fdaad77386f024f)。此任务不提交或推送，由主任务维护远端。

首轮云运行的 full625 已通过，但候选 before/after 因冻结 fixture 注册晚于 SDK 导入，在收集阶段 Worker 未定义、各 0 用例，属于加载失败。CI config 现把同一冻结 fixture 设为 setupFiles，使既有边界 mock 在测试的真实 hook/caller 导入前注册；未修改 7 个冻结源文件或 33 个断言。本地按该配置实际得到 before9 正控通过/24 AssertionError、after33 全通过，并通过相同 JSON/JUnit/audit 完成门禁；云端重跑仍以 Actions 实际结果为准。
