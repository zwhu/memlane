#!/usr/bin/env python3
"""从已核对的源码边界生成离线动态流程图。示例状态不来自用户数据。"""
import json
import xml.etree.ElementTree as ET
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
PLUGIN = REPO / 'build/agent-integrations-validation/upstream/integrations/codex/plugins/cognee'
COGNEE = REPO / '.local/cognee-python/lib/python3.12/site-packages/cognee'

def source(path, line, label):
    try:
        display = path.relative_to(REPO)
    except ValueError:
        display = path
    return {'path': display.as_posix(), 'line': line, 'label': label}

steps = [
 {'id':'task','title':'1 · Codex 正常工作','sub':['用户提示、工具调用、回答','新提示也会查询已有记忆'],
  'owner':'Codex / 官方插件','detail':'SessionStart 准备连接。UserPromptSubmit 查询已有会话记忆并暂存本轮用户提示；PostToolUse 和 Stop 接收工具结果与回答。这里先形成原始材料，尚未完成整场会话的知识提炼。',
  'saved':'用户提示先在插件侧待配对；Codex 原生 rollout 独立保留。','pending':'工具轨迹与提示／回答还需提交 Cognee。',
  'sources':[source(PLUGIN/'hooks.json',1,'官方 hook 映射'),source(PLUGIN/'scripts/store-user-prompt.py',134,'暂存提示')]},
 {'id':'entry','title':'2 · 写入 QA 与 Trace','sub':['Stop 配对问题和回答','工具结果按 trace 单独写入'],
  'owner':'官方 Codex 插件','detail':'回答结束时，将此前暂存的提示和最终回答配成一个 QAEntry；工具调用通过 TraceEntry 写入。请求是 POST /api/v1/remember/entry，携带 session_id 与 dataset 身份。不是每次调用都发送整份会话。',
  'saved':'每轮 QA 与逐步工具轨迹提交给会话层。','pending':'写入成功不代表知识文档、图谱处理或 Memlane 接收已完成。',
  'sources':[source(PLUGIN/'scripts/store-to-session.py',323,'配对 QA'),source(PLUGIN/'scripts/store-to-session.py',146,'采集工具轨迹'),source(PLUGIN/'scripts/_plugin_common.py',4695,'typed entry 请求')]},
 {'id':'cache','title':'3 · Cognee 会话层保存','sub':['SessionManager 收下原始增量','session_stored 是会话写入回执'],
  'owner':'Cognee / session 层','detail':'QA 分支调用 add_qa；Trace 分支调用 add_agent_trace_step，返回 session_stored。会话数据由 SessionManager 与配置的缓存后端保存。本机受管服务使用 SQLite cache.db。缓存是持久配置的会话层，不能按名称理解为一定只在内存。',
  'saved':'示例：1 条 QA + 2 条 Trace，已进入会话层。','pending':'尚未产生本例的 session_learning。trace 的反馈或轻量失败上下文也不等于最终提炼知识。',
  'sources':[source(COGNEE/'api/v1/remember/remember.py',630,'session_stored 分支'),source(COGNEE/'infrastructure/session/session_manager.py',207,'trace 写入'),source(REPO/'engine/python/cognee_service.py',25,'cache.db 配置')]},
 {'id':'trigger','title':'4 · 插件触发 improve','sub':['阈值自动触发 / 会话结束','也可通过官方手动同步触发'],
  'owner':'官方插件 → Cognee','detail':'工具调用与 Stop 保存后更新计数，达到自动阈值才尝试 improve，并受冷却／退避约束。固定插件默认阈值为 150 个计数事件，不是 150 轮聊天。SessionEnd 或手动同步也提交 improve；会话结束路径会补送本地待发材料。',
  'saved':'请求以 session_ids 指定已保存的会话，run_in_background=true。','pending':'提交成功是已受理，后台可能尚未持久化原始图谱或提炼知识。',
  'sources':[source(PLUGIN/'scripts/store-to-session.py',289,'自动触发'),source(PLUGIN/'scripts/_plugin_common.py',121,'默认阈值'),source(PLUGIN/'scripts/_plugin_common.py',5516,'后台 improve 请求'),source(REPO/'internal/official/agent.go',410,'Memlane 调用官方结束同步')]},
 {'id':'rawgraph','title':'5 · 原始材料进入图谱','sub':['persist_session_qa','persist_agent_traces'],
  'owner':'Cognee / improve','detail':'improve 先按会话水位读取新增 QA 与 Trace，持久化并构建对应图谱。原始内容也可能产生实体与关系，这属于原始材料的图谱化。此时原始水位可以已推进，但可读的提炼知识文档仍不存在。',
  'saved':'本例 3 条原始材料进入官方持久层，官方原始水位已推进。Memlane 此时即可按授权读取并接收 raw，不必等知识完成。','pending':'上下文提取、经验筛选和 session_learning 的构建还未完成。',
  'sources':[source(COGNEE/'modules/improve/stages.py',157,'QA 持久化阶段'),source(COGNEE/'modules/improve/stages.py',195,'Trace 持久化阶段'),source(COGNEE/'tasks/memify/cognify_session.py',22,'add / cognify 后推进水位')]},
 {'id':'context','title':'6 · 提取会话上下文','sub':['extract_agent_context','获取本轮新增的可提炼线索'],
  'owner':'Cognee / LLM 阶段','detail':'Cognee 调用记忆处理配置的 LLM，读取未处理的会话／工具上下文，形成指导与候选。采集会话的 Codex 与提炼模型是两个角色。可用 LLM、AUTO_FEEDBACK 和会话层可用性会影响该阶段是否运行。失败 trace 也可能在采集时生成不用 LLM 的轻量失败候选；它仍是 session context，尚未成为最终学习文档。',
  'saved':'新增上下文线索保留在会话层。','pending':'候选需要归纳与接受／拒绝，尚未成为 session_learning。',
  'sources':[source(COGNEE/'modules/improve/stages.py',295,'上下文阶段门禁'),source(COGNEE/'infrastructure/session/agent_context_extraction.py',125,'轻量失败上下文'),source(COGNEE/'infrastructure/session/agent_context_extraction.py',306,'批量待处理上下文')]},
 {'id':'distill','title':'7 · 提炼并筛选经验','sub':['LOAD → CURATE → ACCEPT','查询既有知识，再接受或拒绝'],
  'owner':'Cognee / distill_sessions','detail':'distill_session 加载未提炼上下文与关联 QA，按批次由 curator LLM 提出经验。每条候选查询此前经验／实体，再由 writer/rejecter LLM 决定写入或拒绝。因此每份 session 不保证产生知识；没有新经验、门禁跳过或模型错误需要分开判断。',
  'saved':'本例提出并接受 1 条“项目统一使用 pnpm”的经验。','pending':'经验还需生成独立文档并完成 add / cognify。',
  'sources':[source(COGNEE/'modules/session_distillation/distill.py',1,'提炼主流程'),source(COGNEE/'modules/session_distillation/distill.py',307,'curator'),source(COGNEE/'modules/session_distillation/distill.py',477,'接受／拒绝候选')]},
 {'id':'learning','title':'8 · 提炼知识构建完成','sub':['Session learning 独立文档','add → cognify → 图谱与向量'],
  'owner':'Cognee / 官方持久层','detail':'每条被接受的经验渲染为独立 Session learning 文档，再执行 add 与 cognify，保留来源关联。固定 Cognee improve 后续还会按门禁执行偏好、truth subspace、冲突评审、全局上下文索引等阶段；本图的“知识已处理”不等于整个 improve 的所有阶段已结束。',
  'saved':'本例：官方原始材料 3 条，已处理的学习文档 1 条。','pending':'Memlane 还没有把这份结果导入自己的业务库。',
  'sources':[source(COGNEE/'modules/session_distillation/distill.py',550,'一条经验对应一份文档'),source(COGNEE/'modules/session_distillation/distill.py',565,'add / cognify'),source(COGNEE/'api/v1/improve/improve.py',153,'全部 improve 阶段')]},
 {'id':'read','title':'9 · Memlane 读取结果','sub':['批准的 dataset / session','等待官方材料和知识处理回执'],
  'owner':'Memlane / 受信读取','detail':'这是 Memlane 的 SyncCognee：按已批准的服务实例、dataset、session 与连接代次读取并导入官方材料。第 9–11 步拆开说明内部职责，实际一次调用可以先导入 raw，再检查已处理的 knowledge；这段可与第 5–8 步后台处理并行。当前 App 有限轮询，要求原始持久化、无待处理文档且至少有一份知识回执，才提前结束等待。',
  'saved':'读到具体 QA、Trace、Session learning 及处理状态。','pending':'身份与全部来源校验通过后才能导入。知识为 0 时不能伪造成功，当前逻辑会继续有限观察；应结合实际阶段结果解释。',
  'sources':[source(REPO/'internal/daemon/cognee_sync.go',122,'受信读取官方材料'),source(REPO/'internal/desktop/cognee_wait.go',11,'有限轮询与知识回执判据')]},
 {'id':'store','title':'10 · 原始与知识分别入库','sub':['raw / knowledge 分开保存','冻结来源、版本，重放去重'],
  'owner':'Memlane / SQLite 业务库','detail':'官方 QA／Trace 以 raw 导入，Processed=true 的学习文档以 knowledge 导入，并绑定具体原文版本。App 托管的 Codex 完整 rollout 还会作为独立原文补入；它不是插件 QA／Trace DTO。导入不能任意覆盖已有有效知识，同一 external_id 重放不会再新建一份。',
  'saved':'本例 Memlane 保存 3 条 raw + 1 条 knowledge@v1，保留出处。','pending':'本地当前版本检索索引尚未完成。',
  'sources':[source(REPO/'internal/daemon/cognee_sync.go',212,'原始材料导入'),source(REPO/'internal/daemon/cognee_sync.go',240,'仅导入已处理学习文档'),source(REPO/'internal/store/cognee_import.go',21,'来源、版本与重放契约'),source(REPO/'internal/official/transcript.go',19,'完整原文单独读取')]},
 {'id':'index','title':'11 · 建立当前版本索引','sub':['Rust worker / E5','App 可编辑、追溯与管理知识'],
  'owner':'Memlane / 当前知识','detail':'Memlane 使用自己的索引队列和 Rust worker 为当前有效知识建立检索索引。保存成功和可检索分别表达。用户编辑生成新版本；官方历史图谱继续保留原内容，本轮代码不自动把 App 编辑回写到官方 Cognee。',
  'saved':'本例 knowledge@v1 已可检索；编辑演示可生成 v2。','pending':'新会话消费时，应区分历史图谱材料和当前版本知识。',
  'sources':[source(REPO/'internal/store/cognee_import.go',18,'复用索引与版本'),source(REPO/'internal/official/current_knowledge.go',17,'当前知识与官方历史的边界')]},
 {'id':'recall','title':'12 · 新会话消费记忆','sub':['Memlane memory_search / get','官方 Cognee recall 保留历史'],
  'owner':'Codex / 双读取路径','detail':'已接入 Memlane 当前知识读取的会话，先查 memory_search，必要时 memory_get，使用当前正文、版本和范围。官方插件的 session lookup／Cognee graph recall 是另一条历史读取路径。两套存储并存；不能把官方图谱的 v1 说成 App 编辑后的 v2。',
  'saved':'当前版本和来源被带入新的编码任务。','pending':'后续任务继续采集新增会话，再进入同一增量流程。',
  'sources':[source(REPO/'internal/official/current_knowledge.go',72,'新会话读取指令'),source(PLUGIN/'scripts/session-context-lookup.py',1,'官方会话召回')]},
]

