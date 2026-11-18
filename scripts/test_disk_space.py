import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('disk_space', Path(__file__).with_name('disk-space.py'))
disk = importlib.util.module_from_spec(spec)
spec.loader.exec_module(disk)

class DiskSafety(unittest.TestCase):
    def test_daily_official_data_and_model_survive_without_paths_in_ps(self):
        from types import SimpleNamespace
        with tempfile.TemporaryDirectory() as name:
            root = Path(name).resolve(); (root/'build').mkdir()
            tmp = root/'temp'; tmp.mkdir()
            runtime = tmp/'memlane-original/initialization/client/official'
            data = runtime/'cognee'; model = runtime/'components/embedding'
            for folder in (data/'system', data/'data', model): folder.mkdir(parents=True)
            database = data/'system/cognee_db'; weights = model/'model.onnx'
            old = tmp/'memlane-unused/model.onnx'; old.parent.mkdir()
            for file in (database, weights, old):
                with file.open('wb') as stream: stream.truncate(8*1024**2)
            alias = root/'official-alias'; alias.symlink_to(data, target_is_directory=True)
            daily = root/'.local/daily'; daily.mkdir(parents=True)
            environment = daily/'environment.json'; environment.write_text(json.dumps({'HF_HOME':str(model), 'DEFAULT_USER_PASSWORD':'synthetic-private'}))
            config = daily/'runtime.json'; config.write_text(json.dumps({'official_data':str(alias), 'service_environment_file':str(environment)}))
            with patch.object(disk,'ROOT',root), patch.object(disk,'KEEP',root/'build/latest'), patch.object(disk,'RETAINED',root/'absent'), patch.object(disk,'DEPENDENCIES',root/'absent-dependencies'), patch.object(disk,'worktrees',return_value=[]), patch.object(disk,'active_arguments',return_value=['123 python official_service_dev.py --config '+str(config)]), patch.object(disk.tempfile,'gettempdir',return_value=str(tmp)), patch.object(disk.subprocess,'run',return_value=SimpleNamespace(stdout='')):
                result = disk.prune_history()
            self.assertTrue(database.is_file()); self.assertTrue(weights.is_file())
            self.assertFalse(old.exists())
            self.assertIn(str(tmp/'memlane-original'),result['skipped_active'])

    def test_explicit_dependency_manifest_protects_temporary_runtime(self):
        from types import SimpleNamespace
        with tempfile.TemporaryDirectory() as name:
            root=Path(name).resolve(); (root/'build').mkdir(); tmp=root/'temp'; tmp.mkdir()
            runtime=tmp/'memlane-owned/MemlaneRuntime.app'; runtime.mkdir(parents=True)
            manifest=root/'dependencies.txt'; manifest.write_text(str(runtime/'Contents/bin/helper')+'\n')
            with patch.object(disk,'ROOT',root), patch.object(disk,'KEEP',root/'build/latest'), patch.object(disk,'RETAINED',root/'absent'), patch.object(disk,'DEPENDENCIES',manifest), patch.object(disk,'worktrees',return_value=[]), patch.object(disk,'active_arguments',return_value=[]), patch.object(disk.tempfile,'gettempdir',return_value=str(tmp)), patch.object(disk.subprocess,'run',return_value=SimpleNamespace(stdout='')):
                disk.prune_history()
            self.assertTrue(runtime.is_dir())

    def test_cache_dependency_and_invalid_config_fail_closed(self):
        with tempfile.TemporaryDirectory() as name:
            root=Path(name).resolve(); daily=root/'.local/daily'; daily.mkdir(parents=True)
            cache=root/'.cache/cognee-service-build'; cache.mkdir(parents=True)
            library=cache/'runtime/libpython.dylib'; library.parent.mkdir(); library.write_bytes(b'preserved')
            config=daily/'runtime.json'; config.write_text(json.dumps({'python':str(library)}))
            with patch.object(disk,'ROOT',root), patch.object(disk,'DEPENDENCIES',root/'absent'), patch.object(disk,'active_arguments',return_value=[]), patch.object(disk,'size',return_value=100*disk.GIB):
                disk.trim_caches(0)
                self.assertTrue(library.is_file())
                config.write_text('{broken-private-config')
                with self.assertRaisesRegex(RuntimeError,'cleanup refused'):
                    disk.trim_caches(0)
            self.assertTrue(library.is_file())

    def test_mapped_daemon_and_explicit_dependencies_survive_pruning(self):
        from types import SimpleNamespace
        with tempfile.TemporaryDirectory() as name:
            root = Path(name).resolve(); build = root/'build'; build.mkdir(); tmp=root/'temp'; tmp.mkdir()
            owner=build/'owner/MemlaneOwner.app'; owner.mkdir(parents=True)
            dependency=build/'dependency/MemlaneDependency.app'; dependency.mkdir(parents=True)
            old=build/'old/MemlaneOld.app'; old.mkdir(parents=True)
            manifest=root/'dependencies.txt'; manifest.write_text(str(dependency)+'\n')
            mapped=SimpleNamespace(stdout='p123\nn'+str(owner/'Contents/Resources/bin/memlaned')+'\n')
            with patch.object(disk,'ROOT',root), patch.object(disk,'KEEP',build/'latest'), patch.object(disk,'RETAINED',root/'absent'), patch.object(disk,'DEPENDENCIES',manifest), patch.object(disk,'worktrees',return_value=[]), patch.object(disk,'active_arguments',return_value=['123 memlaned']), patch.object(disk.tempfile,'gettempdir',return_value=str(tmp)), patch.object(disk.subprocess,'run',return_value=mapped):
                disk.prune_history()
            self.assertTrue(owner.exists()); self.assertTrue(dependency.exists())
            self.assertFalse(old.exists())

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
        usage=namedtuple('Usage','total used free')(500*disk.GIB,451*disk.GIB,49*disk.GIB)
        with patch('sys.argv',['disk-space.py']), patch.object(disk.shutil,'disk_usage',return_value=usage), patch.object(disk,'trim_caches',return_value=[]):
            with self.assertRaises(SystemExit): disk.main()

    def test_sufficient_space_permits_task(self):
        from collections import namedtuple
        usage=namedtuple('Usage','total used free')(500*disk.GIB,450*disk.GIB,50*disk.GIB)
        with patch('sys.argv',['disk-space.py']), patch.object(disk.shutil,'disk_usage',return_value=usage), patch.object(disk,'trim_caches',return_value=[]): disk.main()

    def test_finish_preserves_output_for_following_verification(self):
        from collections import namedtuple
        from types import SimpleNamespace
        usage=namedtuple('Usage','total used free')(500*disk.GIB,350*disk.GIB,150*disk.GIB)
        with tempfile.TemporaryDirectory() as name:
            root=Path(name); (root/'build').mkdir()
            output=root/'build/candidate/runtime'; output.mkdir(parents=True)
            executable=output/'memlane-cognee-service'; executable.write_bytes(b'x'*(8*1024**2))
            library=output/'libpython.dylib'; library.write_bytes(b'x'*(8*1024**2))
            temporary=root/'temp'; temporary.mkdir()
            with patch('sys.argv',['disk-space.py','--finish']), patch.object(disk,'ROOT',root), patch.object(disk.shutil,'disk_usage',return_value=usage), patch.object(disk,'other_tasks',return_value=False), patch.object(disk,'trim_caches',return_value=[]), patch.object(disk,'worktrees',return_value=[]), patch.object(disk,'active_arguments',return_value=[]), patch.object(disk.tempfile,'gettempdir',return_value=str(temporary)), patch.object(disk.subprocess,'run',return_value=SimpleNamespace(stdout='')):
                disk.main()
            self.assertTrue(executable.is_file()); self.assertTrue(library.is_file())

if __name__=='__main__': unittest.main()
