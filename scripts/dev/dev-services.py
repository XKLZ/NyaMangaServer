#!/usr/bin/env python3
"""Start/stop only the standard project development API and Vite."""
import argparse
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
DEV = ROOT / '.runtime/dev'


def guard():
    record = ROOT / '.runtime/maintenance/promote-pilot/state.json'
    if record.exists() and json.loads(record.read_text())['phase'] not in ('complete', 'rolled_back'):
        raise RuntimeError('instance migration incomplete; resume or roll back before starting')
    for name in ('config.json', 'vite-config.json', 'data/app.sqlite'):
        if not (DEV / name).is_file():
            raise RuntimeError('existing instance/build required; use explicit make init-dev for a new installation')


def command(component):
    if component == 'api':
        return [str(ROOT / '.runtime/bin/server'), '--config', str(DEV / 'vite-config.json')]
    return [str(ROOT / 'web/node_modules/.bin/vite'), '--config', str(ROOT / 'web/vite.config.mjs')]


def info(pid):
    r = subprocess.run(['ps', '-p', str(pid), '-o', 'command='], capture_output=True, text=True)
    if r.returncode and r.stderr.strip():
        raise RuntimeError('process identity unavailable; stop/status requires normal process inspection permission')
    return r.stdout.strip()


def owned(component, pid):
    cmd = info(pid)
    expected = command(component)
    return all(value in cmd for value in expected) if component == 'api' else all(value in cmd for value in expected[1:]) and str(ROOT / 'web/node_modules/.bin/vite') in cmd


def listener(port):
    with socket.socket() as s:
        s.settimeout(.2)
        return s.connect_ex(('127.0.0.1', port)) == 0


def run(action, components):
    if action == 'start': guard()
    for component in components:
        port = 8080 if component == 'api' else 5173
        pidfile = DEV / (component + '.managed.pid')
        pid = int(pidfile.read_text()) if pidfile.exists() else None
        if action == 'status':
            print(component + ': ' + ('running' if pid and owned(component, pid) else 'not managed/running'))
        elif action == 'stop':
            if not pid: print(component + ': no managed PID'); continue
            if not owned(component, pid):
                if info(pid): raise RuntimeError('PID identity differs; refusing to stop')
                pidfile.unlink()
                continue
            os.kill(pid, signal.SIGTERM)
            for _ in range(100):
                if not owned(component, pid): break
                time.sleep(.1)
            else: raise RuntimeError('normal shutdown timed out; no force kill performed')
            pidfile.unlink(missing_ok=True)
            print(component + ': stopped')
        elif action == 'start':
            if pid and owned(component, pid):
                print(component + ': already running'); continue
            if listener(port): raise RuntimeError('standard port already occupied; refusing duplicate start')
            logfile = DEV / (component + '.log')
            fd = os.open(logfile, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
            env = dict(os.environ)
            env.pop('NYAMANGA_PILOT', None)
            with os.fdopen(fd, 'a') as log:
                p = subprocess.Popen(command(component), cwd=ROOT / 'web' if component == 'web' else ROOT,
                                     stdin=subprocess.DEVNULL, stdout=log, stderr=log, start_new_session=True, env=env)
            fd = os.open(pidfile, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
            with os.fdopen(fd, 'w') as f: f.write(str(p.pid) + '\n')
            for _ in range(100):
                if p.poll() is not None: raise RuntimeError(component + ' exited; inspect its private log')
                if listener(port): break
                time.sleep(.1)
            else: raise RuntimeError(component + ' startup timed out; inspect status/log')
            print(component + ': http://127.0.0.1:' + str(port) + ' (log .runtime/dev/' + component + '.log)')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['start', 'stop', 'status'])
    parser.add_argument('component', nargs='?', choices=['api', 'web'])
    args = parser.parse_args()
    try:
        run(args.action, [args.component] if args.component else ['web', 'api'])
    except (OSError, ValueError, RuntimeError) as e:
        print('Development service stopped: ' + str(e), file=sys.stderr)
        sys.exit(1)
