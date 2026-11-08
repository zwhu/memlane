# 本地开发与验收

当前已有真实 MyGo Native UI、独立 Go 记忆服务、Rust/Python 原文 worker 和外置组件管理。Rust 为本轮 App 原文索引路线，Python 归档按同一契约完成对照；App 自动准备、后台系统注册、生命周期与受控 HTTPS 更新已实测。P1 记忆管理与 P2 真实 Codex/pi 互通、App Agent 管理和系统窗口对照均已通过。未保存草稿只在窗口内存中保留，退出后不保存。最新测量和验收边界见 [P2 验证记录](p2-validation.md)，未受影响的能力引用 P0/P1 证据。

## 桌面客户端

首轮验证环境为 macOS Apple Silicon。Go 工具链、模块和编译缓存保存在仓库 `.local/` 与 `.cache/`，均被 Git 忽略；脚本不修改 shell 配置、系统 Go 或用户默认 Python。

```sh
python3 scripts/bootstrap.py
sh scripts/go.sh mod download
sh scripts/go.sh test ./...
sh scripts/go.sh tool mygo dev
```

Go 固定 `1.27.3`，MyGo 固定 `v0.7.4`，依赖校验见 `go.sum`。工具链下载约 68 MB，仅用于开发，不进入客户端。默认 Go proxy 若超时，可以在单次命令中指定可访问的镜像：

```sh
GOPROXY=https://goproxy.cn,direct sh scripts/go.sh mod download
```

生成 `.app` 与 DMG：

```sh
python3 scripts/build-client.py --out build/p0-final
```

完整输出位于 `build/p0-final/darwin-arm64/`。原始 `sh scripts/go.sh tool mygo build -skip-notarize` 仍可检查框架构建，但不会完成 helper/LaunchAgent 后处理，P0 主包使用上面的 builder。当前使用 ad-hoc 签名，仅用于本机开发和体积验证，尚未完成 Developer ID 签名、公证或正式分发。主包不附带 Cognee、Python、Rust 编译器、模型或开发缓存。

每次加入依赖后重新测量主包；输出不把尚未测量的引擎与模型记为 0：

```sh
python3 scripts/measure-package.py build/p0-final/darwin-arm64/memlane-0.0.1-arm64.dmg build/p0-final/darwin-arm64/Memlane.app
```

无窗口输入验证与截图：

```sh
MEMLANE_SCREENSHOT_DIR="$PWD/build/p0-screenshots" sh scripts/go.sh test ./internal/desktop -count=1
```

它覆盖未连接状态、中文组合输入、复制、切换页面保留草稿、保存不可用和缩放。无窗口模拟不能代替 macOS 输入法候选窗、VoiceOver 和真实窗口验证。

实际窗口的自动化构建只用于本机 P0：

```sh
sh scripts/go.sh build -tags e2e -o build/memlane-e2e ./cmd/memlane-app
MYGO_AUTOMATION=/tmp/memlane-p0.sock build/memlane-e2e
```

保持进程运行，在另一个终端执行 `python3 scripts/verify-window.py`，验证真实窗口、草稿保留、保存禁用、更新状态、关闭隐藏和重新打开；截图与 JSON 报告在 `build/p0-screenshots/`。运行结束后用 Cmd+Q 退出测试进程。

正式构建不包含自动化插件。菜单栏支持重新打开窗口，Cmd+W 收起窗口，Cmd+Q 或菜单退出应用；默认退出保留后台，主动停止或关闭“退出 App 后继续运行”会等待服务和 worker 退出。

## Cognee-RS 构建候选

固定源码提交为 `151905c302fc9b5b9aad88d6b13931c09f0eac30`。准备脚本要求本机已有 `uv`，将 Rust `1.91.1`、CMake `4.1.2`、protoc `33.1`、源码与 ONNX Runtime `1.28.0` 放在 `.local/`：

```sh
python3 scripts/bootstrap-engine.py
sh scripts/build-engine.sh
python3 scripts/package-engine.py
python3 scripts/verify-engine.py --archive build/p0-engine/cognee-cli-p0-darwin-arm64.tar.gz
```

构建开启 `sqlite,ladybug,lancedb,onnx-dynamic,hf-tokenizer`，禁用其他默认 feature；`onnx-dynamic` 包含 `onnx` 并改为动态加载。它包含真实本地持久存储和推理接口，ONNX 动态库必须随引擎组件提供。上游未提交 `Cargo.lock`，本轮生成的候选锁文件已归档于 `deps/cognee-rs/Cargo.lock`，脚本复制该锁文件并使用 `--locked`。依赖解析被冻结不代表功能已通过验收；调整依赖时须记录原因并重新验证。

已构建的 CLI 位于 `.cache/cognee-target/release/cognee-cli`，构建限制为 4 个并发任务，不启用发布调试信息。实验归档位于 `build/p0-engine/`，约 80.94 MB，不含模型。它仍不是 Memlane 自定义 JSONL worker，也未接入 App 自动下载器。

验证脚本将 CLI 与动态库放到独立临时目录，隔离配置和日志，不继承凭据；使用两条合成记录验证跨进程持久化及 dataset 归属，并通过 ONNX C API 检查动态库加载。脚本只访问临时数据库；完成后清理临时目录，将报告保存在 `build/p0-engine/verification.json`。这些检查不代表语义检索、跨项目查询隔离、模型推理效果或编辑删除已经通过。

无模型和 LLM 配置时，当前 CLI 的 `cognify` 明确报错：自动选择了本地 `gliner` 图谱提取器，但该构建没有注册它。下一步需要验证独立原文索引入口及模型桥接，不能把 `add` 成功当成已经可检索。原生依赖的开发缓存占用不代表发布引擎大小。

## 本地 embedding 候选验证

