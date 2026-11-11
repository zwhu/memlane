# 同身份 helper 替换：下一步最小区分计划

2026-11-11。**仅只读审计与计划，未执行下列修复／替换试验。** 真实失败见 [015回执](../../build/goal-015-helper-replacement-20261111/RESULT.md)、[旧F1](../p4-helper-replacement-validation.md)。不换bundle身份或数据根，不改生产、活签名、系统防护或全局BTM，不创建新模型任务。

## 问题与当前事实

目标是同bundle identifier、同规范App路径、同服务label和数据根，旧helper正常退出后替换新helper，再由SMAppService启动实际包内helper；直接exec成功不是该目标。

- 014原App29608/helper35721/worker35722/official35774已Native明确停止退出后才替换。015完整构建与严格签名检查通过，但明确Resume后SM启动EX_CONFIG78；系统日志c[5]p[1]m[1]e[0]、vc10，精确新helper路径。
- launchctl记录parent bundle identifier、BundleProgram、`has LWCR`与`needs LWCR update`；这些字段证明动态约束存在，**不暴露具体未匹配事实**，不能把needs-update直接当根因。
- 原014完整字节恢复且签名通过，15:55:53再次Resume仍同类失败。因此“恢复文件=恢复健康”不成立，也不能仅归因于015 helper字节损坏。当前final-state.json为014文件、服务停止、job absent、socket不接受，不是健康。
- 015受保护5表前后相同：25 memories/39 versions/14 sources/4 common/3 connections。无新模型或图任务。失败015保留为同scratch的failed015.app、同bundle，未启动。
- root只读LSCopyApplicationURLsForBundleIdentifier初次返回build012/013/014/015，**没有当前规范scratch路径**。targeted-unregister.json随后仅取消build015注册，最新ls-current仍返回012/013/014；这不是已完成候选归一化。build012候选版本0.1.0，013/014为0.0.1；这是真实候选差异，不证明LS版本选择算法或SM采用了哪个候选。

## 已失败探针：不再盲重演

旧F1已分别失败：helper更换后重开、仅-f刷新LS、从新App注销注册、从旧App注销并等job消失再换helper、plist写helper哈希、移Contents/MacOS、版本与helper同时变、producer linker签名与显式ad-hoc签名。只有改App版本但helper不变曾通过。BTM tuple UUID不是Mach-O UUID。当前StopService源码本身包含Unregister；QuiesceForUpdate也明确Unregister并等待owner锁，不能把015一概认定为漏注销。

严格codesign、普通exec、ServiceStatus enabled都不证明SM spawn约束满足。旧Apple主文保存于build/p4-delivery/apple-launch-constraints.md与apple-launch-constraint-facts.md：c5=spawn约束，p1=目标，m1/e0=存在但不匹配；vc10不等于“Developer ID必修复”。

## 下一假设与形成原因

| 假设 | 具体依据／缺证据 | 能区分的最小变化 |
|---|---|---|
| H1：同identifier多完整App候选／非规范URL，影响SM动态父包或spawn约束关联 | 新LS结果只含构建目录，不含真正运行路径；015安装脚本保留rollback/staging同ID.app。**LS结果不是SM内部选中事实**，目前仅关联假设。 | 保持014完整字节、签名、路径不变，只取消本identifier的确切其他URL注册，再注册规范URL；证明候选集合实际改变后单次Resume。与旧F1“仅-f”不同。 |
| H2：即使代码完全相同，新vnode／整包路径置换会改变动态关联 | 015用完整App目录rename交换；旧F1主要在旧包路径直接重建helper，缺严格A→字节相同A的置换控制。尚无inode单变量证据。 | **仅在014健康基线恢复后**，同路径A→A字节相同、签名/CDHash不变的新目录实例；不重签、不改版本/helper。若失败，代码内容变化不是必要条件。 |
| H3：同代码身份签名事实／签名类别造成跨版本不稳定 | 候选helper为linker-signed ad-hoc、Identifier=a.out、TeamIdentifier缺失；App自身是另一个identifier。此形状在旧健康版本也存在，不能直接判错。 | 先离线比较App/helper identifier、CDHash、DR、flags、TeamID、plist，不改签名。正式Developer ID只在有合法条件时对**A与B都用同一Team身份**另做替换基线；不把现有ad-hoc换一种新身份即称成功。 |

H1优先，因为有新实际证据且不需重建／重签；H2在H1后仍不清楚且A确实健康时才做。H3先只读事实，不再重复旧“显式ad-hoc重签一次试试”，也不写自定义弱DR、固定CDHash或关闭约束来绕过保护。

## 第一格：仅归一化本bundle的LS候选（待root明确执行）

