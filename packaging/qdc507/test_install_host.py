import os
from pathlib import Path
import subprocess
import tempfile
import unittest


class InstallHostTests(unittest.TestCase):
    def test_staged_install_and_upgrade_preserve_site_data(self):
        script = Path(__file__).with_name('install-host.sh')
        with tempfile.TemporaryDirectory() as tmp:
            stage = Path(tmp)
            root = stage / 'opt/docker/hideck'
            (root / 'data').mkdir(parents=True)
            (root / 'data/keep').write_text('unchanged')
            dropin = stage / 'etc/systemd/system/hideck-qdc507-audio.service.d/local.conf'
            dropin.parent.mkdir(parents=True)
            dropin.write_text('[Service]\nEnvironment=SITE_SETTING=keep\n')
            old = stage / 'usr/local/sbin/hideck-qdc507-hotplug'
            old.parent.mkdir(parents=True)
            old.write_text('old script')
            env = dict(os.environ, DESTDIR=tmp)
            for attempt in range(2):
                subprocess.run(['bash', str(script)], env=env, check=True, capture_output=True)
                self.assertEqual(os.readlink(old), '/opt/docker/hideck/host/bin/hideck-qdc507-hotplug')
                self.assertTrue((root / 'host/bin/hideck-qdc507-hotplug').is_file())
                self.assertEqual((root / 'data/keep').read_text(), 'unchanged')
                self.assertIn('SITE_SETTING=keep', dropin.read_text())
            backups = list((root / 'archive').glob('host-install.*'))
            self.assertEqual(len(backups), 2)
            self.assertTrue(any((b / 'usr/local/sbin/hideck-qdc507-hotplug').is_file()
                                and not (b / 'usr/local/sbin/hideck-qdc507-hotplug').is_symlink()
                                and (b / 'usr/local/sbin/hideck-qdc507-hotplug').read_text() == 'old script'
                                for b in backups))
            for name in ['hideck-qdc507-usb.service', 'hideck-qdc507-audio.service']:
                self.assertEqual(os.readlink(stage / 'etc/systemd/system' / name),
                                 '/opt/docker/hideck/host/systemd/' + name)
            self.assertTrue((root / 'docs/packaging/qdc507/DEPLOYMENT.md').exists())
            self.assertIn('/opt/docker/hideck', (root / 'deploy-qdc507-host.sh').read_text())


if __name__ == '__main__':
    unittest.main()