逐步结果和失败记录见 [P0 执行记录](p0-execution.md)。新增 `sh scripts/build-embedding-probe.sh` 构建真实 Cognee-RS ONNX 探针，复用固定依赖与本地编译缓存；探针源码保存在 `engine/rust/`，脚本复制到忽略的上游源码目录中作为独立 binary，不修改上游依赖锁文件。

`python3 scripts/bootstrap-model.py --manifest deps/models/multilingual-e5-small-int8.json` 按固定清单准备模型并校验文件；`python3 scripts/verify-embedding.py --manifest deps/models/multilingual-e5-small-int8.json` 在临时目录运行模型，使用合成中英文和代码标识符输入。多语言候选已通过三条 Top-1 smoke，报告包含实际耗时与峰值 RSS。首次 BGE 中文候选未通过英文查询，失败报告仍保留；尚未把 smoke 当成完整索引检索验收。

## 无 LLM 的 Rust 原文索引验证

```sh
python3 scripts/bootstrap-model.py
sh scripts/build-index-probe.sh
python3 scripts/verify-index.py
```

默认模型现在是固定E5-base int8；模型首次文件下载合计295.73 MB。构建脚本先按 `deps/cognee-rs/patches.json` 应用并校验输入/padding适配补丁。验证把索引探针、ONNX动态库与模型复制到系统临时目录，使用合成正文建立真实LanceDB片段索引，再用独立进程检索。报告在 `build/p0-engine/raw-index-verification.json`，包含9条Top-1/Top-3结果、来源范围、长文切片、冷加载、索引时间和峰值RSS。

原BGE与E5-small模型对照保留在清单和执行记录中，不再作为当前原文检索默认。此检查未验收App记忆UI、完整图谱、项目隔离或版本过滤。

## 原文版本与范围契约验证

```sh
sh scripts/go.sh build -o build/p0-engine/memory-probe ./cmd/p0-memory-probe
python3 scripts/verify-memory-contract.py
```

该命令在独立临时目录运行Go业务SQLite和真实Rust模型索引；`cmd/p0-memory-probe`只作验证入口，不进入主包。每个子验收实际执行handoff。覆盖项目A/B与相同原文隔离、显式全局范围、编辑/删除/恢复新revision、刻意保留的旧chunk、伪造绑定和正文、真实引擎缺失、失败持久任务、操作ID冲突、迟到索引结果和重启。报告为 `build/p0-engine/memory-contract-verification.json`；这不代表App记忆界面或Agent接入已完成。

## 更新与组件准备状态记录

客户端使用 `mygo.Updater`。受控 HTTPS 源已实测下载、更新签名拒绝、短传失败恢复、0.0.1→0.0.2 及主动停止时 0.0.2→0.0.3 实际替换；原文保留与兼容组件复用通过。当前 `mygo.json` 没有正式发布源，界面明确报告未启用；Developer ID 与公证仍未配置。

App 已使用真实下载器准备组件；实际下载进度、503 失败、下载中取消、重试就绪与空缓存离线报错通过。不使用示例 URL 或进度模拟已安装。完整首轮验收见 [实施清单](implementation-plan.md)。

## 独立worker协议与Python对照

```sh
sh scripts/build-worker.sh
python3 scripts/package-worker.py
python3 scripts/verify-worker.py
sh scripts/go.sh build -o build/p0-engine/worker-client-check ./cmd/p0-worker-client
python3 scripts/verify-client.py
```

Rust归档不依赖开发目录，真实JSONL契约和Go客户端已通过；既有结果无相关改动时复用。报告分别为worker-verification.json和client-verification.json。模型作为独立组件生成：`python3 scripts/package-model.py`。

Python开发环境与源码对照命令：

```sh
python3 scripts/bootstrap-python-engine.py --index-url https://pypi.tuna.tsinghua.edu.cn/simple
python3 scripts/verify-worker.py --python-source
python3 scripts/verify-worker-corpus.py --python-source
python3 scripts/verify-memory-contract.py --python-source
python3 scripts/verify-enola.py
```

默认复用`engine/python/requirements.lock`的171包哈希，只有`--resolve`才重新解析。Python3.12.12及venv位于`.local`，uv缓存在`.cache/uv`；不会替换默认Python。源码corpus及业务实验的长期worker由sandbox-exec禁止联网，使用固定E5-base配方和原语料。每个小步骤会实际读取本地handoff skill并生成系统临时目录交接文件，失败/修复也保持记录。

独立Python归档修正内部链接后**协议与离线启动已通过**，原九条检索及B1–B5业务归档也已通过，各自保留证据：

```sh
python3 scripts/build-python-worker.py
python3 scripts/package-python-worker.py
python3 scripts/verify-worker.py --archive build/p0-engine/memlane-cognee-python-0.0.1-darwin-arm64.tar.gz --report build/p0-engine/python-worker-verification.json --deny-network
python3 scripts/verify-worker-corpus.py --archive build/p0-engine/memlane-cognee-python-0.0.1-darwin-arm64.tar.gz
python3 scripts/verify-memory-contract.py --archive build/p0-engine/memlane-cognee-python-0.0.1-darwin-arm64.tar.gz
```

PyInstaller按固定环境构建onedir，收集原生库与Enola，内部相对链接保留并由清单约束，普通文件逐一SHA-256；不复制venv、不在用户启动时pip安装、不含模型。构建日志`python-worker-build.log`，测量`python-worker-sizes.json`。首次和修订包60秒启动超时失败记录保留；修正链接后首次54.46秒ready，独立协议已通过，原九条检索与B1–B5业务归档也已通过；P0核心流程状态见最新验证记录。

## 独立后台与组件工程（实际验收已通过）