1. 保留当前A失败的系统log、launchctl、签名/hash及LS候选原件；记录当前停服标记与job absent。冻结App与所有runtime入口，确认helper/worker/official退出与锁释放；unknown停止，不把原样恢复当健康。
2. 由现成inspect-ls.py再次只读取得**本identifier**确切URL集合，逐一检查Info.plist identifier、synthetic根/已知build目录。只处理返回且在本项目批准目录内的URL；遇非本项目URL立即停止，不能自动注销用户其他安装。
3. 对非canonical候选逐个`lsregister -u <exact URL>`；保留每项退出码。再`lsregister -f <canonical App>`。不使用-reset/-kill/-seed，不遍历整个系统、不清全局BTM、不删除bundle。
4. 再查相同identifier URL，记录规范/解析后的路径。预期仅canonical；如果其他URL仍在或canonical仍缺失，试验前置失败，**不发Resume，不反复-f**。若工具不能保证唯一，也不能把其exit0当集合正确。
5. canonical014文件全部保持原hash；以规范App真正进程的Native“明确启动”单次Resume。记录App Bundle.main.path、SM状态与launchd真正helper路径、IPC peer/worker、same-root及5表摘要。任何约束失败保留即停，不重试B、不另换ID。
6. A若ready，只支持“本次候选集合归一化后旧A恢复”；不能据此说B替换已经通过，也不能精确断言SM用了LS哪个事实。A若仍EX78，H1不足以独立解释，应保留结果进入签名／动态关联事实对照，不能继续清系统记录。

计划命令骨架（**本agent没有执行**，路径逐项来自保存的JSON，不从用户输入拼shell）：

```text
python3 scripts/task.py -- python3 build/goal-015-helper-replacement-20261111/inspect-ls.py
python3 scripts/task.py -- /System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -u <exact approved noncanonical URL>
python3 scripts/task.py -- /System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -f <canonical App>
```

inspect-ls.py会覆盖自己的ls-current.json，执行前应保留before副本；after也保存独立副本和timestamp。该工具是针对bundle的注册URL查询，不是全局BTM清理。原平台status/register/unregister必须在真正对应App上下文执行，不能从standalone Go工具冒充Bundle.main。

## A健康后：最小同身份替换Harness

复用scripts/verify-p4-helper-replacement.py的标记根、真实helper、严格签名/IPC/stop检查，但**不直接运行其旧fixture路径**；后续只在build内准备一个小型Python驱动和显式阶段，不新增生产入口或多环境矩阵。

- frozen manifest：一个canonical App、一个identifier/service label、一个synthetic client/data/root、固定PI/CODEX隔离目录；A/B完整payload以不被LS当完整App候选的惰性归档保留。包结构只在单canonical路径发表。不得复制引擎/模型或改用户默认环境。
- `baseline`：实际A由SM ready、IPC peer对应A helper、worker与数据identity准确、无活任务/计划。没有A健康，后续阶段全部拒绝。
- `noop-swap`（H2必要时）：旧A正常QuiesceForUpdate完整生命周期→job/锁消失→同路径原子发表**字节相同A**→单次注册/Resume→核真实ready。记录App/helper dev/ino、hash/CDHash，但不能把BTM UUID作Mach-O事实。
- `replace-once`：相同步骤A→B。外部只有helper/本批实际包payload正常改变，identifier/path/root/signer类别/PI/授权不变，不另外修改Info.plist环境并重签以制造第二变量。若必须注入测试环境，应A/B在baseline前同样设置且冻结。
- `rollback-once`：B失败后先停并注销该canonical owner，等退出/锁，再原样回A；A也必须实际ready才可说恢复。失败立即停，不无限循环或发新模型。
- 成功证据：至少A ready→B同身份新helper确切SHA ready→重开保持＋明确停服保持，数据/授权保护；A→A通过仅是控制，A→B通过才是替换证据。SM真实启动不可由直接exec替代。无模型/task，所需外部签名条件缺失则如实未验收。

每阶段输出小JSON：step/op时间、bundle/canonical路径、A/B App/helper hash与签名事实、原SM状态、job absent／spawn状态、actual kernel executable、IPC peer、worker/锁、同库identity及受保护表摘要；错误只固定约束枚举与受控目标log，零auth/聊天。候选URL/DR不是实际kernel spawn策略dump，未知保持unknown。

## 边界与决策

这不是自动更新已修复方案，更不是新身份绕过。当前已证是同身份本机ad-hoc替换失败且文件rollback未恢复运行；正式Developer ID/公证/干净机条件仍独立未验。下一步先做H1一格，只有真实新证据才决定H2/H3，不重复全包构建、盲重签或全局重置。所有实际动作继续须root明确授权并经task.py；本文件不执行任何动作。

## 追加：本bundle归一化未达前置与独立路径恢复格

root随后实际精确-u build012/013/014，返回均0；-f当前/private/var/folders下原014路径返回0，但targeted LSCopyURLs结果空，-v/-lint也无输出。**没有发Resume，保持停服。** 上文“只剩012/013/014”为较早快照；本追加为更新状态。不能因命令exit0宣布规范路径已注册，也不能认为“候选集合归一化后A仍失败”，因为canonical入集合前置没有达到。

