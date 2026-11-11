import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('disk_space', Path(__file__).with_name('disk-space.py'))
disk = importlib.util.module_from_spec(spec)
spec.loader.exec_module(disk)

class DiskSafety(unittest.TestCase):
    def test_pruning_preserves_worktree_latest_evidence_and_active_roots(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name); build = root/'build'; build.mkdir(); tmp=root/'temp'; tmp.mkdir()
            for n in ['old','prototype','latest','active']:
                p=build/n; p.mkdir(); (p/'model.onnx').write_bytes(b'x'*(8*1024**2))
                (p/'report.json').write_text('{}'); (p/'image.png').write_bytes(b'x'*(8*1024**2))
            (build/'old/MemlaneP1.app').mkdir(); (build/'old/MemlaneP1.app/tiny').write_text('synthetic')
            outside=root/'outside'; outside.write_bytes(b'x'*(8*1024**2)); (build/'old/link').symlink_to(outside)
            with patch.object(disk,'ROOT',root), patch.object(disk,'KEEP',build/'latest'), patch.object(disk,'worktrees',return_value=[build/'prototype']), patch.object(disk,'active_arguments',return_value=[str(build/'active')+'/daemon']), patch.object(disk.tempfile,'gettempdir',return_value=str(tmp)):
                result=disk.prune_history()
            self.assertEqual(result['removed_files'],[str(build/'old/model.onnx')])
            self.assertTrue(outside.exists())
            self.assertFalse((build/'old/MemlaneP1.app').exists())
            self.assertEqual(result['removed_bundles'], [str(build/'old/MemlaneP1.app')])
            for n in ['old','prototype','latest','active']:
                self.assertTrue((build/n/'report.json').exists()); self.assertTrue((build/n/'image.png').exists())

    def test_relative_active_path_and_concurrent_tasks_are_protected(self):
        p = disk.ROOT / 'build/new-output'
        self.assertTrue(disk.busy(p, ['123 python3 scripts/build-client.py --out build/new-output']))
        line = '123 /usr/bin/python3 '+str(disk.ROOT/'scripts/task.py')+' -- command'
        with patch.dict(disk.os.environ, {'MEMLANE_TASK_OWNER_PID':'124'}):
            self.assertTrue(disk.other_tasks([line]))
            self.assertTrue(disk.other_tasks(['123 /usr/bin/python3 scripts/task.py -- command']))
        with patch.dict(disk.os.environ, {'MEMLANE_TASK_OWNER_PID':'123'}):
            self.assertFalse(disk.other_tasks([line]))

    def test_low_space_rejects_task(self):
        from collections import namedtuple
        usage=namedtuple('Usage','total used free')(500*disk.GIB,401*disk.GIB,99*disk.GIB)
        with patch('sys.argv',['disk-space.py']), patch.object(disk.shutil,'disk_usage',return_value=usage), patch.object(disk,'trim_caches',return_value=[]):
            with self.assertRaises(SystemExit): disk.main()

    def test_sufficient_space_permits_task(self):
        from collections import namedtuple
        usage=namedtuple('Usage','total used free')(500*disk.GIB,350*disk.GIB,150*disk.GIB)
        with patch('sys.argv',['disk-space.py']), patch.object(disk.shutil,'disk_usage',return_value=usage), patch.object(disk,'trim_caches',return_value=[]): disk.main()

if __name__=='__main__': unittest.main()
