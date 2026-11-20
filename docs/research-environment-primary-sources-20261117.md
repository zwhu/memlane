# Memlane 环境准备与运行稳定性：官方依据

核验日期：2026-11-17。只读调研；未安装依赖、启停服务、修改业务或日常配置。“建议”均为方案推论，不代表现有能力。

同日用户修订：所有依赖均先检测并复用已有兼容项，缺失时先询问用户，同意后才安装。下文受管制品、固定目录与离线装配建议仅适用于获准补装的部分，不构成默认另装 Python/Cognee/模型的理由；已有资源保留外部所有权。完整方案已同步[HTML 技术报告](environment-readiness-reliability-20261117.html)。

## 系统后台生命周期

**官方事实：** macOS 13+ 的 SMAppService 管理 App bundle 内 LoginItem、LaunchAgent、LaunchDaemon，注册仍受用户批准约束；应用应核对授权状态。LaunchDaemon 需要系统级授权。[SMAppService](https://developer.apple.com/documentation/servicemanagement/smappservice?language=objc)、[helper 迁移](https://developer.apple.com/documentation/servicemanagement/updating-helper-executables-from-earlier-versions-of-macos?language=objc)。launchd 可以按需启动或依 KeepAlive 尝试保活；过快退出可能影响重启。[Apple launchd 指南（存档）](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html)。

**建议：** 用户级 helper 管理单实例与所属子进程；区分已注册、等待允许、进程存活、服务就绪、模型可用。尊重用户停用，外部模型故障不重启数据库。当前 `docs/design.md` 仍规定 dev 重启电脑后手动启动，系统持久监督属待实施设计。

**未知：** MyGo/Go 桥接、App 移动/替换后的注册、多用户登录和完整子进程退出行为。

## 签名与公证

**官方事实：** Apple 公证检查恶意内容和签名问题，不保证运行正确。Developer ID 分发要求有效签名、Hardened Runtime、安全时间戳；可 staple ticket。自定义提交使用 notarytool。[Apple 公证](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution/)、[自定义流程](https://developer.apple.com/documentation/security/customizing-the-notarization-workflow)。

**建议：** GUI 与独立下载组件各有签名责任；Python 原生扩展的架构、动态加载及必要 entitlement 均需干净机器验证。GUI 已签名不证明后下载组件可执行。

## Python 环境与稳定路径

**官方事实：** uv 可安装 managed Python，安装目录可指定；移动基础解释器不会自动修复既有 venv 引用。[安装 Python](https://docs.astral.sh/uv/guides/install-python/)、[Storage](https://docs.astral.sh/uv/reference/storage/)。普通 venv 常含绝对路径，一般不可搬迁。[Python venv](https://docs.python.org/3/library/venv.html#how-venvs-work)。uv `--relocatable` 仅保证标准入口等有限范围，二进制保持原样；默认可搬迁功能仍列为 preview。[uv venv](https://docs.astral.sh/uv/reference/cli/#uv-venv)、[Preview](https://docs.astral.sh/uv/concepts/preview/#relocatable-envs-default)。

**建议：** 下载校验后，先落位不可变版本目录，再创建 venv，验证后切 active 引用。不要在 staging 创建普通 venv 后 rename。保护其基础解释器路径。整环境解压交付须另验不同用户、空格/中文路径和全部 native 库。

## 锁定与离线

**官方事实：** `--locked` 检查元数据与锁文件一致；`--frozen` 使用锁文件而跳过此检查；`--no-sync` 跳过同步。默认 sync 会移除目标依赖外的包。[uv 锁与同步](https://docs.astral.sh/uv/concepts/projects/sync/)。`--offline` 只用本地文件/缓存，缺包仍失败。[uv CLI](https://docs.astral.sh/uv/reference/cli/)。

**建议：** CI 固定解释器、传递依赖、插件和 adapter，收齐对应平台 wheel 与资源；用户端安装已验证组合，运行时直接调用固定路径，不重新求解。锁文件不等于离线包；须禁网、清缓存安装验证。不要对用户共享 Python 做 exact sync。

## 跨存储备份

**官方事实：** SQLite Backup API 可生成该库的一致快照，持续写入可能让备份重启。[Backup API](https://www.sqlite.org/backup.html)。WAL 属持久状态，遗漏可能丢已提交数据或损坏库；WAL 不支持网络文件系统，多 ATTACH 库也非整体原子。[WAL](https://sqlite.org/wal.html)。

**建议：** 先停止接纳新写入、核清在途操作，再按各后端支持的方法备份关系库、图/向量库、原文、权限及必要关联，记录同一代 manifest、版本与水位。无法证明在线跨库一致时采用受控停写快照。多个 cp、单库 SQL 备份或“重建索引”均不能证明整库可恢复。

**未知：** 固定 Cognee 后端的一致备份协议，以及全部直写入口能否被同一写入屏障约束。

## 更新信任与回滚

**官方事实：** TUF 用签名角色、版本与有效期处理旧版本回退、无限冻结、混搭及密钥轮换等问题。[角色](https://theupdateframework.io/docs/metadata/)、[威胁](https://theupdateframework.io/docs/security/)、[规范](https://theupdateframework.github.io/specification/latest/)。

**建议：** 至少用内置信任根验证签名 manifest，绑定平台、组件兼容组合、版本、长度、哈希和有效期；失败保留原可用版本。HTTPS 加哈希不能证明 manifest 来源可信。简化签名方案不能宣称完整 TUF 防护。本机故障回切仅允许可信兼容版本；不可逆 schema 迁移后回切二进制不等于数据回滚。

## 需要先消除的未知

完整 Python/native 物料交付、系统生命周期、跨存储恢复一致性，是正式安装与稳定性承诺之前的三个关键验证。环境就绪应代表“版本组合＋稳定路径＋数据归属＋能力回执”成立，而非安装命令退出码为零。
