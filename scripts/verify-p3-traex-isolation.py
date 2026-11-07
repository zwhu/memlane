#!/usr/bin/env python3
"""用已安装 TRAE CLI 的实际提示输入复核隔离；不调用模型，不读取聊天。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT=Path(__file__).resolve().parents[1]
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--model',default='GPT-6.1-Sol',help='本次已验证的现有模型选择')
a=p.parse_args()
out=ROOT/'build/p3';out.mkdir(parents=True,exist_ok=True)
with tempfile.TemporaryDirectory(prefix='memlane-traex-isolation-') as name:
    root=Path(name);shared=root/'agent-home';private=shared/'cli';work=root/'task'
    private.mkdir(parents=True);work.mkdir()
    auth=Path(os.environ.get('TRAECLI_HOME',str(Path(os.environ.get('TRAE_HOME',str(Path.home()/'.trae')))/'cli')))/'auth.json'
    if auth.exists(): (private/'auth.json').symlink_to(auth)
    env={k:v for k,v in os.environ.items() if k in ('PATH','HOME','TMPDIR','LANG','LC_ALL','HTTPS_PROXY','HTTP_PROXY','ALL_PROXY','NO_PROXY')}
    env.update(TRAE_HOME=str(shared),TRAECLI_HOME=str(private))
    cfg={'model':a.model,'model_provider':'trae','resource_dirs':[],'project_doc_max_bytes':0,
         'skills.bundled.enabled':False,'skills.include_instructions':False,
         'memories.generate_memories':False,'memories.use_memories':False,'web_search':'disabled'}
    cmd=['traecli','debug','prompt-input']
    for key,value in cfg.items(): cmd+=['-c',key+'='+json.dumps(value,ensure_ascii=False)]
    entries=Path.home()/'.agents/skills'
    disabled=['{path='+json.dumps(str(e/'SKILL.md'))+',enabled=false}' for e in entries.iterdir()] if entries.exists() else []
    cmd+=['-c','skills.config=['+','.join(disabled)+']','-c','model_tool_modes={'+json.dumps(a.model)+'="code_mode_only"}']
    for flag in ('skip_host_skill_discovery','code_mode_host'): cmd+=['--enable',flag]
    for flag in ('shell_tool','unified_exec','plugins','hooks','memories','multi_agent','multi_agent_v2','skill_search','skill_mcp_dependency_install','workspace_dependencies','goals','team_memory','memory_self_evolution','skill_evolution','browser_use','browser_use_external','computer_use','image_generation'):
        cmd+=['--disable',flag]
    cmd+=['Memlane P3 synthetic isolation probe. Do not read files or execute tools.']
    r=subprocess.run(cmd,cwd=work,env=env,capture_output=True,text=True,timeout=40)
    path=out/'traex-isolated-prompt-input.json';path.write_text(r.stdout)
    valid=False;skills=True;memories=True
    if r.returncode==0:
        items=json.loads(r.stdout)
        text='\n'.join(c.get('text','') for item in items for c in item.get('content',[]) if isinstance(c,dict))
        skills='<skills_instructions>' in text or '### Available skills' in text
        memories='MEMORY_SUMMARY' in text or 'memory_summary.md' in text or '<oai-mem-citation>' in text
        valid=not skills and not memories
    report={'status':'passed' if valid else 'failed','exit_code':r.returncode,'model':a.model,'actual_prompt_input':True,
            'model_call':False,'skills_injected':skills,'memories_injected':memories,
            'global_skill_entries_disabled':len(disabled),'input_sha256':hashlib.sha256(r.stdout.encode()).hexdigest(),
            'prompt_input_path':str(path)}
    if r.returncode: report['error']=r.stderr[:1024]
    (out/'traex-isolation.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
    print(json.dumps(report,ensure_ascii=False))
    raise SystemExit(0 if valid else 1)
