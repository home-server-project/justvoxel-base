#!/usr/bin/bash
# Isolated real-file fixtures; also runnable with podman unshare by a human.
# No real Minecraft, root system files, network mounts, or service mutations.
set -euo pipefail
export JV_BACKUP_TEST_REPO="$(cd -- "$(dirname -- "$0")/.." && pwd)"
python3 - <<'PY'
import gzip, hashlib, io, json, os, struct, subprocess, tarfile, unittest
from pathlib import Path

REPO = Path(os.environ['JV_BACKUP_TEST_REPO'])
# Reuse the Phase 4A fixture's actual metadata writer and mount validation.
text = (REPO/'tests/test-instance-backups.sh').read_text().split("python3 - <<'PY'\n",1)[1].rsplit('\nPY',1)[0]
namespace = {'__name__':'backup_fixtures'}
exec(compile(text.replace('unittest.main()',''),'<backup fixtures>','exec'),namespace)
Base = namespace['InstanceBackups']

def world_metadata(version='1.21.4', data_version=4189):
    def name(value):
        raw=value.encode();return struct.pack('>H',len(raw))+raw
    raw=b'\x0a'+name('')+b'\x0a'+name('Data')
    raw+=b'\x03'+name('DataVersion')+struct.pack('>i',data_version)
    raw+=b'\x0a'+name('Version')+b'\x03'+name('Id')+struct.pack('>i',data_version)
    raw+=b'\x08'+name('Name')+name(version)+b'\x01'+name('Snapshot')+b'\x00'
    return gzip.compress(raw+b'\x00\x00\x00', mtime=0)

