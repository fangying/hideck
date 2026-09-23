import importlib.machinery
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

loader = importlib.machinery.SourceFileLoader('hotplug', str(Path(__file__).with_name('hideck-qdc507-hotplug')))
spec = importlib.util.spec_from_loader(loader.name, loader)
h = importlib.util.module_from_spec(spec)
loader.exec_module(h)


class HotplugTests(unittest.TestCase):
    def test_debounce_and_no_repeat(self):
        state = h.Debounce()
        token = ('5-1', '5', '4')
        self.assertFalse(state.due(token, 0))
        self.assertFalse(state.due(token, 5))
        self.assertTrue(state.due(token, 6))
        state.completed = token
        self.assertFalse(state.due(token, 100))
        self.assertFalse(state.due(None, 101))
        self.assertFalse(state.due(('5-1', '5', '5'), 102))
        self.assertTrue(state.due(('5-1', '5', '5'), 108))

    def test_retry_backoff(self):
        state = h.Debounce()
        token = ('5-1', '5', '4')
        state.due(token, 0)
        state.retry_at = 70
        self.assertFalse(state.due(token, 60))
        self.assertTrue(state.due(token, 70))

    def test_reauthorization_with_same_usb_number(self):
        token = ('5-1', '5', '4')
        state = h.Debounce(token)
        self.assertFalse(state.due(token, 0))
        self.assertFalse(state.due(None, 2))
        self.assertFalse(state.due(token, 4))
        self.assertTrue(state.due(token, 10))

    def test_driver_readiness_and_dynamic_interface(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            p = root / '5-1'
            p.mkdir()
            for key, value in dict(idVendor='2ca3', idProduct='4006', busnum='5', devnum='4').items():
                (p / key).write_text(value)
            self.assertIsNone(h.generation(root))
            for n in range(9):
                interface = p / f'5-1:1.{n}'
                interface.mkdir()
                if n == 5:
                    continue
                name = 'option' if n < 4 else 'qmi_wwan' if n == 4 else 'snd-usb-audio'
                driver = root / name
                driver.mkdir(exist_ok=True)
                (interface / 'driver').symlink_to(driver)
            net = p / '5-1:1.4' / 'net' / 'wwan9'
            net.mkdir(parents=True)
            self.assertEqual(h.generation(root), ('5-1', '5', '4'))
            (p / '5-1:1.5' / 'driver').symlink_to(root / 'option')
            self.assertIsNone(h.generation(root))

    @patch.object(h, 'run')
    @patch.object(h, 'generation', return_value=('5-1', '5', '4'))
    def test_manually_stopped_container_not_started(self, generation, run):
        run.return_value = 'false'
        with self.assertRaises(RuntimeError):
            h.recover(('5-1', '5', '4'))
        self.assertEqual(run.call_count, 1)

    @patch.object(h, 'run')
    @patch.object(h, 'generation', return_value=None)
    def test_disappearance_does_not_restart_services(self, generation, run):
        with self.assertRaises(RuntimeError):
            h.recover(('5-1', '5', '4'))
        run.assert_not_called()

    @patch.object(h, 'RESET_MARKER')
    @patch.object(h, 'verify_adb', return_value='old-boot')
    @patch.object(h, 'run')
    def test_failed_reset_budget_is_not_reissued(self, run, verify, marker):
        marker.open.side_effect = FileExistsError()
        with self.assertRaises(FileExistsError):
            h.reset_once(('5-1', '5', '4'))
        run.assert_not_called()

    @patch.object(h, 'RESET_MARKER')
    @patch.object(h, 'check_qmi')
    @patch.object(h, 'verify_adb')
    @patch.object(h, 'run', return_value='true')
    @patch.object(h, 'generation', return_value=('5-1', '5', '4'))
    def test_probe_runs_while_container_stopped(self, generation, run, verify, probe, marker):
        def check(token):
            self.assertEqual(run.call_args.args[:2], ('docker', 'stop'))
        probe.side_effect = check
        self.assertEqual(h.recover(('5-1', '5', '4')), ('5-1', '5', '4'))
        self.assertIn(('docker', 'start', h.CONTAINER), [c.args for c in run.call_args_list])

    @patch.object(h, 'reset_once', side_effect=RuntimeError('reset failed'))
    @patch.object(h, 'check_qmi', side_effect=RuntimeError('QMI failed'))
    @patch.object(h, 'verify_adb')
    @patch.object(h, 'run', return_value='true')
    @patch.object(h, 'generation', return_value=('5-1', '5', '4'))
    def test_failure_still_restarts_container(self, generation, run, verify, probe, reset):
        with self.assertRaises(RuntimeError):
            h.recover(('5-1', '5', '4'))
        self.assertEqual(run.call_args.args, ('docker', 'start', h.CONTAINER))


if __name__ == '__main__':
    unittest.main()