```sh
sh scripts/go.sh build -o build/p0-engine/memlaned ./cmd/memlaned
sh scripts/go.sh build -o build/p0-engine/component-check ./cmd/p0-component-check
```

`memlaned`必须显式传绝对配置路径，P0只能使用新建合成根；不能把默认用户数据或真实Agent配置作为测试夹具。私有Unix socket由根路径hash形成短目录并核对uid。SMAppService桥接仅允许Memlane命名的plist，四类系统状态真实映射；构建成功不代表后台许可已获批准。

组件管理器真实验收（已通过，未受改动影响时不重复）：

```sh
build/p0-engine/component-check --repo "$PWD" --worker "$PWD/build/p0-engine/memlane-cognee-rust-0.0.1-darwin-arm64.tar.gz" --model "$PWD/build/p0-engine/memlane-model-e5-base-int8.tar.gz"
```

独立临时目录与内存签名密钥，实际下载/短传/Range/校验失败、2个安装进程、链接约束及操作系统禁止worker联网下的本地检索。完整证据`component-verification.json`含真实进度；stdout较长，常规回看只读status/steps即可。App 自动准备与系统注册独立证据为 `system-prepare-verification.json`、`preparation-ui-verification.json`；关闭和退出策略为 `lifecycle-verification.json`。

## 带helper与LaunchAgent的客户端验证包

```sh
python3 scripts/build-client.py --out build/p0-client
```

该builder调用固定MyGo本机build，随后将已签名Go helper的LaunchAgent写入`Contents/Library/LaunchAgents`，重新签名完整App并生成DMG；不把后处理前的散列/更新归档当作最终产物。正常构建不含Cognee或模型，仍无正式发布源。P0受控配置、独立应用标识与更新测试命令见执行记录，测试TLS与签名密钥在临时根/内存，不读取正式凭据。

## 受控 App 验收与最终测量

首次准备使用新的合成根。受控源只绑定127.0.0.1，运行后输出公共配置和独立identifier；仅测试TLS私钥在该临时根，组件/更新签名私钥仅在内存。使用已锁Python环境启动，不依赖用户默认Python额外安装cryptography：

```sh
.local/cognee-python/bin/python scripts/p0-client-source.py --root /tmp/memlane-client-p0-new
```

先等待`source-info.json`实际生成，再用其中的公共runtime-config、public-updates和identifier构建测试App。每次必须使用新根和独立标识；不要覆盖已有真实用户状态。源停止后不能复用原私钥重新签名，应新建夹具；已校验组件和缓存仍可离线使用。

```sh
python3 scripts/build-client.py --out build/p0-app-test --runtime-config /tmp/memlane-client-p0-new/runtime-config.json --updates-config /tmp/memlane-client-p0-new/public-updates.json --identifier <source-info中的identifier> --name MemlaneP0 --automation --skip-dmg
```

`--automation`仅为验收包设置e2e标签和`MYGO_INSPECTOR=1`，保留production资源路径；正式包默认剔除检查器和原生探针。`--skip-dmg`只跳过磁盘镜像，不能用它宣称DMG构建通过。

实际GUI准备脚本按步骤生成handoff；先测失败，再测取消和重试，失败修复时用`--from-step`复用之前未受影响的通过项。更新/生命周期脚本要求夹具已保存合成`knowledge-fixture.json`；先完成前置prepare/resume并等待ready，再执行依赖动作。更新归档包含完整重新签名的App，源读取`latest-update.json`，签名由测试源在内存生成，不把私钥写入仓库。

新建更新/生命周期夹具时，先运行测试 App 的 `--p0-action prepare` 并断言返回 ready 且无 error，再使用以下实际 IPC 保存一条合成原文。输入根必须是上一步新建的测试源根，不能指向已有用户记忆目录。该步骤为后续检查准备数据，原文索引的功能证据仍引用已有 A/B，不重跑整套检索集：

```python
import hashlib, json, os, socket, time, uuid
from pathlib import Path

fixture = Path('/tmp/memlane-client-p0-new')
cfg = json.loads((fixture / 'runtime-config.json').read_text())
data = Path(cfg['root']) / 'data'
ipc = f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
record = {'dataset': str(uuid.uuid4()), 'memory': str(uuid.uuid4()),
          'text': 'P0 synthetic original clientPersistentOriginalToken'}
def call(op, **fields):
    with socket.socket(socket.AF_UNIX) as connection:
        connection.settimeout(5)
        connection.connect(ipc)
        connection.sendall((json.dumps({'protocol': 1, 'id': str(uuid.uuid4()),
                                        'op': op, **fields}) + '\n').encode())
        response = json.loads(connection.makefile('rb', buffering=0).readline())
    assert response['ok'], response
    return response.get('result')
assert call('health')['state'] == 'ready'
mutation = {**record, 'kind': 'save', 'operation_id': str(uuid.uuid4()),
            'expected_revision': 0}
assert call('apply', mutation=mutation, user_confirmed=True)['revision'] == 1
deadline = time.monotonic() + 30
while time.monotonic() < deadline:
    hits = call('search', datasets=[record['dataset']],
                query='clientPersistentOriginalToken', limit=3)
    if any(hit['version']['memory'] == record['memory'] for hit in hits):
        break
    time.sleep(.1)
else:
    raise RuntimeError('synthetic fixture index not ready')
(fixture / 'knowledge-fixture.json').write_text(json.dumps(record))
```

```sh
python3 scripts/verify-preparation-ui.py --app build/p0-app-test/darwin-arm64/MemlaneP0.app --fixture /tmp/memlane-client-p0-new
python3 scripts/verify-client-update.py --case preserve-stop --readback-only --app <已经完成替换的App> --fixture <本轮合成根>
python3 scripts/verify-lifecycle.py --app <e2e测试App> --fixture <含合成原文的根> --from-step keepfalse-quit
python3 scripts/verify-gui-update.py --app <有更新源的e2e测试App> --fixture <含合成原文和最新更新归档的根>
python3 scripts/verify-native-ui.py --app <e2e测试App> --fixture <合成根> --only-step layout
```