locations = [
 '插件运行 HOME/.cognee-plugin/codex/pending/；Codex rollout 是独立原文。',
 'HTTP → Cognee remember/entry；未送达条目暂存于插件 bridge/。',
 '<Cognee 根>/system/databases/cache.db：QA、Trace、context 与水位。',
 '插件 improve-state/ 保存触发与退避状态；Cognee 后台执行 improve。',
 '<Cognee 根>/data/ 保存官方文件；system/databases/ 保存元数据、图谱与向量。',
 'Cognee 会话上下文仍在 session 层；本机缓存后端为 cache.db。',
 '候选在提炼阶段筛选；接受后才进入下一步的独立学习文档。',
 '<Cognee 根>/data/ + cognee_db + <dataset>.lbug + <dataset>.lance.db。',
 '受信 HTTP 读取 /api/v1/memlane/datasets/<dataset>/sessions/<session>。',
 '<Memlane 服务根>/memlane.sqlite：raw、knowledge、版本与来源。',
 '<Memlane 服务根>/index/：worker 索引；当前正文仍由 memlane.sqlite 提供。',
 'Memlane MCP → 当前正文；官方插件 → session／历史图谱材料。',
]
for step, location in zip(steps, locations):
    step['location'] = location
steps[10]['sources'].extend([source(REPO/'internal/desktop/runtime.go',179,'业务根与索引目录'),source(REPO/'engine/rust/backend.rs',100,'按 dataset 与 generation 建立 LanceDB')])

