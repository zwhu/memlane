"""Bounded macOS input/AX helpers for Memlane's isolated P0 test App."""
import ctypes as C
import subprocess
import time

class Point(C.Structure):
    _fields_=[('x',C.c_double),('y',C.c_double)]
class Size(C.Structure):
    _fields_=[('width',C.c_double),('height',C.c_double)]
class Rect(C.Structure):
    _fields_=[('origin',Point),('size',Size)]

def fn(lib,name,result,*args):
    f=getattr(lib,name);f.restype=result;f.argtypes=args;return f

class Mac:
    def __init__(self):
        self.cf=C.CDLL('/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation')
        self.ax=C.CDLL('/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices')
        self.cg=C.CDLL('/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics')
        self.objc=C.CDLL('/usr/lib/libobjc.A.dylib')
        self.appkit=C.CDLL('/System/Library/Frameworks/AppKit.framework/AppKit')
        self.release=fn(self.cf,'CFRelease',None,C.c_void_p)
        self.retain=fn(self.cf,'CFRetain',C.c_void_p,C.c_void_p)
        self.make_string=fn(self.cf,'CFStringCreateWithCString',C.c_void_p,C.c_void_p,C.c_char_p,C.c_uint32)
        self.get_string=fn(self.cf,'CFStringGetCString',C.c_bool,C.c_void_p,C.c_void_p,C.c_long,C.c_uint32)
        self.array_count=fn(self.cf,'CFArrayGetCount',C.c_long,C.c_void_p)
        self.array_at=fn(self.cf,'CFArrayGetValueAtIndex',C.c_void_p,C.c_void_p,C.c_long)
        self.bool_value=fn(self.cf,'CFBooleanGetValue',C.c_bool,C.c_void_p)
        self.number_value=fn(self.cf,'CFNumberGetValue',C.c_bool,C.c_void_p,C.c_int,C.c_void_p)
        self.dictionary_get=fn(self.cf,'CFDictionaryGetValue',C.c_void_p,C.c_void_p,C.c_void_p)
        self.ax_app=fn(self.ax,'AXUIElementCreateApplication',C.c_void_p,C.c_int)
        self.ax_system=fn(self.ax,'AXUIElementCreateSystemWide',C.c_void_p)
        self.ax_pid=fn(self.ax,'AXUIElementGetPid',C.c_int,C.c_void_p,C.POINTER(C.c_int))
        self.ax_copy=fn(self.ax,'AXUIElementCopyAttributeValue',C.c_int,C.c_void_p,C.c_void_p,C.POINTER(C.c_void_p))
        self.ax_value=fn(self.ax,'AXValueGetValue',C.c_bool,C.c_void_p,C.c_int,C.c_void_p)
        self.ax_action=fn(self.ax,'AXUIElementPerformAction',C.c_int,C.c_void_p,C.c_void_p)
        self.mouse_event=fn(self.cg,'CGEventCreateMouseEvent',C.c_void_p,C.c_void_p,C.c_uint32,Point,C.c_uint32)
        self.key_event=fn(self.cg,'CGEventCreateKeyboardEvent',C.c_void_p,C.c_void_p,C.c_uint16,C.c_bool)
        self.current_event=fn(self.cg,'CGEventCreate',C.c_void_p,C.c_void_p)
        self.event_location=fn(self.cg,'CGEventGetLocation',Point,C.c_void_p)
        self.event_flags=fn(self.cg,'CGEventSetFlags',None,C.c_void_p,C.c_uint64)
        self.event_type=fn(self.cg,'CGEventSetType',None,C.c_void_p,C.c_uint32)
        self.post=fn(self.cg,'CGEventPost',None,C.c_uint32,C.c_void_p)
        self.event_field=fn(self.cg,'CGEventSetIntegerValueField',None,C.c_void_p,C.c_uint32,C.c_int64)
        self.window_list=fn(self.cg,'CGWindowListCopyWindowInfo',C.c_void_p,C.c_uint32,C.c_uint32)
        self.session_copy=fn(self.cg,'CGSessionCopyCurrentDictionary',C.c_void_p)
        self.display_list=fn(self.cg,'CGGetActiveDisplayList',C.c_int,C.c_uint32,C.POINTER(C.c_uint32),C.POINTER(C.c_uint32))
        self.display_bounds=fn(self.cg,'CGDisplayBounds',Rect,C.c_uint32)
        self.cls=fn(self.objc,'objc_getClass',C.c_void_p,C.c_char_p)
        self.sel=fn(self.objc,'sel_registerName',C.c_void_p,C.c_char_p)
        self.send0=fn(self.objc,'objc_msgSend',C.c_void_p,C.c_void_p,C.c_void_p)
        # Make separate CFUNCTYPE wrappers so signatures do not overwrite each other.
        address=C.cast(self.objc.objc_msgSend,C.c_void_p).value
        self.send1=C.CFUNCTYPE(C.c_void_p,C.c_void_p,C.c_void_p,C.c_long)(address)
        self.send_bool=C.CFUNCTYPE(C.c_bool,C.c_void_p,C.c_void_p)(address)
        self.run_loop=fn(self.cf,'CFRunLoopRunInMode',C.c_int,C.c_void_p,C.c_double,C.c_bool)
    def string(self,text):return self.make_string(None,text.encode(),0x08000100)
    def text(self,value):
        if not value:return ''
        buffer=C.create_string_buffer(65536)
        if not self.get_string(value,buffer,len(buffer),0x08000100):raise RuntimeError('CFString exceeds test bound')
        return buffer.value.decode()
    def attr(self,element,name,optional=False):
        key=self.string(name);value=C.c_void_p()
        try:code=self.ax_copy(element,key,C.byref(value))
        finally:self.release(key)
        if code:
            if optional:return None
            raise RuntimeError((name,'AXError',code))
        return value.value
    def text_attr(self,element,name,optional=False):
        value=self.attr(element,name,optional)
        try:return self.text(value)
        finally:
            if value:self.release(value)
    def bool_attr(self,element,name):
        value=self.attr(element,name)
        try:return self.bool_value(value)
        finally:self.release(value)
    def rectangle(self,element):
        values=[]
        for name,kind,structure in [('AXPosition',1,Point),('AXSize',2,Size)]:
            value=self.attr(element,name)
            try:
                out=structure();assert self.ax_value(value,kind,C.byref(out)),name;values.append(out)
            finally:self.release(value)
        return {'x':values[0].x,'y':values[0].y,'width':values[1].width,'height':values[1].height}
    def window(self,pid):
        app=self.ax_app(pid)
        try:array=self.attr(app,'AXWindows')
        finally:self.release(app)
        try:
            if not self.array_count(array):raise RuntimeError('test App has no AX window')
            return self.retain(self.array_at(array,0))
        finally:self.release(array)
    def activate(self,pid):
        app=self.send1(self.cls(b'NSRunningApplication'),self.sel(b'runningApplicationWithProcessIdentifier:'),pid)
        assert app,'test App no longer running'
        assert self.send1(app,self.sel(b'activateWithOptions:'),3),'activate test App failed'
    def front_pid(self):
        workspace=self.send0(self.cls(b'NSWorkspace'),self.sel(b'sharedWorkspace'))
        mode=self.string('kCFRunLoopDefaultMode')
        try:self.run_loop(mode,.02,False)
        finally:self.release(mode)
        front=self.send0(workspace,self.sel(b'frontmostApplication'))
        if front:
            pid=int(self.send0(front,self.sel(b'processIdentifier')) or 0)
            if pid:return pid
        system=self.ax_system()
        try:front=self.attr(system,'AXFocusedApplication')
        finally:self.release(system)
        try:
            pid=C.c_int();assert self.ax_pid(front,C.byref(pid))==0
            return pid.value
        finally:self.release(front)
    def voiceover_enabled(self):
        workspace=self.send0(self.cls(b'NSWorkspace'),self.sel(b'sharedWorkspace'))
        return self.send_bool(workspace,self.sel(b'isVoiceOverEnabled'))
    def menu_press(self,pid,menu_title,item_fragments):
        app=self.ax_app(pid);bar=None;children=None;top=None;found=[]
        try:
            bar=self.attr(app,'AXMenuBar');children=self.attr(bar,'AXChildren')
            for i in range(self.array_count(children)):
                element=self.array_at(children,i)
                if self.text_attr(element,'AXTitle',True)==menu_title:top=self.retain(element);break
            assert top,('native menu missing',menu_title)
            action=self.string('AXPress')
            try:
                assert self.ax_action(top,action)==0
                def walk(element,depth):
                    if depth>4:return
                    title=self.text_attr(element,'AXTitle',True)
                    if any(fragment in title for fragment in item_fragments):found.append((self.retain(element),title))
                    kids=self.attr(element,'AXChildren',True)
                    if kids:
                        try:
                            for i in range(self.array_count(kids)):walk(self.array_at(kids,i),depth+1)
                        finally:self.release(kids)
                time.sleep(.15);walk(top,0);assert len(found)==1,('native menu matches',[title for _,title in found])
                assert self.ax_action(found[0][0],action)==0
                return {'menu':menu_title,'item':found[0][1],'path':'system menu AXPress'}
            finally:self.release(action)
        finally:
            for element,_ in found:self.release(element)
            for element in [top,children,bar,app]:
                if element:self.release(element)
    def session_state(self):
        value=self.session_copy()
        if not value:return {'graphical_session':False,'screen_locked':True}
        result={'graphical_session':True}
        try:
            for key_name,field in [('kCGSSessionOnConsoleKey','on_console'),('CGSSessionScreenIsLocked','screen_locked')]:
                key=self.string(key_name)
                try:
                    item=self.dictionary_get(value,key)
                    result[field]=self.bool_value(item) if item else False
                finally:self.release(key)
        finally:self.release(value)
        return result
    def require_unlocked(self):
        state=self.session_state()
        if not state.get('graphical_session') or state.get('screen_locked'):
            raise RuntimeError(('macOS desktop is locked or unavailable',state))
    def mouse(self,kind,x,y,flags=0,clicks=1,delta=None):
        old=self.current_event(None)
        try:position=self.event_location(old)
        finally:self.release(old)
        event=self.mouse_event(None,kind,Point(x,y),0);assert event
        try:
            self.event_flags(event,flags);self.event_field(event,1,clicks)
            self.event_field(event,4,int(delta[0] if delta else x-position.x))
            self.event_field(event,5,int(delta[1] if delta else y-position.y))
            self.post(0,event)
        finally:self.release(event)
    def click(self,x,y,flags=0):
        self.mouse(5,x,y,flags);time.sleep(.04);self.mouse(1,x,y,flags);time.sleep(.06);self.mouse(2,x,y,flags);time.sleep(.15)
    def drag(self,x,y,dx,dy):
        self.mouse(5,x,y);self.mouse(1,x,y);time.sleep(.08)
        for i in range(1,13):self.mouse(6,x+dx*i/12,y+dy*i/12);time.sleep(.025)
        self.mouse(2,x+dx,y+dy);time.sleep(.2)
    def key(self,code,flags=0,tap=1):
        for down in [True,False]:
            event=self.key_event(None,code,down);assert event
            try:self.event_flags(event,flags);self.post(tap,event)
            finally:self.release(event)
            time.sleep(.035)
    def type_ascii(self,text):
        codes={'a':0,'s':1,'d':2,'f':3,'h':4,'g':5,'z':6,'x':7,'c':8,'v':9,'b':11,'q':12,'w':13,'e':14,'r':15,'y':16,'t':17,'o':31,'u':32,'i':34,'p':35,'l':37,'j':38,'k':40,'n':45,'m':46,' ':49,'1':18,'2':19,'3':20,'4':21,'5':23,'6':22,'7':26,'8':28,'9':25,'0':29}
        for char in text:
            if char=='_':self.key(27,1<<17)
            else:self.key(codes[char])
    def chord(self,code,flags):
        modifiers=[(59,1<<18),(58,1<<19),(55,1<<20),(56,1<<17)]
        held=[];active=0
        def change(keycode,down,new_flags):
            event=self.key_event(None,keycode,down);assert event
            try:
                self.event_type(event,12);self.event_flags(event,new_flags);self.post(0,event)
            finally:self.release(event)
            time.sleep(.08)
        try:
            for keycode,mask in modifiers:
                if flags&mask:
                    active|=mask;change(keycode,True,active);held.append((keycode,mask))
            self.key(code,active,tap=0)
        finally:
            for keycode,mask in reversed(held):
                active&=~mask;change(keycode,False,active)
    def bundle_pids(self,identifier):
        name=self.string(identifier)
        try:apps=self.send1(self.cls(b'NSRunningApplication'),self.sel(b'runningApplicationsWithBundleIdentifier:'),name)
        finally:self.release(name)
        return [int(self.send0(self.array_at(apps,i),self.sel(b'processIdentifier'))) for i in range(self.array_count(apps))]
    def visible_windows(self,pids):
        values=self.window_list(1,0);keys={name:self.string(name) for name in ['kCGWindowOwnerPID','kCGWindowNumber','kCGWindowLayer','kCGWindowBounds']};found=[]
        try:
            for i in range(self.array_count(values)):
                row=self.array_at(values,i);pid=C.c_longlong();value=self.dictionary_get(row,keys['kCGWindowOwnerPID'])
                if not value or not self.number_value(value,4,C.byref(pid)) or pid.value not in pids:continue
                item={'pid':pid.value}
                for name,field in [('kCGWindowNumber','number'),('kCGWindowLayer','layer')]:
                    value=self.dictionary_get(row,keys[name]);number=C.c_longlong();assert self.number_value(value,4,C.byref(number));item[field]=number.value
                bounds=self.dictionary_get(row,keys['kCGWindowBounds']);rect={}
                for name in ['X','Y','Width','Height']:
                    key=self.string(name)
                    try:
                        value=self.dictionary_get(bounds,key);number=C.c_double();assert self.number_value(value,6,C.byref(number));rect[name.lower()]=number.value
                    finally:self.release(key)
                item['bounds']=rect;found.append(item)
        finally:
            self.release(values)
            for key in keys.values():self.release(key)
        return found
    def click_button(self,window,name):
        button=self.attr(window,name)
        try:r=self.rectangle(button)
        finally:self.release(button)
        self.click(r['x']+r['width']/2,r['y']+r['height']/2)
        return r
    def dock_click(self,app_name,window_title=None):
        name=self.string('com.apple.dock')
        try:apps=self.send1(self.cls(b'NSRunningApplication'),self.sel(b'runningApplicationsWithBundleIdentifier:'),name)
        finally:self.release(name)
        assert self.array_count(apps)==1
        pid=int(self.send0(self.array_at(apps,0),self.sel(b'processIdentifier')))
        app=self.ax_app(pid)
        found=[]
        def walk(element,depth):
            if depth>5:return
            title=self.text_attr(element,'AXTitle',optional=True)
            subrole=self.text_attr(element,'AXSubrole',optional=True)
            if title==app_name or (window_title and window_title in title and 'MinimizedWindow' in subrole):
                found.append({'title':title,'subrole':subrole,'bounds':self.rectangle(element),'element':self.retain(element)})
            children=self.attr(element,'AXChildren',optional=True)
            if children:
                try:
                    for i in range(self.array_count(children)):walk(self.array_at(children,i),depth+1)
                finally:self.release(children)
        def selected_item():
            for item in found:self.release(item['element'])
            found.clear();walk(app,0)
            minimized=[item for item in found if 'MinimizedWindow' in item['subrole']]
            selected=minimized or [item for item in found if item['title']==app_name]
            assert len(selected)==1,('unique test Dock item not found',app_name,found)
            return selected[0]
        try:
            item=selected_item();r=item['bounds'];x=r['x']+r['width']/2;y=r['y']+r['height']/2
            action=self.string('AXPress')
            try:assert self.ax_action(item['element'],action)==0,'Dock AXPress failed'
            finally:self.release(action)
            return {**{key:value for key,value in item.items() if key!='element'},'restore_path':'system Dock AXPress'}
        finally:
            for item in found:self.release(item['element'])
            self.release(app)
    def window_id(self,pid):
        values=self.window_list(1,0);keys={name:self.string(name) for name in ['kCGWindowOwnerPID','kCGWindowNumber','kCGWindowLayer','kCGWindowBounds']};found=[]
        try:
            for i in range(self.array_count(values)):
                row=self.array_at(values,i);numbers={}
                for name in ['kCGWindowOwnerPID','kCGWindowNumber','kCGWindowLayer']:
                    key=keys[name]
                    value=self.dictionary_get(row,key);number=C.c_longlong()
                    if value and self.number_value(value,4,C.byref(number)):numbers[name]=number.value
                if numbers.get('kCGWindowOwnerPID')==pid and numbers.get('kCGWindowLayer')==0:
                    bounds=self.dictionary_get(row,keys['kCGWindowBounds']);dimensions={}
                    for name in ['Width','Height']:
                        key=self.string(name)
                        try:
                            value=self.dictionary_get(bounds,key);number=C.c_double()
                            if value and self.number_value(value,6,C.byref(number)):dimensions[name]=number.value
                        finally:self.release(key)
                    if dimensions.get('Width',0)>=980 and dimensions.get('Height',0)>=620:found.append(numbers['kCGWindowNumber'])
        finally:
            self.release(values)
            for key in keys.values():self.release(key)
        assert len(found)==1,('own App window IDs',found)
        return found[0]
    def capture(self,pid,path):
        identifier=self.window_id(pid)
        self.capture_id(identifier,path)
        return identifier
    def capture_id(self,identifier,path):
        subprocess.run(['/usr/sbin/screencapture','-x','-l',str(identifier),str(path)],check=True,timeout=15)