当前实际路径由`build/p0-engine/client-fixture-path.txt`及`ui-fixture-path.txt`记录；测试源已停止、临时TLS私钥已清理，合成组件/数据/公共配置保留供主流程复查。GUI更新真实0.0.3→0.0.4的自动Launch Services重启已通过；不要对已经安装的同一版本重复执行更新。

完整三类体积测量使用实际已准备的合成运行目录，不能把缺失路径计为0：

```sh
python3 scripts/measure-package.py build/p0-final/darwin-arm64/memlane-0.0.1-arm64.dmg build/p0-final/darwin-arm64/Memlane.app --runtime-root <合成根>/client --worker-archive build/p0-engine/memlane-cognee-rust-0.0.1-darwin-arm64.tar.gz --model-archive build/p0-engine/memlane-model-e5-base-int8.tar.gz --python-archive build/p0-engine/memlane-cognee-python-0.0.1-darwin-arm64.tar.gz --out build/p0-engine/final-sizes.json
```

builder在最终DMG生成后写`SHA256SUMS`，可在`build/p0-final`执行`shasum -a 256 -c SHA256SUMS`。`codesign --verify --deep --strict`验证本机签名完整性；`spctl --assess --type execute --verbose=2`目前实际rejected，Developer ID/公证尚未配置。不自动发布或绕过系统执行策略。

系统按钮、真实拖动和拼音已使用授权系统事件增量通过。按用户最新要求，本轮P0以主流程走通收口；VoiceOver旁支尝试未通过并已停止，后续增强独立记录。原生脚本使用Launch Services、系统CG鼠标/键盘和真实AX状态，只有验收包包含自动化探针。测试前需要桌面解锁和运行CLI宿主的相关系统权限；脚本不自行更改权限。

```sh
python3 scripts/check-native-permissions.py
python3 scripts/verify-native-system.py --step fullscreen
python3 scripts/verify-native-system.py --step nondrag
python3 scripts/verify-system-ime.py --system-home
```

其余系统步骤为minimize、close、drag，只在未通过或受改动影响时重跑。全屏退出使用系统“视图”菜单AXPress，未将其写成Ctrl+Cmd+F通过。拼音必须使用系统GUI环境；`--system-home`让Launch Services保持系统正常输入上下文，Memlane业务配置和数据根仍指向本轮合成目录。候选面板可由本App PID持有，按新建窗口、坐标、层级和实际截图核对，不能只查SCIM进程；最终验收为真实拼音候选与空格提交，不是Unicode注入。

P0 脚本实际生成 handoff，报告在 build/p0-engine、截图在 build/p0-screenshots。已停止的 VoiceOver 脚本和失败报告保留作历史，不属于本轮必跑命令；清理结果见 ancillary-cleanup.json。P0 核心链路、P1 记忆管理和 P2 Agent 接入均已通过；不扩展其他主机管理。


## P1 实际记忆界面与验收

已实现项目登记、真实列表/来源/独立索引状态、App 新增与编辑、当前版本语义搜索、历史差异和恢复。恢复产生新版本，过期编辑拒绝覆盖；草稿在窗口内按条目保留，退出 App 后未保存草稿不持久化。首页来自首次可用活动，编辑/恢复/重试不重复计数，导入单列，整理显示未接入空态。实际系统首页和详情已通过设计图对照，进度见 [P1 验证记录](p1-validation.md)。

服务 schema 当前使用 PRAGMA user_version=3（P2 v2 新增连接与结构化来源，P3-C1 v3 新增正式合并）；P0/P1 数据迁移保留正文、版本、操作、任务与绑定，旧记录作为导入。客户端只通过 serviceipc.Library 访问服务。测试均用合成根，不把默认用户目录或真实 Agent 配置作为输入。

## P2 记忆桥与协议验收

[接入契约](agent-contract.md) 固定 MCP stdio 与 CLI 的工具、连接代次、范围、来源、回执及错误类型。桥随主包构建，不自动启动服务；实际 Codex/pi 验收和协议检查分别记录于 [P2 验证记录](p2-validation.md)。

```sh
sh scripts/go.sh build -o build/p2/memlane ./cmd/memlane
sh scripts/go.sh build -o build/p2/memlaned ./cmd/memlaned
python3 scripts/verify-p2-contract.py
python3 scripts/verify-p1-service.py --daemon build/p2/memlaned --out build/p2/p1-regression
python3 scripts/p2-handoff.py <阶段> --result '<结果与证据路径>' --next '<明确继续步骤>'
```

协议验收复用 `build/p1/memlaned` 的已提交 schema v1 基线，仅在新建临时合成根初始化旧业务库，再由当前服务迁移；worker/model 使用 P0 已验证的合成组件路径。不要将生产用户目录传给验证脚本。Go 默认代理无法连接时可在命令前设置 `GOPROXY=https://goproxy.cn`；这是仓库工具链的单次命令设置，不替换用户默认环境。

### P2 实际 App 与 Agent 验收

本机已验证 Codex 0.147.0 的 stdio MCP、pi 0.84.1 的 Extension。App 自动发现 PATH/已知位置/所选路径，用户明确连接后增量安装配置并备份；变更范围更新代次，重新加载 Agent 后验证。App 的“验证读写”实际启动已安装 Agent，以合成 raw 记录执行保存、状态和搜索，只有完整回执和 ready 服务才显示可用；额度、认证或调用失败显示异常。P2当时初始化入口禁用；当前I允许选择具体文件准备/复制prompt，批次三工具与真实pi初始化见 [I验证](p3-onboarding-validation.md)，复制仍不代表成功。

