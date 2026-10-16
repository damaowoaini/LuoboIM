# Admin CI

Ubuntu 24.04 / Node 24.21.0，push、PR、手动触发。只读仓库权限，无 secrets、部署、业务 HTTP 或数据库夹具；checkout 不持久保存凭据。

执行 `node ci/admin-ci.mjs`，先校验 `admin-ci.inputs.json` 的 authored 输入、当前命令及 Linux x64 原生依赖解析。随后 `npm ci --ignore-scripts --include=dev`；显式调用锁定的 `max setup` 生成 `src/.umi/exports.ts`，不复用已生成目录。安装或 setup 失败会保留日志并停止依赖它们的步骤。

setup 成功后分别执行当前 `test:core` 等价完整 Vitest、`tsc --noEmit`、`max build`，互不以另一项失败为跳过理由。核心基线为 22 个文件、至少 93 个用例；实际数量以 JSON/JUnit/完成审计为准。跳过、未处理异常、加载错误、钩子失败和任意失败都让 CI 失败。没有改用 legacy Jest 或缩减到部分测试。

`ci-output/admin` 保存各阶段日志、进程退出记录、Vitest JSON/JUnit、审计和汇总。workflow 在成功/失败时均先上传报告，再上传存在的 dist。失败后形成的部分 dist 标记 `complete:false`，不能称可用构建；产物仅供下载审查。

Max 从项目真实配置构建；CI 不执行 openapi/deploy/gh-pages/写入式 lint，也不替换产品配置。核心测试使用现有 mock transport，不将其计为真实后台/权限端到端验收。npm 安装需要公开 npm registry；这不是操作系统级无网络沙箱。

锁元数据静态核验发现 26 条可按实际父路径解析的 Linux x64 optional 依赖，包括三套 esbuild、三套 lightningcss、rolldown/rollup、TypeScript 7 和 Umi 的相关 native 包；未修改锁文件。静态存在不能代替 Ubuntu 安装/加载成功，真实失败保持红色。

最小复制：`admin/` 全部 authored 源、锁与 public assets（无 node_modules/dist/.umi），本 workflow、`ci/admin-ci{.mjs,.helpers.mjs,.test.mjs,.inputs.json}`、本 README，以及共享 `ci/web-ci.mjs`、`ci/web-ci.audit-reporter.mjs`。共享 runner 被导入时只定义函数，不执行 Web 安装/测试。

本机本批仅准备源文件、读取现有依赖元数据；未进行安装、Max 生成、Vitest、类型检查或构建。5 个 helper 单元源待 CI 实际运行。