class IndependentRestore(unittest.TestCase):
    private = Base.private
    tool = Base.tool
    config = Base.config
    run_helper = Base.run_helper
    inventory = Base.inventory
    pin_backup = Base.pin_backup

    def test_restore_uses_persisted_backup_namespace(self):
        selected=self.root/'selected-backups'
        self.pin_backup('jv-aaaaaa',selected)
        (self.data/'world/level.dat').write_bytes(world_metadata())
        self.run_helper('create')
        item=self.inventory()[0]
        self.archive=item['archive']
        self.config(network=True)
        self.env['FIXTURE_MOUNT_ID']='disconnected-global-default'
        result=self.plan()
        self.assertEqual(result['metadata']['id'],'jv-aaaaaa')
        self.assertTrue((selected/'jv-aaaaaa'/self.archive).exists())

    def setUp(self):
        Base.setUp(self)
        self.assertFalse((self.registry/'minecraft.json').exists())
        self.restore_source = (REPO/'mjust/libexec/instance-restore').read_text()
        self.assertNotIn('minecraft.json', self.restore_source)
        for old,new in [('/usr/libexec/justvoxel/mjust/',str(self.helpers)+'/'),('/etc/justvoxel/instances',str(self.configs)),('/var/lib/justvoxel/instances',str(self.registry)),('/etc/justvoxel/minecraft-backup.env',str(self.root/'backup.env'))]:
            self.restore_source=self.restore_source.replace(old,new)
        world=self.root/'storage/minecraft-instances/jv-aaaaaa/data/world'
        world.mkdir();(world/'level.dat').write_bytes(world_metadata())
        self.run_helper('create')
        self.item=self.inventory()[0]
        self.archive=self.item['archive']
        self.data=self.root/'storage/minecraft-instances/jv-aaaaaa/data'
        self.other=self.root/'storage/minecraft-instances/jv-bbbbbb/data/level.dat'
        (self.data/'level.dat').write_bytes(b'original at restore time')

    def restore(self,action,plan='',ok=True,identity='jv-aaaaaa',source=None,archive=None):
        helper=self.helpers/'restore-worker';self.tool(helper,source or self.restore_source)
        result=subprocess.run(['bash',str(helper),action,identity,archive or self.archive,plan,'false'],env=self.env,capture_output=True,text=True,timeout=30)
        self.assertEqual(result.returncode==0,ok,result.stdout+result.stderr)
        self.assertNotIn('private-runtime-secret',result.stdout+result.stderr)
        return result

    def plan(self,**kw): return json.loads(self.restore('plan',**kw).stdout)
    def journal(self): return json.loads((self.configs/'jv-aaaaaa/restore-journal.json').read_text())
    def tx(self): return self.data.parent/('.restore-'+self.journal()['transaction'])
    def prepare(self):
        plan=self.plan();self.restore('prepare',plan['fingerprint']);return plan
    def replace_archive(self,members=None,raw=None):
        folder=self.backups/'jv-aaaaaa'; path=folder/self.archive
        if raw is None:
            with tarfile.open(path,'w:gz') as tf:
                root=tarfile.TarInfo('data');root.type=tarfile.DIRTYPE;root.mode=0o750;tf.addfile(root)
                world=tarfile.TarInfo('data/world/level.dat');world.mode=0o640
                content=world_metadata();world.size=len(content);tf.addfile(world,io.BytesIO(content))
                for name,kind,value in members:
                    m=tarfile.TarInfo(name);m.mode=0o640
                    if kind=='file':
                        m.size=len(value);tf.addfile(m,io.BytesIO(value))
                    elif kind in ('symlink','hardlink'):
                        m.type=tarfile.SYMTYPE if kind=='symlink' else tarfile.LNKTYPE;m.linkname=value;tf.addfile(m)
                    elif kind=='fifo':
                        m.type=tarfile.FIFOTYPE;tf.addfile(m)
        else:path.write_bytes(raw)
        m=json.loads((folder/(self.archive+'.meta.json')).read_text())
        m['size']=path.stat().st_size;m['sha256']=hashlib.sha256(path.read_bytes()).hexdigest()
        self.private(folder/(self.archive+'.meta.json'),json.dumps(m))

    def bedrock_backup(self):
        import shutil
        shutil.rmtree(self.data/'world')
        (self.data/'level.dat').unlink()
        world=self.data/'worlds/Bedrock level';world.mkdir(parents=True)
        payload=b'\x0a\x00\x00\x00'
        (world/'level.dat').write_bytes(struct.pack('<II',10,len(payload))+payload)
        (world/'db').mkdir();(world/'db/CURRENT').write_bytes(b'world database')
        path=self.configs/'jv-aaaaaa/config.json';record=json.loads(path.read_text())
        record['runtime'].update(software='bedrock',version='1.26.50.1')
        self.private(path,json.dumps(record));self.run_helper('create')
        item=next(m for m in self.inventory() if m['software']=='bedrock')
        self.archive=item['archive']
        return world,path,record

    def test_bedrock_exact_same_instance_restore_and_rollback(self):
        world,path,record=self.bedrock_backup()
        original=(world/'db/CURRENT').read_bytes()
        (world/'db/CURRENT').write_bytes(b'newer original data')
        plan=self.prepare()
        self.assertEqual(plan['metadata']['software'],'bedrock')
        self.assertEqual(plan['metadata']['version'],'1.26.50.1')
        self.restore('shutdown');self.restore('activate')
        self.assertEqual((world/'db/CURRENT').read_bytes(),original)
        self.restore('rollback');self.restore('rollback-verifying');self.restore('rolled-back')
        self.assertEqual((world/'db/CURRENT').read_bytes(),b'newer original data')
        self.assertEqual(json.loads(path.read_text())['runtime']['id'],'jv-aaaaaa')

    def test_bedrock_restore_rejects_java_and_wrong_exact_version(self):
        world,path,record=self.bedrock_backup()
        for software,version in [('paper','1.26.50.1'),('bedrock','1.26.51.1')]:
            record['runtime'].update(software=software,version=version)
            self.private(path,json.dumps(record));self.restore('plan',ok=False)
        record['runtime'].update(software='bedrock',version='1.26.50.1')
        self.private(path,json.dumps(record))
        self.assertEqual(self.plan()['integrity'],'verified')
        # A Java archive can never become a Bedrock archive via destination selection.
        self.archive=self.item['archive'];self.restore('plan',ok=False)

    def test_mod_loader_pin_required_for_restore(self):
        config=self.configs/'jv-aaaaaa/config.json'
        record=json.loads(config.read_text())
        record['runtime']['software']='fabric'
        record['runtime']['loader_version']='0.16.10'
        self.private(config,json.dumps(record))
        self.run_helper('create')
        item=next(m for m in self.inventory() if m['software']=='fabric')
        self.archive=item['archive']
        self.assertEqual(self.plan()['metadata']['loader_version'],'0.16.10')
        record['runtime']['loader_version']='0.16.11'
        self.private(config,json.dumps(record))
        self.restore('plan',ok=False)
        self.assertEqual((self.data/'world/level.dat').read_bytes(),world_metadata())

    def test_same_instance_atomic_replacement_original_preserved_and_isolation(self):
        other=self.other.read_bytes();plan=self.prepare()
        self.assertEqual((self.data/'level.dat').read_bytes(),b'original at restore time')
        self.restore('shutdown');self.restore('activate')
        self.assertEqual((self.data/'level.dat').read_bytes(),b'jv-aaaaaa world')
        self.assertEqual((self.tx()/'original/level.dat').read_bytes(),b'original at restore time')
        self.restore('verifying');self.restore('completed')
        self.assertEqual(self.journal()['stage'],'completed')
        self.assertEqual(self.other.read_bytes(),other)
        self.assertEqual(list((self.backups/'jv-aaaaaa').glob('minecraft-*.tar.gz')),[])
        self.assertEqual((self.backups/'minecraft-paper-2026-01-01-0000.tar.gz').read_text(),'legacy evidence')

    def test_foreign_identity_and_software_or_version_refused(self):
        path=self.backups/'jv-aaaaaa'/(self.archive+'.meta.json')
        original=json.loads(path.read_text())
        for key,value in [('id','jv-bbbbbb'),('software','vanilla'),('version','1.20.1'),('version','LATEST')]:
            m=dict(original);m[key]=value;self.private(path,json.dumps(m))
            self.restore('plan',ok=False)
        self.private(path,json.dumps(original))
        self.restore('plan',identity='jv-bbbbbb',ok=False)
        self.restore('plan',archive='minecraft-paper-2026-01-01-0000.tar.gz',ok=False)

    def test_sha_truncation_and_malformed_tar(self):
        self.assertEqual(self.plan()['integrity'],'verified')
        path=self.backups/'jv-aaaaaa'/self.archive;original=path.read_bytes()
        path.write_bytes(original[:-4]+b'xxxx')
        self.restore('plan',ok=False) # Metadata SHA has not changed.
        self.replace_archive(raw=original[:-4]);self.restore('plan',ok=False) # SHA matches truncated gzip.
        self.replace_archive(raw=gzip.compress(b'not a tar archive'));self.restore('plan',ok=False)

    def test_paths_links_duplicates_conflicts_and_special_files(self):
        for entries in [[('../escape','file',b'x')],[('/data/escape','file',b'x')],[('data/../escape','file',b'x')],[('data/link','symlink','/etc/passwd')],[('data/fifo','fifo','')],[('data/link','hardlink','data/world/level.dat')],[('data/a','file',b'a'),('data/a','file',b'b')],[('data/a/b','file',b'a'),('data/a','file',b'b')],[('data/a','file',b'a'),('data/a/b','file',b'b')]]:
            with self.subTest(entries=entries):
                self.replace_archive(entries);self.restore('plan',ok=False)
        self.assertFalse((self.root/'escape').exists())

    def test_expansion_entry_and_capacity_bounds(self):
        self.replace_archive([('data/huge','file',b'x'*8192)])
        bounded=self.restore_source.replace('MAX_BYTES = 128 * 1024**3','MAX_BYTES = 1024')
        self.restore('plan',source=bounded,ok=False)
        bounded=self.restore_source.replace('MAX_ENTRIES = 250000','MAX_ENTRIES = 1')
        self.restore('plan',source=bounded,ok=False)
        low=self.restore_source.replace('available = os.fstatvfs(parent)',"available = type('Space',(),dict(f_bavail=0,f_frsize=4096,f_favail=1000))()")
        self.restore('plan',source=low,ok=False)
        self.assertFalse((self.configs/'jv-aaaaaa/restore-journal.json').exists())

    def test_stale_plan_archive_removed_and_preparation_failure_preserves_original(self):
        plan=self.plan();meta=self.backups/'jv-aaaaaa'/(self.archive+'.meta.json')
        m=json.loads(meta.read_text());m['name']='changed';self.private(meta,json.dumps(m))
        self.restore('prepare',plan['fingerprint'],ok=False)
        self.assertFalse((self.configs/'jv-aaaaaa/restore-journal.json').exists())
        plan=self.plan();(self.backups/'jv-aaaaaa'/self.archive).unlink()
        self.restore('prepare',plan['fingerprint'],ok=False)
        self.assertEqual((self.data/'level.dat').read_bytes(),b'original at restore time')

    def test_wrong_mount_and_unavailable_network_source(self):
        self.config(network=True)
        self.env['FIXTURE_MOUNT_ID']='foreign';self.restore('plan',ok=False)
        self.env['FIXTURE_MOUNT_ID']='expected'
        self.tool(self.bin/'mountpoint','#!/usr/bin/bash\nexit 1\n')
        self.restore('plan',ok=False)
        self.assertEqual((self.data/'level.dat').read_bytes(),b'original at restore time')

    def test_destination_mount_identity_using_real_prerequisite_helper(self):
        config=self.configs/'jv-aaaaaa/config.json'
        record=json.loads(config.read_text());storage=record['storage']
        storage['mount_point']=storage['root'];storage['uuid']='expected'
        self.private(config,json.dumps(record))
        common=self.helpers/'common.sh'
        common.write_text(common.read_text()+'\nrequire_root() { [[ $EUID == 0 ]]; }\n')
        source=(REPO/'mjust/libexec/instance-runtime-prerequisites').read_text()
        source=source.replace('/usr/libexec/justvoxel/mjust/',str(self.helpers)+'/')
        self.tool(self.helpers/'instance-runtime-prerequisites',source)
        self.data.parent.parent.chmod(0o700);self.data.parent.chmod(0o700)
        os.chown(self.data,1000,1000)
        self.env['FIXTURE_MOUNT_ID']='foreign';self.restore('plan',ok=False)
        self.env['FIXTURE_MOUNT_ID']='expected';self.assertEqual(self.plan()['integrity'],'verified')
        self.tool(self.bin/'mountpoint','#!/usr/bin/bash\nexit 1\n')
        self.restore('plan',ok=False)
        self.assertEqual((self.data/'level.dat').read_bytes(),b'original at restore time')

    def test_archive_and_metadata_ownership_modes_and_links(self):
        archive=self.backups/'jv-aaaaaa'/self.archive
        meta=self.backups/'jv-aaaaaa'/(self.archive+'.meta.json')
        for path in (archive,meta):
            path.chmod(0o644);self.restore('plan',ok=False);path.chmod(0o600)
        for path in (archive,meta):
            original=path.read_bytes();path.unlink()
            target=self.root/'foreign-file';target.write_bytes(original);target.chmod(0o600)
            path.symlink_to(target);self.restore('plan',ok=False);path.unlink()
            path.write_bytes(original);path.chmod(0o600)
            alias=self.root/'hardlink';os.link(path,alias)
            self.restore('plan',ok=False);alias.unlink()
        self.assertEqual(self.plan()['integrity'],'verified')

    def test_recovery_after_atomic_exchange_and_retry_without_backup_source(self):
        self.prepare();self.restore('shutdown')
        crash=self.restore_source.replace("stage(j,'replacement_activated')","raise OSError('injected interruption after atomic exchange')")
        self.restore('activate',source=crash,ok=False)
        self.assertEqual((self.data/'level.dat').read_bytes(),b'jv-aaaaaa world')
        # An interrupted restore cannot accept another prepare/plan.
        self.restore('plan',ok=False)
        (self.backups/'jv-aaaaaa'/self.archive).unlink()
        # Incomplete rollback preserves both original and failed replacement.
        broken=self.restore_source.replace("move(original_fd,original_name,parent,'data')","raise OSError('injected rollback interruption')")
        self.restore('rollback',source=broken,ok=False)
        self.assertTrue((self.tx()/'failed-replacement/level.dat').exists())
        self.restore('rollback');self.restore('rollback')
        self.assertEqual((self.data/'level.dat').read_bytes(),b'original at restore time')
        self.restore('rollback-verifying');self.restore('rolled-back')
        self.assertEqual(self.journal()['stage'],'rolled_back')
        self.assertTrue((self.tx()/'failed-replacement/level.dat').exists())

    def test_interrupted_preparation_has_recovery_even_before_transaction_mkdir(self):
        plan=self.plan()
        crash=self.restore_source.replace("os.mkdir(txname,0o700,dir_fd=parent)","raise OSError('injected early interruption')\n            os.mkdir(txname,0o700,dir_fd=parent)")
        self.restore('prepare',plan['fingerprint'],source=crash,ok=False)
        self.restore('rollback');self.restore('rollback-verifying');self.restore('rolled-back')
        self.assertEqual((self.data/'level.dat').read_bytes(),b'original at restore time')

    def test_complete_tar_end_markers_and_no_hidden_trailing_archive(self):
        self.replace_archive([('data/file','file',b'world')])
        path=self.backups/'jv-aaaaaa'/self.archive
        raw=gzip.decompress(path.read_bytes())
        with tarfile.open(fileobj=io.BytesIO(raw),mode='r:') as tf:
            members=tf.getmembers();end=members[-1].offset_data+((members[-1].size+511)//512)*512
        self.replace_archive(raw=gzip.compress(raw[:end]));self.restore('plan',ok=False)
        self.replace_archive(raw=gzip.compress(raw+b'hidden trailing data'));self.restore('plan',ok=False)

    def test_world_version_and_corrupt_world_metadata(self):
        path=self.backups/'jv-aaaaaa'/self.archive
        original=path.read_bytes()
        for value in (world_metadata('1.22.0'),gzip.compress(b'invalid NBT')):
            with tarfile.open(path,'w:gz') as tf:
                root=tarfile.TarInfo('data');root.type=tarfile.DIRTYPE;root.mode=0o750;tf.addfile(root)
                member=tarfile.TarInfo('data/world/level.dat');member.mode=0o640;member.size=len(value)
                tf.addfile(member,io.BytesIO(value))
            self.replace_archive(raw=path.read_bytes());self.restore('plan',ok=False)
        self.replace_archive(raw=original)
        self.assertEqual(self.plan()['integrity'],'verified')

    def test_retention_preserves_interrupted_restore_archive(self):
        meta=self.backups/'jv-aaaaaa'/(self.archive+'.meta.json')
        m=json.loads(meta.read_text());m['created']='2020-01-01T00:00:00Z';self.private(meta,json.dumps(m))
        self.prepare()
        for _ in range(3):self.run_helper('create')
        self.run_helper('retain') # Fixture startup validator is mocked for this defense-in-depth check.
        self.assertTrue((self.backups/'jv-aaaaaa'/self.archive).exists())
        self.assertTrue((self.backups/'jv-aaaaaa'/(self.archive+'.meta.json')).exists())
        self.restore('rollback');self.restore('rollback-verifying');self.restore('rolled-back')
        self.run_helper('retain')
        self.assertFalse((self.backups/'jv-aaaaaa'/self.archive).exists())

    def test_rollback_before_exchange_and_after_original_preservation(self):
        self.prepare();self.restore('shutdown')
        crash=self.restore_source.replace("stage(j,'preserving_original')","raise OSError('before exchange')")
        self.restore('activate',source=crash,ok=False)
        self.restore('rollback')
        self.assertEqual((self.data/'level.dat').read_bytes(),b'original at restore time')
        self.restore('rollback-verifying');self.restore('rolled-back')
        self.prepare();self.restore('shutdown');self.restore('activate');self.restore('rollback')
        self.assertEqual((self.data/'level.dat').read_bytes(),b'original at restore time')
        self.assertTrue((self.tx()/'failed-replacement/level.dat').exists())

unittest.main()
PY
