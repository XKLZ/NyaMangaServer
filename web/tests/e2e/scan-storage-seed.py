"""Only synthetic independent scan/storage UI fixtures; never accept daily paths."""
import json, sys, pathlib, sqlite3, zipfile, zlib, struct, time, os
config = json.loads(pathlib.Path(sys.argv[1]).read_text())
data = pathlib.Path(config['data_dir'])
assert '/.runtime/tests/runs/scan-storage-ui-' in str(data)
source = pathlib.Path(config['library_roots'][0]['path'])
def png(w, h):
    def chunk(k, v): return struct.pack('>I', len(v)) + k + v + struct.pack('>I', zlib.crc32(k+v))
    return b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', w, h, 8, 0, 0, 0, 0)) + chunk(b'IDAT', zlib.compress((b'\0' + b'\x80'*w)*h)) + chunk(b'IEND', b'')
with zipfile.ZipFile(source/'anonymous-budget.cbz', 'w') as z: z.writestr('1.png', png(5000,5000))
p = source/'anonymous-encoding.cbz'
with zipfile.ZipFile(p, 'w') as z:
    z.writestr('AA.png', png(2,2)); z.writestr('BB.png', png(2,2))
p.write_bytes(p.read_bytes().replace(b'AA.png', b'\x82\xa0.png').replace(b'BB.png', b'\x82\xa2.png'))
def material(key, contents):
    p = data/key; p.parent.mkdir(parents=True, exist_ok=True); p.write_bytes(contents); os.utime(p, (time.time()-172800,)*2)
material('tmp/anonymous-expired.bin', b'x'*2048)
material('assets/treec-originals/anonymous.png', png(2,2))
material('instance-recovery/anonymous-ended/upload.zip', b'x'*4096)
material('instance-recovery/anonymous-ended/unknown.txt', b'keep')
material('instance-recovery/anonymous-backup/archive.zip', b'user-backup-retained')
c=sqlite3.connect(data/'app.sqlite')
c.execute("INSERT INTO instance_operations(id,kind,request_key,state,phase,created_at,updated_at) VALUES('anonymous-ended','restore','anonymous-ended','failed','upload',1,2)")
c.execute("INSERT INTO instance_operations(id,kind,request_key,state,phase,created_at,updated_at) VALUES('anonymous-backup','backup','anonymous-backup','completed','complete',1,2)")
c.commit(); c.close()

# The generic recovery fixture contains illustrative cache paths. Give these
# synthetic images real cache-shaped keys for the actual cache-delete API.
for kind, digit in [('covers','a'),('tag-images','b')]:
    old=data/f'cache/{kind}/v1/fixture/retained.png'
    new=data/f'cache/{kind}/v1/{digit*2}/{digit*64}.png'
    new.parent.mkdir(parents=True,exist_ok=True); old.rename(new)
