#!/usr/bin/bash
# Isolated source fixtures only. No real Minecraft, systemd, mounts or network.
set -euo pipefail
export JV_BACKUP_TEST_REPO="$(cd -- "$(dirname -- "$0")/.." && pwd)"
python3 - <<'PY'
import json, os, shlex, subprocess, tarfile, tempfile, unittest
from pathlib import Path

REPO = Path(os.environ['JV_BACKUP_TEST_REPO'])

class InstanceBackups(unittest.TestCase):
    def setUp(self):
        if os.geteuid() != 0:
            self.skipTest('private production configuration fixtures require root ownership')
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.configs = self.root / 'configs'
        self.registry = self.root / 'registry'
        self.backups = self.root / 'backups'
        self.helpers = self.root / 'helpers'
        self.bin = self.root / 'bin'
        for path in (self.configs,self.registry,self.backups,self.helpers,self.bin):
            path.mkdir(mode=0o700)
        self.env = dict(os.environ, PATH=str(self.bin)+':'+os.environ['PATH'])
        self.private(self.registry/'minecraft.json',json.dumps({'id':'jv-legacy0'}))
        self.private(self.backups/'minecraft-paper-2026-01-01-0000.tar.gz','legacy evidence')
        self.tool(self.bin/'mountpoint','#!/usr/bin/bash\nexit 0\n')
        self.tool(self.helpers/'validate-instance-runtime-commit','#!/usr/bin/bash\nexit 0\n')
        self.tool(self.helpers/'instance-runtime-prerequisites','#!/usr/bin/bash\nexit 0\n')
        # Only this common mount identity primitive is mocked. The production
        # backup mount validation and read/write-target logic stay unchanged.
        self.private(self.helpers/'common.sh', 'JV_CONFIG=/nonexistent-fixture-config\njv_exact_mount_identity() { printf "%s\\n" "${FIXTURE_MOUNT_ID:-expected}"; }\n')
        (self.helpers/'backup-common.sh').write_text((REPO/'mjust/libexec/backup-common.sh').read_text())
        # jq is used only for trusted source arguments; deterministic mock avoids
        # installing dependencies or querying any actual appliance config.
        self.tool(self.bin/'jq', '''#!/usr/bin/env python3
import json,sys
r=json.load(open(sys.argv[-1]));s=r['storage'];c=r['runtime']
for value in (c['id'],s['root'],s['data_path'],s['mount_point'],s['uuid'],s['source'],c['uid'],c['gid']):print(value)
''')
        self.source = (REPO/'mjust/libexec/instance-backup').read_text()
        self.source = self.source.replace('/usr/libexec/justvoxel/mjust/',str(self.helpers)+'/')
        self.source = self.source.replace('/etc/justvoxel/instances',str(self.configs))
        self.source = self.source.replace('/var/lib/justvoxel/instances',str(self.registry))
        self.source = self.source.replace('/etc/justvoxel/minecraft-backup.env',str(self.root/'backup.env'))
        self.config()
        for identity in ('jv-aaaaaa','jv-bbbbbb'):
            directory=self.configs/identity;directory.mkdir(mode=0o700)
            data=self.root/'storage/minecraft-instances'/identity/'data';data.mkdir(parents=True)
            (data/'level.dat').write_bytes((identity+' world').encode())
            self.private(self.registry/(identity+'.json'),json.dumps({'id':identity}))
            self.private(directory/'config.json',json.dumps(dict(runtime=dict(id=identity,uid=1000,gid=1000,software='paper',version='1.21.4',rcon_password='private-runtime-secret'),storage=dict(root=str(self.root/'storage'),data_path=str(data),mount_point='',uuid='',source=''))))
            self.private(directory/'backup-settings.json',json.dumps(dict(enabled=False,time='04:00',retention=2)))

    def private(self,path,text):
        path.write_text(text);path.chmod(0o600)

    def tool(self,path,text):
        path.write_text(text);path.chmod(0o700)

    def config(self,network=False):
        fields=dict(MINECRAFT_DATA_PATH=str(self.root/'legacy-data'),MINECRAFT_BACKUP_PATH=str(self.backups),BACKUP_TYPE='nfs' if network else 'system',BACKUP_KEEP='7',BACKUP_MOUNT_POINT=str(self.backups) if network else '',BACKUP_EXPECTED_SOURCE='expected' if network else '',BACKUP_EXPECTED_UUID='')
        self.private(self.root/'backup.env',''.join(k+'='+shlex.quote(v)+'\n' for k,v in fields.items()))

    def run_helper(self,action,identity='jv-aaaaaa',source=None,ok=True):
        helper=self.helpers/'worker';self.tool(helper,source or self.source)
        result=subprocess.run(['bash',str(helper),action,identity],env=self.env,capture_output=True,text=True,timeout=20)
        self.assertEqual(result.returncode==0,ok,result.stdout+result.stderr)
        self.assertNotIn('private-runtime-secret',result.stdout+result.stderr)
        return result

    def inventory(self,identity='jv-aaaaaa'):
        return json.loads(self.run_helper('inventory',identity).stdout)

    def test_read_only_and_independent_archives_retention(self):
        self.assertEqual(self.inventory(),[])
        self.assertFalse((self.backups/'jv-aaaaaa').exists())
        for identity in ('jv-aaaaaa','jv-bbbbbb'):
            self.run_helper('preflight',identity)
            for _ in range(3):self.run_helper('create',identity)
        other=sorted(p.name for p in (self.backups/'jv-bbbbbb').iterdir())
        folder=self.backups/'jv-aaaaaa'
        (folder/'unrecognized.tar.gz').write_bytes(b'preserve')
        (folder/'.instance-backup-stale').write_bytes(b'preserve interrupted evidence')
        (folder/'instance-backup-paper-2000-01-01-0000.tar.gz').write_bytes(b'no metadata')
        self.run_helper('retain')
        items=self.inventory();self.assertEqual(len(items),2)
        self.assertEqual(sorted(p.name for p in (self.backups/'jv-bbbbbb').iterdir()),other)
        self.assertEqual((self.backups/'minecraft-paper-2026-01-01-0000.tar.gz').read_text(),'legacy evidence')
        self.assertTrue((folder/'.instance-backup-stale').exists())
        self.assertTrue((folder/'unrecognized.tar.gz').exists())
        self.assertTrue((folder/'instance-backup-paper-2000-01-01-0000.tar.gz').exists())
        for item in items:
            self.assertEqual(item['id'],'jv-aaaaaa')
            self.assertEqual(len(item['sha256']),64)
            with tarfile.open(folder/item['archive'],'r:gz') as archive:
                self.assertEqual(set(archive.getnames()),{'data','data/level.dat'})
                self.assertEqual(archive.extractfile('data/level.dat').read(),b'jv-aaaaaa world')
            self.assertNotIn('private-runtime-secret',json.dumps(item))
        # Existing legacy discovery's unchanged glob excludes all new archives.
        self.assertEqual(list(folder.glob('minecraft-*.tar.gz')),[])

    def test_mount_capacity_and_integrity_fail_before_publication(self):
        self.config(network=True)
        self.tool(self.bin/'mountpoint','#!/usr/bin/bash\nexit 1\n')
        self.run_helper('preflight',ok=False)
        self.tool(self.bin/'mountpoint','#!/usr/bin/bash\nexit 0\n')
        self.env['FIXTURE_MOUNT_ID']='changed'
        self.run_helper('preflight',ok=False)
        self.assertFalse((self.backups/'jv-aaaaaa').exists())
        self.config();self.env.pop('FIXTURE_MOUNT_ID')
        low_space=self.source.replace('space = os.fstatvfs(fd)',"space = type('Space',(),dict(f_bavail=0,f_frsize=4096))()")
        self.run_helper('preflight',source=low_space,ok=False)
        self.assertEqual(self.inventory(),[])
        self.run_helper('create')
        before={p.name:p.read_bytes() for p in (self.backups/'jv-aaaaaa').iterdir()}
        corrupt=self.source.replace("with tarfile.open(tmp,'r:gz') as archive:","raise ValueError('injected failed integrity check')\n                    with tarfile.open(tmp,'r:gz') as archive:")
        self.run_helper('create',source=corrupt,ok=False)
        self.assertEqual({p.name:p.read_bytes() for p in (self.backups/'jv-aaaaaa').iterdir()},before)
        interrupted=self.source.replace("archive.add(source,arcname='data',filter=safe_member)","raise KeyboardInterrupt('fixture interruption')")
        self.run_helper('create',source=interrupted,ok=False)
        self.assertEqual({p.name:p.read_bytes() for p in (self.backups/'jv-aaaaaa').iterdir()},before)

    def test_symlinks_partial_metadata_and_foreign_archive_not_owned(self):
        self.run_helper('create');folder=self.backups/'jv-aaaaaa';item=self.inventory()[0]
        meta=folder/(item['archive']+'.meta.json')
        item['id']='jv-bbbbbb';meta.write_text(json.dumps(item))
        self.assertEqual(self.inventory(),[])
        self.run_helper('retain');self.assertTrue((folder/item['archive']).exists())
        meta.write_text('{"partial":')
        self.assertEqual(self.inventory(),[])
        source=self.root/'storage/minecraft-instances/jv-aaaaaa/data'
        (source/'escape').symlink_to(self.root/'backup.env')
        self.run_helper('preflight',ok=False);(source/'escape').unlink()
        for path in folder.iterdir():path.unlink()
        folder.rmdir();folder.symlink_to(self.backups/'jv-bbbbbb')
        self.run_helper('inventory',ok=False)
        self.run_helper('create','../../escape',ok=False)

    def test_source_validation_failure_and_publication_failure_preserve_world(self):
        self.run_helper('create');folder=self.backups/'jv-aaaaaa'
        before={p.name:p.read_bytes() for p in folder.iterdir()}
        self.tool(self.helpers/'instance-runtime-prerequisites','#!/usr/bin/bash\nexit 1\n')
        self.run_helper('preflight',ok=False)
        self.tool(self.helpers/'instance-runtime-prerequisites','#!/usr/bin/bash\nexit 0\n')
        failure=self.source.replace('os.link(tmp,filename,',"(_ for _ in ()).throw(OSError('injected publication failure'))\n                    os.link(tmp,filename,")
        self.run_helper('create',source=failure,ok=False)
        self.assertEqual({p.name:p.read_bytes() for p in folder.iterdir()},before)
        self.assertEqual((self.root/'storage/minecraft-instances/jv-aaaaaa/data/level.dat').read_bytes(),b'jv-aaaaaa world')

unittest.main()
PY