positions = [(40,120),(380,120),(720,120),(1060,120),
             (1060,440),(720,440),(380,440),(40,440),
             (40,760),(380,760),(720,760),(1060,760)]
nodes=[]
for step,(x,y) in zip(steps,positions):
    nodes.append({'id':step['id'],'kind':'rect','x':x,'y':y,'width':280,'height':132,
                  'label':step['title'],'sublabel':step['sub'][0],
                  'fill':'#f5f9ff' if step['id'] in ['cache','learning','store'] else '#ffffff','stroke':'#cbd7e6','flat':True})
arrows=[]
for i in range(11):
    sx,sy=positions[i];tx,ty=positions[i+1]
    if ty>sy:sp,tp='bottom','top'
    elif tx>sx:sp,tp='right','left'
    else:sp,tp='left','right'
    arrows.append({'id':'edge-'+str(i+1),'source':steps[i]['id'],'target':steps[i+1]['id'],'source_port':sp,'target_port':tp,'flow':'read'})
nodes.extend([
    {'id':'buffer','kind':'rect','x':450,'y':310,'width':260,'height':84,'label':'失联：插件本地暂存','sublabel':'恢复后补送会话条目','fill':'#fff8ed','stroke':'#dfc699','flat':True},
    {'id':'empty','kind':'rect','x':380,'y':625,'width':280,'height':82,'label':'提炼产物为 0 / 未完成','sublabel':'无新知识、跳过与错误需区分','fill':'#fff8ed','stroke':'#dfc699','flat':True}
])
arrows.extend([
    {'id':'buffer-write','source':'entry','target':'buffer','source_port':'bottom','target_port':'top','flow':'feedback'},
    {'id':'buffer-replay','source':'buffer','target':'cache','source_port':'right','target_port':'bottom','flow':'feedback'},
    {'id':'empty-result','source':'distill','target':'empty','source_port':'bottom','target_port':'top','flow':'feedback'}
])
diagram={'schema_version':1,'mode':'flowchart','style':1,'template_type':'architecture','text_policy':'strict',
         'quality_profile':'showcase','width':1380,'height':1020,
         'title':'Codex → Cognee → Memlane','subtitle':'先保存会话增量，再触发提炼；Memlane 可先接收原始记录，知识完成后再补入知识层。',
         'nodes':nodes,'arrows':arrows,'legend':[],'footer':'固定插件 1.7.7 / 上游 997fd6f · 实装 Cognee 1.6.7 · 示例状态，不读取真实会话',
         'footer_x':40,'footer_y':990}
