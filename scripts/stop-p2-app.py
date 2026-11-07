#!/usr/bin/env python3
"""通过隔离 App 的明确停止入口清理本轮服务，不操作其他 job。"""
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1]
d=SourceFileLoader('p2common',str(ROOT/'scripts/p2-common.py')).load_module()
ui=d.UI.UI(d.app,d.fixture,ROOT/'build/p2-screenshots')
ui.quit(stop=True)
print('P2 isolated App stop-and-quit completed',flush=True)
