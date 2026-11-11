"""Isolated anonymous M2 Docker acceptance. Never point at production data."""
import argparse
import hashlib
import http.cookiejar
import json
import os
import pathlib
import secrets
import shutil
import subprocess
import time
import urllib.error
import urllib.request

p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--test-root',required=True,help='explicit isolated anonymous acceptance directory')
p.add_argument('--compose',required=True)
p.add_argument('--library-compose',required=True)
p.add_argument('--token-file',required=True)
p.add_argument('--fixtures',required=True)
p.add_argument('--url',default='http://localhost:9400')
a=p.parse_args()
compose=['docker','compose','-f',str(pathlib.Path(a.compose).resolve()),'-f',str(pathlib.Path(a.library_compose).resolve())]
origin=a.url.rstrip('/')
fixtures=pathlib.Path(a.fixtures).resolve()
test_root=pathlib.Path(a.test_root).resolve()
if test_root in (pathlib.Path('/'),pathlib.Path.home()) or not fixtures.is_relative_to(test_root) or fixtures==test_root:
    raise SystemExit('fixtures must be beneath an explicit isolated test root')
for filename in [a.token_file,a.compose,a.library_compose]:
    if not pathlib.Path(filename).resolve().is_relative_to(test_root):
        raise SystemExit('all acceptance materials must be within test-root')
manifest=json.loads((fixtures/'.nyamanga-anonymous-fixtures.json').read_text())
if manifest.get('kind')!='nyamangaserver-anonymous-v1' or len(manifest.get('files',{}))!=3:
    raise SystemExit('anonymous fixture manifest required')
actual={str(f.relative_to(fixtures)):hashlib.sha256(f.read_bytes()).hexdigest() for f in fixtures.rglob('*') if f.is_file() and f.name!='.nyamanga-anonymous-fixtures.json'}
if actual!=manifest['files'] or any(f.is_symlink() for f in fixtures.rglob('*')):
    raise SystemExit('fixture contents differ from the generated anonymous set')
jar=http.cookiejar.CookieJar()
client=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPCookieProcessor(jar))
csrf=''
password=secrets.token_urlsafe(24)
def call(method,path,body=None,expected=200):
    global csrf
    headers={'Origin':origin}
    if csrf:headers['X-CSRF-Token']=csrf
    data=None
    if body is not None:
        headers['Content-Type']='application/json';data=json.dumps(body).encode()
    request=urllib.request.Request(origin+'/api/v1'+path,data,headers,method=method)
    try:r=client.open(request,timeout=15)
    except urllib.error.HTTPError as e:r=e
    with r:
        assert r.status==expected,(method,path,r.status,expected)
        raw=r.read()
        if r.headers.get('Content-Type','').startswith('image/'):
            assert raw.startswith(b'\x89PNG') or raw.startswith(b'\xff\xd8') or raw.startswith(b'RIFF')
            return raw
        value=json.loads(raw) if raw else None
        if isinstance(value,dict) and 'csrf_token' in value:csrf=value['csrf_token']
        return value

def wait_job(id,timeout=90):
    deadline=time.time()+timeout
    while time.time()<deadline:
        j=call('GET','/jobs/'+id)
        if j['state'] in ['completed','failed','cancelled']:return j
        time.sleep(.05)
    raise AssertionError('scan timed out')
def start(id):return call('POST','/libraries/'+id+'/scans',{},202)['job_id']
def hashes():return {str(f.relative_to(fixtures)):hashlib.sha256(f.read_bytes()).hexdigest() for f in fixtures.rglob('*') if f.is_file()}
assert call('GET','/auth/status')=={'initialized':False},'requires new anonymous data volume'
call('GET','/books',expected=401)
call('POST','/auth/setup',{'setup_token':pathlib.Path(a.token_file).read_text().strip(),'password':password})
assert call('GET','/diagnostics')['database_baseline']=='foundation-v1'
before=hashes()
l=call('POST','/libraries',{'root_key':'anonymous','display_name':'Anonymous acceptance','legacy_zip_encoding':'utf8'},201)
job=wait_job(start(l['id']))
assert job['state']=='completed' and job['done']==3 and job['failed']==1
books=call('GET','/books')['items']
ids={b['relative_path']:(b['id'],b['content_revision']) for b in books}
assert len(books)==3 and sum(b['file_state']=='available' for b in books)==2
for b in books:
    if b['file_state']=='available':
        call('GET',f"/books/{b['id']}/cover?content_revision={b['content_revision']}")
        pages=call('GET',f"/books/{b['id']}/pages")
        assert [v['display_name'] for v in pages['items']]==['1.png','2.png','10.PNG']
        call('GET',f"/books/{b['id']}/pages/3/image?content_revision={b['content_revision']}")