data={'steps':steps,'versions':{'plugin':'1.7.7','upstream':'997fd6f72253aa97b49996eb1b032793f0257935','cognee':'1.6.7'},'scope':'本机固定源码流程说明；示例计数与内容不代表实时运行状态'}
(HERE/'flow.json').write_text(json.dumps(diagram,ensure_ascii=False,indent=2))
(HERE/'facts.json').write_text(json.dumps(data,ensure_ascii=False,indent=2))
if (HERE/'flow.svg').exists():
    ET.register_namespace('', 'http://www.w3.org/2000/svg')
    svg_root=ET.fromstring((HERE/'flow.svg').read_text())
    ns='{http://www.w3.org/2000/svg}'
    for old in list(svg_root):
        if old.get('id','').startswith('lane-'):
            svg_root.remove(old)
    for i, (x, y, text) in enumerate([(40,109,'01  会话采集与触发  →'),(40,429,'02  Cognee 后台处理  ←'),(380,749,'03  Memlane 分层接收与消费  →')]):
        group=ET.SubElement(svg_root, ns+'g', {'id':f'lane-{i}', 'data-graph-role':'reserved','data-graph-bounds':f'{x},{y-16},{x+360},{y+2}'})
        ET.SubElement(group, ns+'text', {'x':str(x),'y':str(y),'font-size':'16','font-weight':'600','fill':'#60748c','class':'lane-caption'}).text=text
    svg=ET.tostring(svg_root,encoding='unicode')
    (HERE/'flow.svg').write_text(svg)
    template=(HERE/'template.html').read_text()
    (HERE/'index.html').write_text(template.replace('<!-- FLOW_SVG -->',svg).replace('/* FLOW_DATA */',json.dumps(data,ensure_ascii=False)))
print('已生成流程定义与事实说明。存在 flow.svg 时同步生成离线 HTML。')
