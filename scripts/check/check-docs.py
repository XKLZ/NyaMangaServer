"""Check repository Markdown file links without reading ignored runtime/private files."""
import pathlib
import re
import subprocess
from urllib.parse import unquote, urlsplit

root = pathlib.Path(__file__).resolve().parent.parent.parent
files = subprocess.check_output(['rg', '--files', '--hidden', '-g', '*.md', '-g', '!.git'], cwd=root, text=True).splitlines()
errors = []
count = 0
for name in files:
    file = root / name
    text = file.read_text()
    for target in re.findall(r'\[[^\]\n]*\]\(([^)\n]+)\)', text):
        target = target.strip().strip('<>')
        parsed = urlsplit(target)
        if parsed.scheme or target.startswith('#'):
            continue
        path = unquote(parsed.path)
        if not path:
            continue
        count += 1
        if not (file.parent / path).exists():
            errors.append(f'{name}: missing {target}')
if errors:
    raise SystemExit('\n'.join(errors))
print(f'Markdown local file links: PASS ({count} links in {len(files)} files)')