assert wait_job(start(l['id']))['done']==3
assert {b['relative_path']:(b['id'],b['content_revision']) for b in call('GET','/books')['items']}==ids
print('fresh-volume library / bounded scan / partial corrupt archive / natural order / cover-page / stable rescan: PASS',flush=True)
call('PATCH','/libraries/'+l['id'],{'expected_revision':1,'legacy_zip_encoding':'gb18030'})
for b in books:
    if b['file_state']=='available':call('GET',f"/books/{b['id']}/cover?content_revision={b['content_revision']}",expected=409)
assert wait_job(start(l['id']))['done']==3
assert all(b['content_revision']==ids[b['relative_path']][1]+1 for b in call('GET','/books')['items'])
print('encoding settings invalidate old image versions and rebuild indexes: PASS',flush=True)

subprocess.run(compose+['restart'],check=True)
subprocess.run(compose+['up','-d','--no-build','--wait'],check=True)
call('GET','/auth/session')
assert call('GET','/books')['total']==3
print('restart preserves authentication and catalog: PASS',flush=True)
# A larger anonymous set ensures stop happens during processing, not after a tiny scan.
recovery=fixtures/'recovery';recovery.mkdir(mode=0o755);recovery.chmod(0o755)
source=fixtures/'合成1.cbz'
for i in range(1000):
    target=recovery/f'fixture-{i:04}.cbz';shutil.copyfile(source,target);target.chmod(0o644)
interrupted=start(l['id'])
deadline=time.time()+10
while time.time()<deadline:
    j=call('GET','/jobs/'+interrupted)
    if j['state']=='running' and j['stage']=='processing' and 0<j['done']<j['total']:break
    time.sleep(.01)
else:raise AssertionError(('could not observe a running partial scan',j['state'],j['error_code']))
subprocess.run(compose+['stop','-t','12'],check=True)
container=subprocess.check_output(compose+['ps','-aq','server'],text=True).strip()
stopped=json.loads(subprocess.check_output(['docker','inspect',container]))[0]
assert stopped['State']['ExitCode']==0
subprocess.run(compose+['up','-d','--no-build','--wait'],check=True)
resumed=wait_job(interrupted,timeout=120)
assert resumed['state']=='completed' and resumed['done']==1003 and resumed['generation']>j['generation']
assert call('GET','/books')['total']==1003
print('SIGTERM during processing / persisted checkpoint / same-job generation recovery: PASS',flush=True)
# Cancel a fresh scan and immediately queue a replacement.
cancelled=start(l['id']);call('POST','/jobs/'+cancelled+'/cancel',expected=202)
replacement=start(l['id']);assert wait_job(replacement,timeout=120)['state']=='completed'
assert call('GET','/jobs/'+cancelled)['state']=='cancelled'
assert call('GET','/books')['total']==1003
print('cancel / immediate restart / no duplicate books / no stale job resurrection: PASS',flush=True)
assert all(hashes()[name]==value for name,value in before.items())
info=json.loads(subprocess.check_output(['docker','inspect',container]))[0]
assert info['Config']['User']=='65532:65532' and info['State']['Health']['Status']=='healthy'
mount=next(m for m in info['Mounts'] if m['Destination']=='/library/manual')
assert not mount['RW']
volume=pathlib.Path(next(m['Source'] for m in info['Mounts'] if m['Destination']=='/data'))
for path in [volume/'app.sqlite',*list((volume/'cache').glob('*.png'))[:3]]:
    assert path.stat().st_uid==65532 and path.stat().st_mode&0o777==0o600
print('read-only source / unchanged original fixtures / non-root database and cache / readiness: PASS',flush=True)
subprocess.run(compose+['stop','-t','12'],check=True)
logs=subprocess.check_output(['docker','logs',container],text=True,stderr=subprocess.STDOUT)
assert 'server stopped' in logs
assert password not in logs and pathlib.Path(a.token_file).read_text().strip() not in logs
assert json.loads(subprocess.check_output(['docker','inspect',container]))[0]['State']['ExitCode']==0
subprocess.run(compose+['down'],check=True)
print('SIGTERM exit 0 / redacted logs / scoped container cleanup: PASS',flush=True)
