"""Read and press one explicitly owned background activity switch through AX."""
import ctypes as C
import subprocess
from importlib.machinery import SourceFileLoader
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
M = SourceFileLoader('backgroundmac', str(ROOT / 'scripts/mac-native.py')).load_module()


class BackgroundToggle:
    def __init__(self, name):
        assert name in ('MemlaneBackground', 'MemlaneBackgroundFixed', 'MemlaneBackgroundFinal'), 'only isolated acceptance App names are authorized'
        self.name = name
        self.mac = M.Mac()
        self.type_id = M.fn(self.mac.cf, 'CFGetTypeID', C.c_ulong, C.c_void_p)
        self.number_type = M.fn(self.mac.cf, 'CFNumberGetTypeID', C.c_ulong)
        self.bool_type = M.fn(self.mac.cf, 'CFBooleanGetTypeID', C.c_ulong)
        self.string_type = M.fn(self.mac.cf, 'CFStringGetTypeID', C.c_ulong)

    def value(self, element):
        value = self.mac.attr(element, 'AXValue', True)
        if not value:
            return None
        try:
            kind = self.type_id(value)
            if kind == self.bool_type():
                return int(self.mac.bool_value(value))
            if kind == self.number_type():
                number = C.c_longlong()
                assert self.mac.number_value(value, 4, C.byref(number))
                return number.value
            if kind == self.string_type():
                text = self.mac.text(value)
                return int(text) if text in ('0', '1') else None
            return None
        finally:
            self.mac.release(value)

    def scan(self):
        self.mac.require_unlocked()
        pids = self.mac.bundle_pids('com.apple.systempreferences')
        assert len(pids) == 1, 'unique System Settings process required'
        self.pid = pids[0]
        try:
            window = self.mac.window(self.pid)
        except RuntimeError as error:
            if error.args == (('AXWindows', 'AXError', -25204),):
                raise AssertionError('System Settings AX window is not ready') from error
            raise
        assert self.mac.text_attr(window, 'AXTitle') == '登录项与扩展', 'background settings pane required'
        matches, values = [], {}
        count = 0

        def walk(element, depth):
            nonlocal count
            count += 1
            assert count <= 1800 and depth <= 25, 'AX tree exceeded test bound'
            role = self.mac.text_attr(element, 'AXRole', True)
            identifier = self.mac.text_attr(element, 'AXIdentifier', True)
            description = self.mac.text_attr(element, 'AXDescription', True)
            if role == 'AXCheckBox' and identifier.startswith('background-switch-') and not description:
                value = self.value(element)
                if value in (0, 1):
                    values.setdefault(identifier, []).append(value)
                if identifier == 'background-switch-' + self.name:
                    assert value in (0, 1)
                    matches.append(self.mac.retain(element))
            children = self.mac.attr(element, 'AXChildren', True)
            if children:
                try:
                    for i in range(self.mac.array_count(children)):
                        walk(self.mac.array_at(children, i), depth + 1)
                finally:
                    self.mac.release(children)

        try:
            walk(window, 0)
            assert len(matches) == 1, 'unique owned background checkbox required'
            return matches.pop(), {key: sorted(value) for key, value in values.items()}
        finally:
            for element in matches:
                self.mac.release(element)
            self.mac.release(window)

    def current(self):
        element, values = self.scan()
        try:
            return values['background-switch-' + self.name][0], values
        finally:
            self.mac.release(element)

    def set(self, desired):
        assert desired in (0, 1)
        element, values = self.scan()
        try:
            if values['background-switch-' + self.name][0] == desired:
                return
            assert self.mac.bool_attr(element, 'AXEnabled'), 'owned background switch is disabled'
            self.mac.activate(self.pid)
            action = self.mac.string('AXPress')
            try:
                assert self.mac.ax_action(element, action) == 0, 'system background AXPress failed'
            finally:
                self.mac.release(action)
        finally:
            self.mac.release(element)

    def open(self):
        subprocess.run(['open', '-b', 'com.apple.systempreferences', 'x-apple.systempreferences:com.apple.LoginItems-Settings.extension'], check=True)