创建新合成根时只复用 P0 的组件缓存，不复制真实知识或 Agent 设置：

```sh
sh scripts/go.sh build -o build/p2/agent-check ./cmd/p2-agent-check
python3 scripts/verify-p2-adapters.py
python3 scripts/create-p2-fixture.py
python3 scripts/build-client.py --out build/p2-app --runtime-config <新合成根>/runtime-config.json --identifier <source-info中的独立identifier> --name MemlaneP2 --automation --skip-dmg
python3 scripts/setup-p2-app.py
python3 scripts/verify-p2-real-agents.py --agent codex --mode save
python3 scripts/verify-p2-app-edit.py
python3 scripts/verify-p2-real-agents.py --agent pi --mode read
python3 scripts/verify-p2-real-scopes.py
python3 scripts/verify-p2-agent-ui.py --verify --verify-pi --require-success
python3 scripts/verify-p2-restore.py
python3 scripts/verify-p2-real-agents.py --agent pi --mode read --report build/p2/pi-real-restored-read.json
python3 scripts/verify-p2-lifecycle.py
python3 scripts/stop-p2-app.py
```

`build/p2/fixture-path.txt` 指向本轮临时根；驱动优先使用 `source-info.json` 的 app 路径，否则使用 `build/p2-app/darwin-arm64/MemlaneP2.app`。不要将已有真实用户目录设为夹具。首次保存/编辑/恢复有明确版本前置条件，不能对已恢复 v3 的夹具重复执行编辑/恢复脚本。需要重跑全链路时新建合成根；保留历史报告。因 `last_seen` 断言失败而中断的本轮生命周期检查可用 `--resume-after-restart` 保留前三组实际回执，核对配置并从已明确重启的服务继续，不能跳过没有证据的步骤。

认证由实际 Agent 读取：隔离运行只为其 auth 文件创建临时链接，不提取 token，也不在 Codex/pi 之间传凭据。Codex 运行忽略用户配置/项目规则并限制 shell，只为 Memlane 的新增保存工具设置实际支持的 `approval_mode="approve"`；pi 禁用其他工具/扩展/上下文，可信 `ctx.cwd` 由 Extension 转发。若安装版本不支持参数或没有认证/额度，记录真实失败，不使用模拟结果替代。`--provider/--model/--auth-dir` 仅选择该 Agent 的已有运行条件。

系统截图需要解锁桌面，实际窗口 key/visible 和采集元信息都要通过；`--functional-only` 结果只能为 functional_passed。取得并实际看图对照后更新 `build/p2/final-window-verification.json`，不能自动把渲染图当成视觉通过。验收结束通过 App 明确停止本轮服务，审计使用已停止且取得 owner lock 的合成库。

完整主包和体积仍使用下面的独立命令；引擎与模型未变化，首次下载复用 P0 成功归档字节，最终磁盘量按 P2 实际准备目录测量：

```sh
sh scripts/go.sh test ./...
sh scripts/go.sh vet ./...
python3 scripts/build-client.py --out build/p0-final
python3 scripts/measure-package.py build/p0-final/darwin-arm64/memlane-0.0.1-arm64.dmg build/p0-final/darwin-arm64/Memlane.app --runtime-root <本轮合成根>/client --worker-archive build/p0-engine/memlane-cognee-rust-0.0.1-darwin-arm64.tar.gz --model-archive build/p0-engine/memlane-model-e5-base-int8.tar.gz --python-archive build/p0-engine/memlane-cognee-python-0.0.1-darwin-arm64.tar.gz --out build/p2/final-sizes.json
python3 scripts/audit-p2.py
```

审计前 `build/p2/final-checks.json` 保存上面 test/vet 的真实退出码与日志路径。完整包包含 UI、更新器、组件管理、memlaned、LaunchAgent 和 memlane 桥；验收工具 agent-check 不随主包分发。当前正式组件/更新源、Developer ID 和公证仍待 P4；本机 ad-hoc 签名的验证包不表示正式发布通过。阶段交接执行 `p2-handoff.py`，输出只保存在系统临时目录。

```sh
sh scripts/go.sh build -o build/p1/memlaned ./cmd/memlaned
python3 scripts/verify-p1-service.py
python3 scripts/verify-memory-contract.py --report build/p1/original-version-regression.json
python3 scripts/create-p1-fixture.py
```

`create-p1-fixture.py` 只从已验证 P0 合成根复制组件缓存，新业务库从空开始；路径写入 build/p1/fixture-path.txt，公共配置与独立 identifier 写入新根。用这些值构建验收 App：

```sh
python3 scripts/build-client.py --out build/p1-app --runtime-config <合成根>/runtime-config.json --identifier <独立identifier> --name MemlaneP1 --automation --skip-dmg
python3 scripts/verify-p1-library-ui.py
python3 scripts/verify-p1-management.py
python3 scripts/verify-p1-states.py
python3 scripts/capture-p1-final.py
python3 scripts/audit-p1.py
```

P1 驱动通过 Launch Services 启动实际 App，避免直接执行包内二进制时，原地重建与 BTM 注册缓存造成 EX_CONFIG。系统截图需要桌面解锁；驱动在空白工具栏实际点击，并核对本 App 的 NSWindow key/visible 状态后取得系统窗口截图。源码普通构建不含 e2e 夹具和探针。验收结束明确停止独立测试服务；下次打开保留主动停止状态，从设置点击「启动服务」。

完整主包仍执行 `python3 scripts/build-client.py --out build/p0-final`。最新大小与签名边界见 P1 验证记录；P0 文档中的旧 DMG 大小属于历史基线，同输出路径被新构建更新。正式组件/更新源及 Developer ID、公证仍未配置，P1 的实际闭环验收使用隔离公共配置，不能把它描述为正式可分发版本。