新可区分假设H1b：原tmp规范位置与LS可登记／SM动态外部约束存在关联。依据是构建目录原可出现在同ID URL列表，tmp运行路径未入列表；并非“tmp一定被禁”。root另实际读取Apple DTS论坛799933，建议排查外部约束与运行位置；它没有证明此tmp路径必是原因，本计划不扩写该结论。

root拟独立“路径恢复探针”：同一完整014从scratch移动到`build/goal-native-runtime-20261111/MemlaneConcentratedTwelve.app`，identifier/config/签名/字节不变，目标原不存在。只用同卷`os.rename`；EXDEV或任何目标冲突停止，禁止shutil.move隐式复制回退。先保存App/helper hash与dev/ino、配置摘要，移动后核字节/签名/dev/ino保持。旧job/owner锁须先消失；不做全局BTM操作。

-f新路径后targeted查询须返回唯一新URL，再由真实App单次Native明确Resume；若仍空或多URL，不Resume。不同时改版本/签名/Info.plist环境，不删失败015。失败只留该格回执并停；成功也仅记**014同ID变路径恢复**，不是原路径更新通过、更不是015 helper替换通过。目标仍同bundle，不能把换路径合理化为换身份验收。

移动会改变helper绝对路径，旧processing dependency失效属正常严格门禁；不自动沿用旧验证或启用，后续模型重验需单独明确授权。原业务data/root/PI_HOME保持原位置，不搬模型/引擎/记忆，不改Agent范围。相同保护表与真实helper/kernel/IPC owner证据仍逐项保存。未来若测试同路径A→B，只能在这个**新规范路径**建立A健康基线后另行进行，不能补称旧tmp路径已修复。

### 已执行路径格结果（root回执，非本agent执行）

已只读核对path-only-move.json与runtime-ready.json：014从原tmp目录同卷rename到上述repo固定路径，9文件摘要全部相同、signed_identity_changed=false、runtime_configuration_changed=false，精确LS注册成功且root确认soleURL。Native明确Resume后App71854、IPC peer/helper72049、worker72058、v11official72183均实际ready；helper内核路径来自新repo canonical包，数据/组件仍原根。

这格**路径恢复通过**：同一代码/签名在本次路径与登记条件改变后重新运行。runtime-ready明确same_path_update_proven=false、model_inference_tested=false；不改写原tmp/F1失败。不能把这一个组合变化唯一归因于tmp禁令、LS算法或某个LWCR字段；它只为下一同路径014→015提供实际健康A基线。

root接续拟在此**同一新canonical路径**做014→015：NativeStop注销→精确LS-u该canonical→正常stage015（记录Info环境和签名预期变化）→替换同路径→LS-f并核soleURL→Native明确Resume。应记录A/B实际payload和预期签名差异；若整个App也更新，这证明受控同身份整包/helper替换，不是“仅helper单变量”实验。若通过，按新规范路径的条件登记，原tmp/F1仍未通过；processing依赖仍需后续显式重验启用，chosen草稿保留不等于模型或任务已验收。本agent没有执行该接续。

### 固定路径014→015也失败；只读assert工具

root实际完成新canonical路径的014→015整包替换，已确保soleLSURL、旧owner全退、精确LS-u/f；仍EX_CONFIG78/c5p1m1e0，见build/goal-015-helper-replacement-20261111/fixed015-launchctl-failed.txt及fixed015-constraint.log。故“单一LS候选＋非tmp路径足以保证helper更新”已被本格否定；H1不是完整修复。上一格014路径恢复通过仍保留，但不外推015替换。当前root另将完整015同卷移动到goal-native-runtime015-20261111做独立功能恢复探针，尚不能据移动声称维护主线10更新通过。

新增**仅build独立工具**`build/goal-015-helper-replacement-20261111/assert-runtime.py`，不改生产：

```text
python3 scripts/task.py -- python3 build/goal-015-helper-replacement-20261111/assert-runtime.py --expected-app <绝对canonical App路径> --expected-helper-sha <64位小写SHA256> [--expected-component 1.6.7-memlane.12] --out <build内不存在的独立JSON>
```

固定读取015/runtime-config.json与source-info、实际synthetic-only标记；不用数据库。逐阶段核包identifier/当前helper文件SHA、只读正式health、同连接peer PID及kernel helper路径、worker组件路径、本bundle launchctl running/peer一致、official kernel路径及私有loopback health；component可选参数指official cognee-service的确切目录版本。只允许同synthetic data根，HTTP不走代理/重定向，错误不输出rawerr/token/正文/wholeconfig。--out独占创建，不覆盖历史失败。

任何未ready退出1并记录阶段固定安全码；旧state phase不被当活owner。该工具核OS当前可执行路径和磁盘文件hash，不宣称读取了进程内存hash，也不是Native按钮或模型验证。没有start/stop/kill/register/retry功能。

已实际跑不启动假路径负例，52084退出1：assert-fake-path-red.json为expected_package/expected_app_missing，拒绝发生在socket观察前，证明不会把未运行／缺包当ready。正例必须等root明确通知目标ready再运行，不在准备工具时自动调用。
