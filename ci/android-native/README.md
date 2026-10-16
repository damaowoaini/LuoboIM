# Frozen current257 auditable Android debug

独立 manual-only workflow `android-debug.yml` 固定 cohort
`current257-auditable-arm64-acquired-lock-v1`，从公开314ac556取420个固定正文，
叠加7个明确controller输入，实际427输入。相对历史426仅替换builder并新增
18776B的acquired-go.sum。历史426/原source810与旧证据不改；后续269必须另封
明确cohort和完整向量，不能静默借新App源。

默认只读：`python3 -B ci/android-native/run.py --cohort current257-auditable-arm64-acquired-lock-v1`。
它只核元数据和7个core输入，不联网/物化/读取mutable App。

四个单次claim阶段：

1. setup（联网）：固定源码和官方工具，原锁pub/Go依赖，job新CA；仅prepare-only
   重放Go patch用于预热依赖。原source go.mod/810先验通过；获取后go.mod必须
   不变、go.sum精确8eb9。raw acquisition锁与graph保留。CA私钥立即删除，绝不上传。
2. core（离线netns）：原builder在另一个新目录重放公开core固定commit+补丁；
   原GoMod/810先验通过后，显式debug-QA flag原子植入受审8eb9；默认/tag TLS
   测试及gomobile ARM64 bind均-mod=readonly且前后锁一致，再做API/回调/AAR/ELF核验。
3. gradle-setup（联网）：校验官方repo与实际Maven下载URL/每文件SHA，拒dynamic；
   生成本job验证元数据。这是首次实际取得的来源证据，不冒称预先hermetic锁。
4. apk（离线netns）：7负门禁、strict dependency verification、实际wrapper29/
   native桥和`lib/main.dart`编译；ARM64 split APK、ZIP/包/签名/zipalign/逐ELF核验。

setup及gradle-setup是联网获取阶段，离线stage只开自有loopback，无宿主fixture/业务服务/设备。执行器
把GOPROXY=off带入原builder，因此两次TLS测试和bind都不能补下载。
`gomobile init`含gobind@latest，故明确不调用，只安装固定module版本两个工具。
Android/Dart不注入CA资源或私defines。core保72bb的显式debug局部jobCA标签，
只影响规范的10.0.2.2:34443 API/WSS，正常SAN/时间/链校验，不冒称完全标准信任。

冷起跑>=20GiB，后续stage>=4GiB，持续1GiB/.2s，owned cap16GiB。
预算是估算非实测：工具/SDK4–5GiB；Flutter/pub约2；Go模块/cache/bind临时3–4；
Maven1–2；build/APK/evidence1–2。仅每个完整校验和解包后的job下载压包，先落SHA
receipt再精确unlink；不删离线依赖、共享预装或失败日志。20GiB不满足即0setup。
成功receipt写入/fsync仍poll，末次sample/join之后检查latched失败；failure文件、
非zero和GitHub实际终态永远优先。final-resource记录其写入时的snapshot，最终sample
的精确minimum/peak也输出在终态JSON；不能单独拿success-candidate当PASS。

来源/工具链在inputs.json/toolchain.json：Go1.27.2、固定x/mobile、Flutter3.47.7、
JDK17、Gradle9.3.1、AGP9.1.1/Kotlin2.4.0、SDK37.0/36、NDK28.2。SDK raw官方
metadata记录所选Linux archive及checksum，sdkmanager内部校验并核平台jar；不把
没有下载过的Linuxzip伪写成既有SHA256。NDK官网只给SHA1，实际下载另记SHA256。
版本/SHA/404错误即停止，不降级或偷偷联网重试。

上传仅新AAR/build.json/generated Java源码jar、新debug APK/public CA/许可证和
证据，不上传Enterprise、旧821、旧CA/config/依赖缓存/私钥。当前257的688是
Dart基线；新原生只能用实际本job结果验收。153实际调用与15callback实现点的
source对照不是新Java编译结果；新绑定/29wrapper都必须实际通过。未用旧
setForServiceListener仍是完整Enterprise二进制ABI缺口。

包名仍com.luoboduihua.imapp，用job新debug签名；不据此覆盖旧安装或回滚DB。
原生产4ABI/iOS/真实Android16KiB/媒体/设备/生物识别/物理删除/发布签名仍未证明。
本次仅源准备与Python单位源码，没有本轮native结果或远端操作。

本runner依原锁pub源码补独立dependency-additions：jni1.1.0需API35与CMake3.22.1；flutter_webrtc1.6.2+hotfix.4声明JitPack audioswitch固定039a35a… commit。只允许此模块/commit，不放开其它JitPack来源；新cohort继续逐字沿用历史toolchain.json，原source锁SHA未改。
