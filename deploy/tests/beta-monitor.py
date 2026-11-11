#!/usr/bin/env python3
"""Bounded NAS observation for only the named beta project; JSONL private output."""
import argparse
import json
from pathlib import Path
import subprocess
import time

p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--output',type=Path,required=True)
p.add_argument('--seconds',type=int,default=1900)
a=p.parse_args()
assert 1800 <= a.seconds <= 3600
start=time.monotonic()
all_ids=subprocess.check_output(['docker','ps','-a','-q'],text=True).split()
all_info=json.loads(subprocess.check_output(['docker','inspect',*all_ids],text=True)) if all_ids else []
original_ids=[v['Id'] for v in all_info if v['Config'].get('Labels',{}).get('com.docker.compose.project') not in ('nyamangaserver-beta','nyamangaserver-beta-restore')]
def states(ids):
 if not ids:return []
 values=json.loads(subprocess.check_output(['docker','inspect',*ids],text=True))
 return [{'id':v['Id'],'name':v['Name'],'status':v['State']['Status'],'health':v['State'].get('Health',{}).get('Status'),'oom_killed':v['State']['OOMKilled'],'restart_count':v['RestartCount']} for v in values]
initial_states=states(original_ids)
with a.output.open('x') as report:
 a.output.chmod(0o600)
 while time.monotonic()-start<a.seconds:
  ids=subprocess.check_output(['docker','ps','-q','--filter','label=com.docker.compose.project=nyamangaserver-beta'],text=True).split()
  stats=[]
  if ids:
   raw=subprocess.check_output(['docker','stats','--no-stream','--format','{{json .}}',*ids],text=True)
   stats=[json.loads(row) for row in raw.splitlines()]
  vm={k:int(v) for k,v in (row.split() for row in Path('/proc/vmstat').read_text().splitlines()) if k in ('pswpin','pswpout','oom_kill')}
  mem={row.split(':')[0]:row.split(':')[1].strip() for row in Path('/proc/meminfo').read_text().splitlines() if row.startswith(('MemAvailable:','SwapFree:','SwapTotal:'))}
  report.write(json.dumps({'time':time.time(),'elapsed':time.monotonic()-start,'stats':stats,'vm':vm,'memory':mem,'beta_states':states(ids),'original_states':states(original_ids),'initial_original_states':initial_states})+'\n');report.flush()
  time.sleep(30)
print('Completed bounded beta observation')
