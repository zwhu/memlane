"""P1 real MyGo window driver; only an explicit isolated App is launched."""
import base64, json, os, plistlib, socket, subprocess, time, uuid
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]

def wait(fn, timeout=45):
    deadline=time.monotonic()+timeout
    last=None
    while time.monotonic()<deadline:
        try:
            value=fn()
            if value:return value
        except (OSError,AssertionError,StopIteration) as error:last=repr(error)
        time.sleep(.1)
    raise AssertionError(('deadline',last))

class UI:
    def __init__(self,app,fixture,out,visual=False,config=None):
        self.out=out;out.mkdir(parents=True,exist_ok=True)
        fixture.mkdir(parents=True,exist_ok=True)
        meta=plistlib.loads((app/'Contents/Info.plist').read_bytes())
        self.name=meta['CFBundleName'];self.seq=0
        self.path='/tmp/memlane-p1-'+uuid.uuid4().hex[:10]+'.sock'
        cmd=[str((app/'Contents/MacOS'/meta['CFBundleExecutable']).resolve())]
        if visual:cmd+=['--p1-visual-fixture']
        if config:cmd+=['--p0-config',str(config)]
        self.log=(out/'window.log').open('a')
        self.native_path='/tmp/memlane-p1-native-'+uuid.uuid4().hex[:10]+'.sock'
        env={'PATH':'/usr/bin:/bin','HOME':str(fixture/'ui-home'),'MYGO_AUTOMATION':self.path,'MEMLANE_NATIVE_PROBE':self.native_path}
        (fixture/'ui-home').mkdir(exist_ok=True)
        # Launch the bundle through Launch Services, as Finder does. After an
        # in-place rebuild, direct binary launch can leave BTM's app registration
        # stale and cause BundleProgram EX_CONFIG before the helper executes.
        command=['/usr/bin/open','-n','-W','--stdout',str(out/'window.log'),'--stderr',str(out/'window.log')]
        for name,value in env.items():command+=['--env',name+'='+value]
        command+=[str(app.resolve())]
        if len(cmd)>1:command+=['--args']+cmd[1:]
        self.proc=subprocess.Popen(command,cwd=fixture,stdout=self.log,stderr=self.log)
        def actual_pid():
            result=subprocess.run(['pgrep','-f','^'+cmd[0]],capture_output=True,text=True)
            pids=[int(x) for x in result.stdout.split()]
            return pids[0] if len(pids)==1 else None
        self.pid=wait(actual_pid)
        wait(lambda:Path(self.path).exists() or self.proc.poll() is not None)
        assert self.proc.poll() is None,('App exited',self.proc.returncode)
        self.s=socket.socket(socket.AF_UNIX);self.s.settimeout(30);self.s.connect(self.path)
        self.f=self.s.makefile('rb',buffering=0)
        wait(lambda:self.req('windows').get('windows'));wait(lambda:self.nodes())
        self.req('menu',path=['文件','打开 Memlane'])
    def req(self,method,**params):
        self.seq+=1
        self.s.sendall((json.dumps({'id':self.seq,'method':method,'params':params})+'\n').encode())
        line=self.f.readline()
        if not line:raise EOFError('UI closed')
        result=json.loads(line);assert 'error' not in result,result
        return result['result']
    def nodes(self):
        self.req('capture')
        return self.req('tree').get('nodes',[])
    def labels(self):return [n.get('label','') for n in self.nodes()]
    def node(self,label=None,key=None):
        return next(n for n in self.nodes() if (label is None or n.get('label')==label) and (key is None or n.get('key')==key))
    def press(self,label):
        n=next(n for n in self.nodes() if n.get('label')==label and 'press' in n.get('actions',[]))
        assert 'disabled' not in n.get('states',[]),n
        self.req('act',id=n['id'],action='press');self.req('idle',timeout=2000)
    def press_key(self,key):
        n=self.node(key=key);assert 'disabled' not in n.get('states',[]),n
        self.req('act',id=n['id'],action='press');self.req('idle',timeout=2000)
    def set(self,key,text):
        self.req('act',id=self.node(key=key)['id'],action='setValue',text=text)
        self.req('idle',timeout=2000)
    def capture(self,name):
        (self.out/name).write_bytes(base64.b64decode(self.req('capture')['png']))
        (self.out/(Path(name).stem+'-tree.json')).write_text(json.dumps(self.nodes(),ensure_ascii=False,indent=2)+'\n')
    def native(self,op='snapshot'):
        with socket.socket(socket.AF_UNIX) as s:
            s.settimeout(5);s.connect(self.native_path);s.sendall((json.dumps({'op':op})+'\n').encode())
            result=json.loads(s.makefile('rb',buffering=0).readline())
        assert 'error' not in result,result
        return result
    def system_capture(self,name):
        from importlib.machinery import SourceFileLoader
        mac=SourceFileLoader('p1mac',str(ROOT/'scripts/mac-native.py')).load_module().Mac()
        mac.require_unlocked()
        mac.activate(self.pid)
        own=next(w for w in mac.visible_windows([self.pid]) if w['layer']==0 and w['bounds']['width']>=980)
        bounds=own['bounds']
        # A real click in empty toolbar space brings this App's window forward.
        # Verify the actual NSWindow state; global AXFocusedApplication can be
        # unavailable even while the console is unlocked.
        mac.click(bounds['x']+500,bounds['y']+27)
        wait(lambda:self.native()['key_window'],timeout=10)
        self.req('capture');self.req('idle',timeout=2000)
        mac.capture(self.pid,self.out/name)
        snapshot=self.native();assert snapshot['key_window'] and snapshot['visible']
        (self.out/(Path(name).stem+'-capture.json')).write_text(json.dumps({'pid':self.pid,'window_key':snapshot['key_window'],'visible':snapshot['visible'],'actual_system_window':True})+'\n')

    def quit(self,stop=False):
        try:
            if stop:self.req('menu',path=['文件','停止服务并退出'])
            else:self.req('menu',path=[self.name,'Quit '+self.name])
        except EOFError:pass
        self.proc.wait(timeout=30);self.s.close();self.log.close()