class InputSources:
    def __init__(self,mac):
        self.mac=mac
        self.lib=C.CDLL('/System/Library/Frameworks/Carbon.framework/Frameworks/HIToolbox.framework/HIToolbox')
        self.current=fn(self.lib,'TISCopyCurrentKeyboardInputSource',C.c_void_p)
        self.sources=fn(self.lib,'TISCreateInputSourceList',C.c_void_p,C.c_void_p,C.c_bool)
        self.property=fn(self.lib,'TISGetInputSourceProperty',C.c_void_p,C.c_void_p,C.c_void_p)
        self.select=fn(self.lib,'TISSelectInputSource',C.c_int,C.c_void_p)
        self.ascii=fn(self.lib,'TISCopyCurrentASCIICapableKeyboardLayoutInputSource',C.c_void_p)
        self.id_key=C.c_void_p.in_dll(self.lib,'kTISPropertyInputSourceID').value
    def identifier(self,source):return self.mac.text(self.property(source,self.id_key))
    def find(self,identifier):
        values=self.sources(None,False)
        try:
            for i in range(self.mac.array_count(values)):
                source=self.mac.array_at(values,i)
                if self.identifier(source)==identifier:return self.mac.retain(source)
        finally:self.mac.release(values)
        raise RuntimeError(('installed input source missing',identifier))
    def use(self,source):
        assert self.select(source)==0,'input source selection failed'
        time.sleep(.15)
