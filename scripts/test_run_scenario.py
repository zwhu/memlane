import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("scenario", Path(__file__).with_name("run-scenario.py"))
scenario = importlib.util.module_from_spec(spec)
spec.loader.exec_module(scenario)


class ScenarioExecution(unittest.TestCase):
    def plan(self):
        def command(name, code):
            return {"id": name, "argv": [sys.executable, "-c", code]}
        return dict(id="synthetic", requirement="synthetic only", baseline="test",
                    proves="command sequencing", does_not_prove="Native or business",
                    preflight=[command("preflight", "pass")],
                    steps=[command("step", "pass")],
                    cleanup=[command("cleanup", "pass")])

    def run_plan(self, plan, root):
        output = root / "evidence"
        code = scenario.execute(plan, output, root)
        return code, json.loads((output / "report.json").read_text())

    def test_precondition_failure_never_runs_business_and_still_cleans(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            plan = self.plan()
            plan["preflight"][0]["argv"][-1] = "raise SystemExit(2)"
            plan["steps"][0]["argv"][-1] = "open('unexpected', 'w').close()"
            code, report = self.run_plan(plan, root)
            self.assertEqual(code, 1)
            self.assertFalse((root / "unexpected").exists())
            self.assertEqual(report["failure_kind"], "precondition")
            self.assertEqual([a["group"] for a in report["actions"]], ["preflight", "cleanup"])

    def test_failed_step_is_not_retried_or_hidden_by_cleanup(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            plan = self.plan()
            plan["steps"][0]["argv"][-1] = "open('attempts','a').write('x'); raise SystemExit(3)"
            code, report = self.run_plan(plan, root)
            self.assertEqual(code, 1)
            self.assertEqual((root / "attempts").read_text(), "x")
            self.assertEqual(report["status"], "failed")
            self.assertEqual(report["actions"][-1]["status"], "passed")
            with self.assertRaises(FileExistsError):
                scenario.execute(plan, root / "evidence", root)

    def test_cleanup_failure_continues_remaining_cleanup(self):
        with tempfile.TemporaryDirectory() as name:
            plan = self.plan()
            plan["cleanup"][0]["argv"] = ["/nonexistent/memlane-synthetic-command"]
            plan["cleanup"].append({"id": "second-cleanup", "argv": [sys.executable, "-c", "pass"]})
            code, report = self.run_plan(plan, Path(name))
            self.assertEqual(code, 1)
            self.assertEqual(report["failure_kind"], "cleanup")
            self.assertEqual(report["actions"][-1]["status"], "passed")

    def test_success_does_not_claim_delivery_and_freezes_plan(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            plan = self.plan()
            code, report = self.run_plan(plan, root)
            self.assertEqual(code, 0)
            self.assertEqual(report["status"], "commands_passed")
            self.assertEqual(json.loads((root / "evidence/plan.json").read_text()), plan)
            self.assertEqual(len(report["plan_sha256"]), 64)

    def test_invalid_plan_rejected_before_execution(self):
        with tempfile.TemporaryDirectory() as name:
            path = Path(name) / "plan.json"
            plan = self.plan()
            plan["cleanup"] = []
            path.write_text(json.dumps(plan))
            with self.assertRaises(ValueError):
                scenario.load_plan(path)
            plan = self.plan()
            plan["steps"][0]["id"] = "preflight"
            path.write_text(json.dumps(plan))
            with self.assertRaises(ValueError):
                scenario.load_plan(path)

    def test_interrupted_cleanup_never_reports_success(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            real_run = scenario.subprocess.run

            def run(argv, **kwargs):
                if argv[-1] == "interrupt-cleanup":
                    raise KeyboardInterrupt()
                return real_run(argv, **kwargs)

            plan = self.plan()
            plan["cleanup"][0]["argv"][-1] = "interrupt-cleanup"
            with patch.object(scenario.subprocess, "run", side_effect=run):
                with self.assertRaises(KeyboardInterrupt):
                    scenario.execute(plan, root / "evidence", root)
            report = json.loads((root / "evidence/report.json").read_text())
            self.assertEqual(report["status"], "failed")
            self.assertEqual(report["actions"][-1]["status"], "interrupted")


if __name__ == "__main__":
    unittest.main()
