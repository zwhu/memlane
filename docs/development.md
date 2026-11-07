# 本地开发与 P0 验证

当前已有真实 MyGo Native UI、独立 Go 记忆服务、Rust/Python 原文 worker 和外置组件管理。Rust 为本轮 App 原文索引验证路线，Python 归档按同一契约完成对照；App 自动准备、后台系统注册、生命周期与受控 HTTPS 更新已实测。记忆管理 UI 与真实 Agent 接入属于后续阶段；草稿只在窗口内存中保留，退出后不保存。测量结果与验收边界见 [P0 验证记录](p0-validation.md)。

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

每个脚本实际生成handoff，报告在build/p0-engine、截图在build/p0-screenshots。已停止的VoiceOver脚本和失败报告保留作历史，不属于本轮必跑命令；清理结果见ancillary-cleanup.json。P0核心链路已通过，App内真实记忆管理UI及Agent接入仍按P1/P2实施；不扩展其他主机管理。