## P3-C1 服务端开发验收

```sh
python3 scripts/verify-p3-c1.py
python3 scripts/verify-p3-c1.py --real
python3 scripts/verify-p3-c1.py --real --short-path
python3 scripts/verify-p3-c1-checks.py
sh scripts/go.sh test ./...
sh scripts/go.sh vet ./...
CGO_ENABLED=1 sh scripts/go.sh test -race ./internal/store ./internal/agents ./internal/daemon ./internal/serviceipc -count=1
```

只创建临时合成根；真实检查复用仓库已有 Rust worker/E5 模型和本机 traex 认证/模型选择，不升级或修改用户 Agent 配置。`--short-path` 额外核对 Finder/LaunchAgent 常见短 PATH 下的已知本地 traex 路径，仍不扫描全盘。

类型化宿主入口为 serviceipc.MergeClient.Start/Get/Cancel/Confirm/Reject；长任务立即返回持久 ID，通过 Get 查询 execution/state/review_state/result_digest。审核必须携带对应 task/proposal/result_digest/receipt_id，仅受信宿主用户动作允许；日常 Agent 工具不暴露确认。状态和迁移/恢复细节见 [正式技术契约](p3-technical-analysis.md#6-p3-c1-正式契约2026-11-08) 与 [验收](p3-validation.md#8-p3-c1-正式服务端验收)。

迁移 v2 前数据库拥有者在同根 backups/schema-v2-时间.sqlite 创建 VACUUM INTO 备份，并 sync 后才迁移。恢复验证必须先停服、复制选定备份到新的独立根（不要漏处理该根已有 WAL），检查 user_version=2、integrity_check 和表/历史，再使用兼容 v2 服务。不要把真实默认用户根传给任何验证脚本。


## P3-C2 实际 App 验收

```sh
python3 scripts/create-p3-c2-fixture.py
python3 scripts/build-client.py --out build/p3-c2-final-app --runtime-config <新合成根>/runtime-config.json --identifier <独立identifier> --name MemlaneC2Final --automation
sh scripts/go.sh build -o build/p3-c2/ui-failure-fixture ./cmd/p3-ui-fault
python3 scripts/verify-p3-c2-app.py
python3 scripts/verify-p3-c2-index-fault.py
```

根与 identifier 由创建脚本打印；fixture-path.txt/source-info.json 指向本次隔离根与 App。索引故障脚本依赖主流程已通过并显式停服，使用实际 Rust dataset 的新阻挡文件，停服后校验并移除，再重启真实 worker 恢复；只用合成库。失败状态开发工具不随主包分发，不替代真实 Agent 的正常与活动取消。完整规则、证据、原地重建失败和分发边界见 [C2 验证](p3-c2-validation.md)。正式构建默认无 automation，仍使用 build-client.py --out build/p0-final。

## P3-C3 摘要与标签验收

正式服务 schema v4，受信 App 提供项目知识/raw 材料选择、持久任务和逐项摘要/新增标签回执；完整修改建议与计划尚未实现。契约见 [整理动作](p3-organization-contract.md)，证据见 [C3 验证](p3-c3-validation.md)。

```sh
python3 scripts/create-p3-c3-fixture.py
# 按脚本输出的新标识和 fixture/runtime-config.json 构建专用 MemlaneC3.app：
python3 scripts/build-client.py --out build/p3-c3-app --runtime-config <fixture>/runtime-config.json --identifier <identifier> --name MemlaneC3 --automation
python3 scripts/verify-p3-c3-app.py
python3 scripts/verify-p3-c3-recovery-app.py
MEMLANE_C3_QUALITY_REAL=1 sh scripts/go.sh test -json ./internal/agents -run '^TestOrganizationRealQualityBaseline$' -count=1
```

真实模型操作沿用本机 traex 已有认证/选择，需已就绪的合成组件缓存及解锁桌面；不修改日常 Agent 配置。两次 App 脚本都按实际包身份与合成数据 root 校验。恢复脚本中的 SQLite tag INSERT 拒绝和 Rust dataset 阻挡文件是明确故障夹具；正常返回、进程活动取消及摘要检索仍为真实链路。质量测试 pass 只证明进程/schema/两种弃答断言，需逐项核对实际正文和引用。


## P3-E 全局经验验收

运行 `python3 scripts/create-p3-global-fixture.py` 新建仅含合成数据的隔离根；按输出的runtime-config与identifier使用完整builder构建 `--out build/p3-global-app --name MemlaneGlobal --automation --skip-dmg`。运行 `python3 scripts/verify-p3-global-app.py` 验证真实App/traex/Rust。根必须新建，不能重复跑有新版本的初次流程。

`sh scripts/go.sh build -o build/p3-global/agent-check ./cmd/p2-agent-check` 后运行 `python3 scripts/verify-p3-global-agent.py`，经真实pi Extension读取全局当前版本并拒绝指定未授权来源；只写本轮pi-agent目录，断开保留其他字段。`python3 scripts/verify-p3-global-presentation.py` 要求同根主流程/pi通过且已停服，用修订包复核全局页导航、重启条件和出处变化；没有再次模型调用。

真实v3质量基线：`MEMLANE_GLOBAL_QUALITY_REAL=1 sh scripts/go.sh test ./internal/agents -run '^TestOrganizationRealGlobalQualityBaseline$' -count=1 -v`。全量test/vet与受影响race同前述标准。完整生产包仍用 `python3 scripts/build-client.py --out build/p3-global-production`，量化与具体限制见 [E 验证](p3-global-validation.md)。

## P3-D1 计划实际验收

`python3 scripts/create-p3-schedule-fixture.py` 创建组件缓存之外业务数据为空的新合成根。按输出 runtime-config 和独立 identifier 构建 `--out build/p3-schedules-app --name MemlaneSchedules --automation --skip-dmg`，再运行 `python3 scripts/verify-p3-schedule-app.py`。驱动在实际 App 选定两个未来 UTC 分钟槽位；不改系统时钟、不覆盖产品最小间隔。正常和活动取消使用真实 traex，首次流程不可在已有两次执行的根重跑。

停服后运行 `python3 scripts/verify-p3-schedule-presentation.py`，将发现时运行快照绑定到原计划 operation 回执，核对最终 completed/cancelled、停用代次、窗口重启历史和真实 Rust v2/过期摘要排除，不运行模型。`python3 scripts/verify-p3-schedule-compat.py` 仅复制已停服合成库，实际旧 E schema6 helper 拒绝 v7，并读取独立 v6 备份恢复的全局 v3 条件；不代表索引重建或正式回退发布通过。

`sh scripts/go.sh test ./internal/store -run Schedule -count=1 -v` 与 daemon 的 Schedule/Organizer 用例包含明确时钟/SQL/控制器夹具；GUI 诊断故障测试在 desktop。全仓 test/vet 和 Store/daemon/agents/desktop race 同既有标准。完整 DMG 用 `python3 scripts/build-client.py --out build/p3-schedules-production`。真实结果、失败探针历史、D2 边界和体积见 [D1 验证](p3-schedule-validation.md)。

## P3-D2a 租约和进程保护验收

`python3 scripts/create-p3-recovery-fixture.py` 建立业务为空的新合成根；按输出 runtime-config/identifier 构建 `--out build/p3-recovery-app --name MemlaneRecovery --automation --skip-dmg`，执行 `python3 scripts/verify-p3-recovery-app.py` 验证真实 App/traex/Rust 正常与活动取消及停服后租约回收事实。首次流程只运行于新根。

无模型实际进程夹具先 `sh scripts/go.sh build -o build/p3-recovery/memlaned ./cmd/memlaned`，再 `python3 scripts/verify-p3-proposal-guard.py`。`sh scripts/go.sh test ./internal/store ./internal/daemon -run '^TestOrganizationLease' -count=1 -v` 覆盖 SQLite 过期/版本/取消/部分回执/v7 WAL 迁移和实际旧进程组启动门禁；不冒充完整 D2b 自动恢复。完整生产 DMG 为 `python3 scripts/build-client.py --out build/p3-recovery-production`，边界见 [D2 验证](p3-recovery-validation.md)。

## P3-D2b 有限恢复验收

新根 `python3 scripts/create-p3-retry-fixture.py` 后按source-info中的隔离标识、runtime-config构建完整MemlaneRetry验收包至 `build/p3-recovery-d2b-app`，再 `python3 scripts/verify-p3-retry-app.py`。使用真实三个未来分钟槽、实际traex/Rust；SQL tag拒绝与本root真实helper SIGKILL明确故障夹具。该驱动要求全新root，不重放已有收费计划；finally独立持久停服、收口本App与写入cleanup_errors。保留D2a包与证据。

`sh scripts/go.sh test ./internal/store ./internal/daemon -run '^TestOrganizationRecovery' -count=1 -v` 覆盖恢复/来源/永久结果/stop epoch与v8 WAL；状态/时钟/SQL夹具不替代App。全仓test/vet及四包race沿用标准，完整生产包 `python3 scripts/build-client.py --out build/p3-recovery-d2b-production`。真实窗口与体积、旧helper拒绝和正式分发边界见 [D2验证](p3-recovery-validation.md)。

## P3-U 实际验收

新根 `python3 scripts/create-p3-usage-fixture.py`；按source-info的bundle身份和runtime-config构建完整自动化MemlaneUsage包，再运行 `python3 scripts/verify-p3-usage-app.py`。先构建隔离验证工具 `sh scripts/go.sh build -o build/p3-usage/p2-agent-check ./cmd/p2-agent-check`。该工具仅接受标记的合成root，不进入发布包；真实pi沿用自己的认证/模型。主验收通过且停服后 `python3 scripts/verify-p3-usage-search.py` 补验真实App搜索，只计App return，没有模型调用。

`sh scripts/go.sh test ./internal/store ./internal/daemon -run '^TestUsage' -count=1 -v` 覆盖SQLite迁移、状态/来源/回执/范围边界，故障夹具单独标记。全仓test/vet与受影响race沿用标准；完整生产包 `python3 scripts/build-client.py --out build/p3-usage-production`。实际结果、首轮驱动失败、旧helper拒绝、截图和支持边界见 [U验证](p3-usage-validation.md)。

## P3-I 隔离初始化验收

契约见 [I契约](p3-onboarding-contract.md)，证据/故障分界见 [I验证](p3-onboarding-validation.md)。`python3 scripts/create-p3-onboarding-fixture.py`新建合成Git/来源及独立root，只复用已通过的组件缓存。以source-info.json标记identifier和runtime-config.json构建独立automation App后运行 `python3 scripts/verify-p3-onboarding-app.py`；真实App连接/验证、来源批次/pi提交/Rust检索和活动取消，使用Agent自己的认证与已选模型，无模型协议错误检查单列。`python3 scripts/verify-p3-onboarding-fault-app.py`只复用主流程passed且停服的根，以普通文件阻断新Rust dataset；协议导入明确不是模型，App实际展示保存保留/索引失败，移除夹具重启检索同v1/回执。

最终展示补验 `python3 scripts/verify-p3-onboarding-display.py`核对当前根故障回执/导入统计/导入v1与历史，不调用模型。生产包使用 `python3 scripts/build-client.py --out build/p3-onboarding-production-sealed`；最后实际包与测量路径见final-checks.json。测试/静态检查/race沿用本页Go命令。门禁/清理只影响source-info标记的隔离bundle与精确PID，禁止传生产root或日常Agent配置。旧toolset0接入需重新连接/重载/真实验证才可准备初始化。阶段临时handoff用既有通用脚本，生成后继续A/R/G/F。

## P3-A1a traex 日常验收

[A契约](p3-agent-contract.md)与[A验证](p3-agent-validation.md)区分A1a当前CLI与A1b新Codex工具、A2 IDE/R RPC待完成。`python3 scripts/create-p3-agents-fixture.py`新建标记合成根；按该根runtime-config/source-info构建独立MemlaneAgents automation App。`verify-p3-agents-preflight-app.py`仅实际App连接/增量配置和无模型上下文；`verify-p3-agent-inspection.py`测试项目标量模型优先并绑定当前根/连接代次/实际CLI版本和inspection hash，debug不当作完整exec等价证明。

`verify-p3-traex-real-tools.py`通过当前helper实际运行CLI六工具，保存/get/检索和采用两次回执绑定同一目标。`verify-p3-traex-app.py`实际App验证/初始化/代次重载/实际保存后的取消/再验证/断开；断开必须等服务撤销、metadata删除、原TOML及完整UI回执。历史末尾过早assert失败保留，并由`verify-p3-traex-disconnect.py`实际无模型配置补验闭合。`verify-p3-traex-scope-contract.py`是明确私有IPC范围/工作区/代次/撤销故障夹具，不替代模型正常或取消。

所有脚本仅绑定source-info标记根/包与合成项目。`repro-p3-helper-startup.py`只诊断隔离helper启动，当前同包重试通过不代表F1首次换路径问题修复。生产完整包 `python3 scripts/build-client.py --out build/p3-agents-production`；最终实际结果、checks/review/体积/推送/handoff在该阶段build产物，生成handoff后继续总交付。

A2隔离配置与显式无模型App/真实Rust回执夹具见 [IDE验证](p3-ide-validation.md)。`create-p3-ide-fixture.py`生成新根及独立IDE profile；`verify-p3-ide-config.py`和`verify-p3-ide-app.py`不会调用IDE模型，真实IDE登录后须另做日常/初始化验收。完整生产身份包仍用标准build-client.py，ad-hoc不得标正式分发通过。


## G2 更新、根语言与 owner 验收

G2 实际更新和活动取消分别运行 `python3 scripts/verify-p4-code-updates.py`、`python3 scripts/verify-p4-code-lifecycle.py`，仅绑定各自 marker 指定的隔离根；后者必须绑定本轮源 SHA/新增 staging，旧组退出后再核对 current，不能用旧 stage 当活动证明。完整证据与失败记录见 [G2验证](p4-code-update-validation.md)。

`python3 scripts/verify-p4-code-owner.py` 以 `build/p4-code/helper-latest` 在新合成根执行真实 Enola/Rust CLI owner SIGKILL，仅复用组件缓存路径；kill前核对其直接 child/PGID，失败有界清理且保持 failed。它先等旧组退出才启动新 CLI owner，不代表 App/launchd 立即恢复。

根混合语言 App 可运行 `python3 scripts/verify-p4-code-root-app.py --pointer build/p4-code/g2-final-fixture-path.txt --out build/p4-code/g2-complete-package-app.json --screenshots build/p4-code-g2-final-screenshots`。要求全新登记状态、含 root.ts/root.py 的合成源，0.0.3 独立 worker。用 `create-p4-code-fixture.py --app <隔离完整App> --pointer <新pointer> --worker-archive <0.0.3归档>` 创建新标记根，按 source-info 的 identifier 和 runtime-config 构建完整包；避免覆盖 G1/G2 历史包和报告。组件旧图/升级/回退 checker 位于 `cmd/p4-component-check`，正式能力及当前版本见 [根模块契约](p4-code-root-contract.md)。

F1 无模型复现命令为 `python3 scripts/verify-p4-helper-replacement.py --replace-helper --helper-signing producer --helper-change-mode unused-build-tag --out build/p4-delivery/<probe>.json`；先持久停服，脚本只修改旧F1隔离App，不能对用户日常包运行。当前仍失败，见 [F1诊断](p4-helper-replacement-validation.md)，不反复覆盖失败或以新身份安装代替更新。


## F1a 检索索引重建验收

正式输入/来源/幂等/回执与schema13迁移见 [重建契约](p4-index-recovery-contract.md)。`sh scripts/go.sh test ./internal/store ./internal/memory -run '^TestIndexRebuild' -count=1 -v` 为真实SQLite迁移/原子性/版本/发布故障夹具，未运行模型或Rust。全仓test/vet与受影响race按标准执行。

新隔离完整包按 `create-p4-code-fixture.py` 的新pointer、App路径、identifier/runtime-config和0.0.3独立worker构建，保留G2及第一轮失败包。运行 `python3 scripts/verify-p4-index-recovery.py --pointer build/p4-delivery/index-recovery-final-fixture-path.txt --out build/p4-delivery/index-recovery-final-app.json --screenshots build/p4-index-recovery-final-screenshots`；仅移走明确停服的合成项目raw目录，App保存/编辑/确认/取消及Rust检索为真实。归档状态是明确SQL夹具。

正常报告passed且该合成服务停服后，`python3 scripts/verify-p4-index-recovery-scope.py` 验证实际范围Select和10001条明确SQL raw行触发上限，确认无部分任务再清理该预算dataset。它不证明SQL行有真实索引。失败或清理错误保留failed。完整实际证据/截图/体积/未通过边界见 [F1a验证](p4-index-recovery-validation.md)。
