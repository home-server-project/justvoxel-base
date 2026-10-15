#!/usr/bin/bash
# Temporary files and mocked appliance commands only. No host services/storage.
set -euo pipefail
export JV_RESTORE_TEST_REPO="$(cd -- "$(dirname -- "$0")/.." && pwd)"
python3 - <<'PY'
import os, selectors, shutil, subprocess, tempfile, unittest
from pathlib import Path

REPO = Path(os.environ['JV_RESTORE_TEST_REPO'])

class LegacyRestoreExclusion(unittest.TestCase):
    def setUp(self):
        if os.geteuid() != 0:
            self.skipTest('legacy worker requires root; use an authorized rootless user namespace')
        self.tmp = tempfile.TemporaryDirectory();self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.bin = self.root/'bin';self.bin.mkdir()
        self.data = self.root/'data';self.data.mkdir();(self.data/'level.dat').write_bytes(b'legacy original')
        self.backups = self.root/'backups';self.backups.mkdir()
        self.maintenance = self.root/'maintenance.lock'
        self.exclusion = self.root/'exclusion.lock'
        self.trace = self.root/'trace'
        helper = self.root/'mock-common'
        helper.write_text('''jv_backup_load_config() {
    echo config >> "$TRACE"
    minecraft_data_path="$DATA"; minecraft_backup_path="$BACKUPS"
    BACKUP_KEEP=7; MINECRAFT_SERVICE=minecraft.service
}
jv_backup_prepare_write_target() { :; }
jv_backup_prepare_instance_directory() { return 2; }
jv_archive_server_label() { echo minecraft-paper; }
server_supports_plugins() { return 1; }
jv_minecraft_still_running() { return 1; }
jv_backup_write_metadata() { :; }
jv_stop_minecraft_adaptive() { echo stop >> "$TRACE"; }
''')
        source = (REPO/'runtime/minecraft-backup').read_text()
        for name in ('backup-common.sh','backup-instance-storage.sh','interrupt-safety.sh'):
            source = source.replace('/usr/libexec/justvoxel/mjust/'+name,str(helper))
        source = source.replace('/run/justvoxel-minecraft-maintenance.lock',str(self.maintenance))
        source = source.replace('/run/justvoxel-minecraft-restore-exclusion.lock',str(self.exclusion))
        source = source.replace('/var/lib/justvoxel/management/setup-in-progress',str(self.root/'setup'))
        self.worker = self.root/'worker';self.worker.write_text(source)
        self.tool('systemctl','echo systemctl >> "$TRACE"\nexit 1\n')
        self.real_tar = shutil.which('tar')
        self.tool('tar','if [[ ${HOLD_BACKUP:-0} == 1 && $1 == -C ]]; then echo ready; read -r release; fi\nexec "'+self.real_tar+'" "$@"\n')
        self.env = dict(os.environ,PATH=str(self.bin)+':'+os.environ['PATH'],TRACE=str(self.trace),DATA=str(self.data),BACKUPS=str(self.backups))

    def tool(self,name,source):
        path=self.bin/name;path.write_text('#!/usr/bin/bash\n'+source);path.chmod(0o700)

    def probe(self,path):
        return subprocess.run(['flock','-n',str(path),'true'],timeout=3).returncode == 0

    def holder(self,paths):
        script='exec 8>"$1"; chmod 600 "$1"; flock -n 8 || exit 1; '
        if len(paths)>1:script+='exec 9>"$2"; flock -n 9 || exit 1; '
        script+='echo ready; read -r release'
        p=subprocess.Popen(['bash','-c',script,'holder',*map(str,paths)],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True)
        self.addCleanup(self.finish,p)
        self.wait_ready(p)
        return p

    def wait_ready(self,p):
        # Select unbuffered fd reads: ordinary TextIO may buffer a later ready
        # line and hide it from select. Every wait is bounded.
        with selectors.DefaultSelector() as selector:
            selector.register(p.stdout,selectors.EVENT_READ)
            pending=b''
            while b'ready\n' not in pending:
                self.assertTrue(selector.select(5),'worker did not reach lock checkpoint')
                block=os.read(p.stdout.fileno(),4096)
                self.assertTrue(block,'worker exited before lock checkpoint')
                pending+=block

    def finish(self,p):
        try:
            if p.poll() is None:
                p.communicate('release\n',timeout=5)
        except subprocess.TimeoutExpired:
            p.kill()
            p.communicate()
            raise
        finally:
            for stream in (p.stdin,p.stdout,p.stderr):
                if stream is not None:
                    stream.close()

    def run_worker(self,**extra):
        return subprocess.run(['bash',str(self.worker)],env=dict(self.env,**extra),capture_output=True,text=True,timeout=5)

    def test_normal_backup_and_completion_release(self):
        result=self.run_worker()
        self.assertEqual(result.returncode,0,result.stdout+result.stderr)
        self.assertEqual(len(list(self.backups.glob('minecraft-paper-*.tar.gz'))),1)
        self.assertTrue(self.probe(self.exclusion));self.assertTrue(self.probe(self.maintenance))
        self.assertEqual((self.data/'level.dat').read_bytes(),b'legacy original')

    def test_restore_exclusion_refuses_legacy_before_any_backup_work(self):
        owner=self.holder([self.exclusion])
        for inherited in ('0','1'):
            result=self.run_worker(JUSTVOXEL_MAINTENANCE_LOCK_HELD=inherited)
            self.assertNotEqual(result.returncode,0)
            self.assertIn('Restore operation',result.stderr)
            self.assertFalse(self.trace.exists());self.assertEqual(list(self.backups.iterdir()),[])
        self.finish(owner)
        self.assertTrue(self.probe(self.exclusion))

    def test_running_legacy_owns_exclusion_until_archive_finishes(self):
        worker=subprocess.Popen(['bash',str(self.worker)],env=dict(self.env,HOLD_BACKUP='1'),stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True)
        self.addCleanup(self.finish,worker);self.wait_ready(worker)
        self.assertFalse(self.probe(self.exclusion),'Restore admitted while legacy backup was active')
        self.assertFalse(self.probe(self.maintenance))
        self.finish(worker);self.assertEqual(worker.returncode,0)
        self.assertTrue(self.probe(self.exclusion));self.assertTrue(self.probe(self.maintenance))

    def test_handoff_keeps_exclusion_while_startup_acquires_maintenance(self):
        restore=self.holder([self.exclusion])
        startup=self.holder([self.maintenance]) # actual separate ExecStartPre process
        self.assertFalse(self.probe(self.maintenance))
        result=self.run_worker()
        self.assertNotEqual(result.returncode,0);self.assertFalse(self.trace.exists())
        self.finish(startup)
        self.assertTrue(self.probe(self.maintenance));self.assertFalse(self.probe(self.exclusion))
        self.assertNotEqual(self.run_worker().returncode,0)
        self.finish(restore)
        self.assertEqual(self.run_worker().returncode,0)

    def test_no_circular_wait_with_existing_maintenance_owner(self):
        owner=self.holder([self.maintenance])
        # Legacy may obtain exclusion, but must immediately refuse maintenance
        # and release exclusion. It cannot wait for Restore or another caller.
        self.assertNotEqual(self.run_worker().returncode,0)
        self.assertTrue(self.probe(self.exclusion))
        self.finish(owner)

    def test_process_termination_releases_both_flocks(self):
        owner=self.holder([self.exclusion,self.maintenance])
        owner.kill();owner.wait(timeout=5)
        self.assertTrue(self.probe(self.exclusion));self.assertTrue(self.probe(self.maintenance))

unittest.main()
PY
