# 移动端云验证（schema v3）

默认仅接受 `current269`：269份已采用业务源码 + SDK Dart/pubspec/tool 51份 +
迁移helper 1份，共321份固定输入。串行执行全局搜索目标46、完整734和严格analyze。
这些是强制预期，尚未执行的269不能借用父257/688结果。没有新依赖、原生构建或部署。

历史257的run38854472595已实际target64/full688/analyze0通过；原输入digest和
Root receipt保留在 `inputs.json.history.accepted_current257`。230/249/257均不是269
别名；复现旧结果须使用其明确历史提交及该提交的工具/输入。默认job不重复运行旧257。

## 输入与执行

`inputs.json`锁每文件路径/字节/SHA与269/321聚合；新增16份源中4替换、12新增。
仅global-search r1三处显式mounted补充覆盖原v1同文件，所有46测试正文不变。
Loader严格拒绝额外/缺失源、symlink、旧cohort及旧candidate重复正文。
本地主工作区已有的 `app/tool/check.sh` 辅件留在本机，原字节出处单列记录；
它不上传、不计入269/321、不执行。公共发布树与CI不接受额外辅助文件例外。

```sh
python3 ci/mobile-test-tools.py
python3 ci/mobile-source.py --cohort current269
bash ci/mobile-setup.sh current269 "$RUNNER_TEMP/mobile-current269"
sudo unshare --net -- bash "$PWD/ci/mobile-netns.sh" \
  "$(id -un)" "$PWD" "$RUNNER_TEMP/mobile-current269" current269
```

纯Python工具单元23项单列，不计入46/734。这里的个数是prepared，须以实际日志为准。

## 可选参考入口（默认关闭）

仅workflow_dispatch显式启用 `reference_before_entry`，或向netns脚本增加最后参数
`reference-before-entry`。它创建独立 `source-reference-entry`：同一完整269上下文及
同SDK51/迁移helper，只把4个生产文件换成 `ci/mobile/reference/global-search-before`
锁定的原字节。其余新搜索源和测试仍在，避免缺API引发伪RED。

只运行 `global_search_entry_test.dart` 的2项；必须完整收齐：原会话filter正控1PASS，
缺全局入口的具名、真实expect断言1FAIL，child1、唯一false footer、0skip、无隐藏
load失败/超时/panic。任何其它非0、编译/加载失败或不同断言都失败，不能当参考复现。
该阶段绝不称“旧257 full”。纯parser有独立反例门禁。

依赖config仅重定位到此隔离树；不复制旧dill/stamp/编译或hook输出，只给新shared
目录复制已核SHA的SQLite原始binary。参考阶段finally把隔离树4份源恢复为after269，
复验全部321及主工作树；partial禁止复用。该可选阶段失败仍保留完整日志，后续阶段停止。

## 保留的工具、网络与资源合同

官方Flutter tag3.47.7、framework `abaf9c523780a608bd46686fd5e53740a07077f8`、
engine `deb287481e3ce9468f3434937ced4240a70539ca`、Dart3.13.5不浮动。
Setup是唯一联网阶段，使用原pubspec/lock与 `pub get --enforce-lockfile`，SQLite原始
binary精确SHA仍由官方锁包交叉核对；不复制旧生成cache。之后在全新Linux network
namespace内只开loopback，无外部路由、无宿主fixture；不继承credential/proxy/业务URL。
这不等于macOS SBPL或真实服务联调。

初始4GiB，持续0.2s检查1GiB底线；主source与可选reference合计allocated上限768MiB，
每阶段900s、concurrency1、关闭VMservice，禁止coverage/start-paused。每阶段前后
复核固定业务/SDK输入；reporter要求完整start/done、唯一bool footer、精确46/734、
0skip（含hidden）/0error。最后receipt fsync期间继续监控，任何latched failure优先。
workflow always上传evidence，包括失败日志/summary/resource，不上传源码工作树、缓存、
账号、数据库或native binary。权限contents:read、checkout不持久化凭证。

本轮只证明源码单元/组件/分析。真实native空CID全局搜索语义、全量分配内存边界、隐藏
会话及文件OS打开/原消息定位仍需独立设备探针；public core源码不是原opaque4ABI证明。

## fixture-r2 / optional parser-r2 follow-up

run38857855453原始900s中止、7个普通用例通过、两项widget错误及无footer均保留，
不能算46通过；full734/analyze未开始。根因仍待定位。此轮仅修改
`global_source_navigation_test.dart`，保留全部9个名称/expect及总46断言：
两项真实ChatPage显式runAsync做有界初始化/清理、使用已有captureTheme，记录
startup/mount/settle/assertions/unmount/cleanup阶段；45s单项超时未被放大。
未替换真实ChatPage、未删正例/跳过、未新建selected9替代阶段。
默认push仍强制target46→full734→analyze，原网络/资源/工具链门禁不变。

可选reference parser另收紧：必须恰好两个不同visible test名称；同名额外ID
不再被dict折叠隐藏。新增1项纯Python反例，原严格1具名断言FAIL+1正控PASS不变。
修改后的reference摘要随同一个测试文件重算；仍是同269上下文的4原生产文件
参考变体，绝非旧257 full。此修订的Dart执行、根因证明和native验收仍未完成。

## fixture-r3 follow-up (pending actual cloud)

run38860572719保留42PASS/4ERROR：真实ChatPage两项均到assertions.end，
随后各在cleanup20s中止；另外entry2超时/重入，target900.1s门禁失败，即使child0
也不接受。full734/analyze未启动；不能用旧688替代。

本次只改两个search私有fixture/test文件，不改公共SdkHarness或任何产品源。
先按原清理顺序释放pending/controller/API，再有界等待同一真实session.dispose、
gateway.dispose和原sdk.dispose（幂等检查、channel解绑、native-folder清理）；
成功后才卸载provider-owned runtime，失败finally仍卸载且错误不吞。
保留全部46名称/expect、真实ChatPage/入口及原mock边界，mount/release都有阶段标记。
这一顺序与已验chat_page_test一致，仍需新云证据证实；无native完成声明。
默认流程和所有网络/资源限制不变：23纯Python门禁→target46→full734→analyze；
不增加替代性selected9阶段，不复制本地tool/check.sh。

## analyze-r4 follow-up (pending exact new cloud)

run38863026938完整target46及full734都PASS，隐藏加载/跳过/错误均严格检查；
旧688全部完成的visible名称按multiset为734子集。两项真实ChatPage的session、
gateway、SDK/channel/native-folder清理均结束后才unmount，entry2也通过。
该run仍未整体验收：严格analyze仅两处if缺大括号info而失败。
r4只给原continue/return分支加等价大括号；不更改条件、46/734测试或任何runner，
不关闭fatal-infos。新源仍须按23门禁→46→734→analyze全部实际通过。
原r1/r2超时和r3成功测试/失败分析原件均保留；native/真实服务验收不由此代替。
