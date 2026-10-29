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
        self.private(self.backups/'minecraft-paper-2026-01-01-0000.tar.gz','legacy evidence')
        self.tool(self.bin/'mountpoint','#!/usr/bin/bash\nexit 0\n')
        self.tool(self.helpers/'validate-instance-runtime-commit','#!/usr/bin/bash\nexit 0\n')
        self.tool(self.helpers/'instance-runtime-prerequisites','#!/usr/bin/bash\nexit 0\n')
        # Only this common mount identity primitive is mocked. The production
        # backup mount validation and read/write-target logic stay unchanged.
        self.private(self.helpers/'common.sh', 'JV_CONFIG=/nonexistent-fixture-config\njv_exact_mount_identity() { printf "%s\\n" "${FIXTURE_MOUNT_ID:-expected}"; }\n')
        common = (REPO/'mjust/libexec/backup-common.sh').read_text().replace('/etc/justvoxel/instances',str(self.configs)).replace('/etc/justvoxel/minecraft-backup.env',str(self.root/'backup.env'))
        (self.helpers/'backup-common.sh').write_text(common)
        # jq is used only for trusted source arguments; deterministic mock avoids
        # installing dependencies or querying any actual appliance config.
        self.tool(self.bin/'jq', '''#!/usr/bin/env python3
import json,sys
r=json.load(open(sys.argv[-1]));s=r['storage'];c=r['runtime'];query=sys.argv[-2]
if query == 'has("backups") | tostring':
    print("true" if "backups" in r else "false");sys.exit(0)
if '.backups | keys' in query:
    b=r.get('backups')
    valid=isinstance(b,dict) and set(b)=={'type','root','path','mount_point','uuid','source'} and all(isinstance(v,str) for v in b.values()) and b['path']==b['root']+'/'+c['id']
    sys.exit(0 if valid else 1)
if '.backups.type' in query:
    b=r['backups']
    for value in (b['type'],b['root'],b['mount_point'],b['uuid'],b['source'],s['data_path']):print(value)
else:
    for value in (c['id'],s['root'],s['data_path'],s['mount_point'],s['uuid'],s['source'],c['uid'],c['gid']):print(value)
''')
        self.source = (REPO/'mjust/libexec/instance-backup').read_text()
        self.source = self.source.replace('/usr/libexec/justvoxel/mjust/',str(self.helpers)+'/')
        self.source = self.source.replace('/etc/justvoxel/instances',str(self.configs))
        self.source = self.source.replace('/var/lib/justvoxel/instances',str(self.registry))
        self.source = self.source.replace('/etc/justvoxel/minecraft-backup.env',str(self.root/'backup.env'))
        self.config()
        (self.registry/'names').mkdir(mode=0o700)
        for identity in ('jv-aaaaaa','jv-bbbbbb'):
            directory=self.configs/identity;directory.mkdir(mode=0o700)
            data=self.root/'storage/minecraft-instances'/identity/'data';data.mkdir(parents=True)
            (data/'level.dat').write_bytes((identity+' world').encode())
            self.private(self.registry/(identity+'.json'),json.dumps({'id':identity}))
            self.private(self.registry/'names'/(identity+'.json'),json.dumps({'id':identity,'display_name':'Paper1' if identity == 'jv-aaaaaa' else 'Other Server'}))
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

    def pin_backup(self, identity, root, source=''):
        root.mkdir(mode=0o700,exist_ok=True)
        path=self.configs/identity/'config.json'
        r=json.loads(path.read_text())
        r['backups']=dict(type='nfs' if source else 'system',root=str(root),path=str(root/identity),mount_point=str(root) if source else '',uuid='',source=source)
        self.private(path,json.dumps(r))

    def test_physical_names_rename_collision_and_historical_retention(self):
        self.run_helper('create')
        first=self.inventory()[0]
        self.assertTrue(first['archive'].startswith('Paper1-backup-paper-'))
        self.assertEqual(first['name'],'Paper1')
        self.assertTrue((self.backups/'jv-aaaaaa'/first['archive']).is_file())
        self.run_helper('create')
        self.assertEqual(len({m['archive'] for m in self.inventory()}),2)
        self.private(self.registry/'names/jv-aaaaaa.json',json.dumps({'id':'jv-aaaaaa','display_name':"家族's Server"}))
        self.run_helper('create')
        items=self.inventory()
        self.assertTrue(any(m['archive'].startswith("家族's Server-backup-paper-") for m in items))
        self.assertTrue(any(m['archive']==first['archive'] and m['name']=='Paper1' for m in items))
        # Historical archives and metadata retain their original physical names.
        folder=self.backups/'jv-aaaaaa'
        old='instance-backup-paper-2000-01-01-0000.tar.gz'
        (folder/first['archive']).rename(folder/old)
        (folder/(first['archive']+'.meta.json')).unlink()
        first.update(archive=old,created='2000-01-01T00:00:00Z')
        self.private(folder/(old+'.meta.json'),json.dumps(first))
        self.assertTrue(any(m['archive']==old for m in self.inventory()))
        self.run_helper('retain')
        self.assertFalse((folder/old).exists())
        self.assertFalse((folder/(old+'.meta.json')).exists())

    def delete_metadata(self, ref):
        source=(REPO/'mjust/libexec/admin-backup-delete-common.sh').read_text()
        function=source[source.index('backup_delete_instance_metadata() {'):source.index('backup_delete_archive_path() {')]
        function=function.replace('/etc/justvoxel/instances',str(self.configs))
        return subprocess.run(['bash','-c',function+'\nbackup_delete_instance_metadata "$1" "$2"','fixture',str(self.backups),ref],capture_output=True).returncode == 0

    def test_delete_requires_owned_metadata_and_preserves_restore_selection(self):
        self.run_helper('create')
        item=self.inventory()[0]
        folder=self.backups/'jv-aaaaaa'
        archive=folder/item['archive']
        meta=folder/(item['archive']+'.meta.json')
        ref='jv-aaaaaa/'+item['archive']
        self.assertTrue(self.delete_metadata(ref))
        damaged=dict(item,id='jv-bbbbbb');self.private(meta,json.dumps(damaged))
        self.assertFalse(self.delete_metadata(ref))
        self.private(meta,json.dumps(item))
        meta.unlink();meta.symlink_to(self.configs/'jv-aaaaaa/config.json')
        self.assertFalse(self.delete_metadata(ref))
        meta.unlink();self.private(meta,json.dumps(item))
        archive.rename(folder/'saved');archive.symlink_to(folder/'saved')
        self.assertFalse(self.delete_metadata(ref))
        archive.unlink();(folder/'saved').rename(archive)
        journal=dict(schema=1,id='jv-aaaaaa',archive=item['archive'],stage='recovery_required')
        self.private(self.configs/'jv-aaaaaa/restore-journal.json',json.dumps(journal))
        self.assertFalse(self.delete_metadata(ref))
        journal['stage']='rolled_back'
        self.private(self.configs/'jv-aaaaaa/restore-journal.json',json.dumps(journal))
        self.assertTrue(self.delete_metadata(ref))
        self.assertTrue(archive.exists());self.assertTrue(meta.exists())
        old='instance-backup-paper-2026-10-28-0831.tar.gz'
        archive.rename(folder/old);meta.unlink();item['archive']=old
        self.private(folder/(old+'.meta.json'),json.dumps(item))
        self.assertTrue(self.delete_metadata('jv-aaaaaa/'+old))

    def test_java_server_backup_identity_and_loader_pin(self):
        path=self.configs/'jv-aaaaaa/config.json'
        original=json.loads(path.read_text())
        for software in ('spigot','forge','neoforge','fabric','quilt'):
            with self.subTest(software=software):
                record=json.loads(json.dumps(original))
                record['runtime']['software']=software
                if software != 'spigot': record['runtime']['loader_version']='0.16.10'
                self.private(path,json.dumps(record))
                self.run_helper('create')
                items=[m for m in self.inventory() if m['software']==software]
                self.assertEqual(len(items),1)
                if software == 'spigot': self.assertNotIn('loader_version',items[0])
                else: self.assertEqual(items[0]['loader_version'],'0.16.10')
                # A sibling instance still has an independent archive namespace.
                self.assertEqual(self.inventory('jv-bbbbbb'),[])
                if software != 'spigot':
                    meta=self.backups/'jv-aaaaaa'/(items[0]['archive']+'.meta.json')
                    damaged=dict(items[0]);del damaged['loader_version']
                    self.private(meta,json.dumps(damaged))
                    self.assertFalse(any(m['archive']==items[0]['archive'] for m in self.inventory()))

    def test_persisted_backup_destinations_ignore_global_defaults(self):
        selected=self.root/'selected-backups'
        self.pin_backup('jv-aaaaaa',selected)
        self.run_helper('create')
        self.assertEqual(len(list((selected/'jv-aaaaaa').glob('*.tar.gz'))),1)
        self.assertFalse((self.backups/'jv-aaaaaa').exists())
        self.config(network=True)
        self.env['FIXTURE_MOUNT_ID']='unavailable-global-default'
        self.assertEqual(len(self.inventory()),1)
        self.run_helper('create','jv-bbbbbb',ok=False)

    def test_explicit_backup_identity_fails_closed(self):
        selected=self.root/'selected-backups'
        self.pin_backup('jv-aaaaaa',selected,'nas:/instance-a')
        self.env['FIXTURE_MOUNT_ID']='nas:/instance-b'
        self.run_helper('create',ok=False)
        self.assertFalse((selected/'jv-aaaaaa').exists())

    def test_explicit_backup_namespace_cannot_select_sibling(self):
        selected=self.root/'selected-backups'
        self.pin_backup('jv-aaaaaa',selected)
        path=self.configs/'jv-aaaaaa/config.json'
        r=json.loads(path.read_text());r['backups']['path']=str(selected/'jv-bbbbbb')
        self.private(path,json.dumps(r))
        self.run_helper('inventory',ok=False)

    def test_read_only_and_independent_archives_retention(self):
        self.assertFalse((self.registry/'minecraft.json').exists())
        self.assertNotIn('minecraft.json', self.source)
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
