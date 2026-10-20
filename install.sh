#!/bin/sh
set -eu

repo=${LOCAL_AI_REPOSITORY:-artofthesmart/dotfiles-bootstrap}
api=${LOCAL_AI_GITHUB_API_BASE:-https://api.github.com}
cache=${XDG_CACHE_HOME:-"$HOME/.cache"}/local-ai/bootstrap.$$
mkdir -p "$cache"
chmod 700 "$cache"
trap 'rm -rf "$cache"' EXIT HUP INT TERM

case "$api" in https://*) ;; *) echo "local-ai: release API must use HTTPS" >&2; exit 21 ;; esac
command -v curl >/dev/null 2>&1 || { echo "local-ai: curl is required" >&2; exit 11; }
command -v python3 >/dev/null 2>&1 || { echo "local-ai: python3 is required for safe release parsing" >&2; exit 11; }

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "local-ai: unsupported operating system" >&2; exit 10 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "local-ai: unsupported architecture" >&2; exit 10 ;;
esac

fetch() {
  url=$1 output=$2 limit=$3
  case "$url" in https://*) ;; *) echo "local-ai: download URL must use HTTPS" >&2; exit 21 ;; esac
  curl --fail --silent --show-error --location \
    --proto '=https' --proto-redir '=https' \
    --connect-timeout 15 --max-time 180 \
    --max-filesize "$limit" --output "$output" "$url"
}

release_json=$cache/release.json
fetch "$api/repos/$repo/releases/latest" "$release_json" 1048576
eval "$(python3 - "$release_json" "$os" "$arch" <<'PY'
import json, shlex, sys
data=json.load(open(sys.argv[1], encoding="utf-8"))
if data.get("draft") or data.get("prerelease") or not data.get("tag_name"):
    raise SystemExit("local-ai: latest release is not stable")
version=data["tag_name"].removeprefix("v")
suffix=".zip" if sys.argv[2]=="windows" else ".tar.gz"
names={f"local-ai_{sys.argv[2]}_{sys.argv[3]}{suffix}",f"local-ai_{version}_{sys.argv[2]}_{sys.argv[3]}{suffix}"}
assets={a.get("name"):a.get("browser_download_url") for a in data.get("assets",[])}
matches=names & assets.keys()
if len(matches)!=1 or "SHA256SUMS" not in assets:
    raise SystemExit("local-ai: compatible release asset or SHA256SUMS is missing")
name=matches.pop()
for key,value in {"version":version,"asset_name":name,"asset_url":assets[name],"sums_url":assets["SHA256SUMS"]}.items():
    if not isinstance(value,str) or not value.startswith("https://") and key.endswith("_url"):
        raise SystemExit("local-ai: invalid release metadata")
    print(f"{key}={shlex.quote(value)}")
PY
)"

archive=$cache/$asset_name
sums=$cache/SHA256SUMS
fetch "$sums_url" "$sums" 4194304
fetch "$asset_url" "$archive" 268435456
expected=$(python3 - "$sums" "$asset_name" <<'PY'
import re,sys
found=[]
for line in open(sys.argv[1],encoding="utf-8"):
    match=re.fullmatch(r"([0-9A-Fa-f]{64})[ \t]+[*]?([^/\r\n]+)\r?\n?",line)
    if match and match.group(2)==sys.argv[2]: found.append(match.group(1).lower())
if len(found)!=1: raise SystemExit("local-ai: checksum entry is missing or ambiguous")
print(found[0])
PY
)
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$archive" | awk '{print $1}')
else
  actual=$(shasum -a 256 "$archive" | awk '{print $1}')
fi
[ "$actual" = "$expected" ] || { echo "local-ai: SHA-256 verification failed" >&2; exit 21; }

if [ "${LOCAL_AI_VERIFY_ATTESTATION:-0}" = 1 ]; then
  if command -v gh >/dev/null 2>&1; then
    gh attestation verify "$archive" --repo "$repo" >/dev/null
  else
    echo "local-ai: gh is not installed; optional attestation verification skipped" >&2
  fi
fi

bindir=$HOME/.local/bin
target=$bindir/local-ai
stage=$bindir/.local-ai.new.$$
mkdir -p "$bindir"
chmod 700 "$bindir"
if [ -x "$target" ]; then
  "$target" update
  exec "$target" setup "$@"
fi
python3 - "$archive" "$asset_name" "$stage" <<'PY'
import pathlib,shutil,stat,sys,tarfile,zipfile
archive,name,out=sys.argv[1:]
def safe(value):
    p=pathlib.PurePosixPath(value.replace("\\","/"))
    return value and not p.is_absolute() and ".." not in p.parts and ":" not in value
found=[]
if name.endswith(".zip"):
    with zipfile.ZipFile(archive) as z:
        for member in z.infolist():
            if not safe(member.filename): raise SystemExit("local-ai: unsafe archive path")
            mode=(member.external_attr >> 16) & 0o170000
            if member.is_dir(): continue
            if mode and mode != stat.S_IFREG: raise SystemExit("local-ai: unsafe archive entry type")
            if pathlib.PurePosixPath(member.filename).name=="local-ai": found.append(z.read(member))
else:
    with tarfile.open(archive,"r:gz") as t:
        for member in t:
            if not safe(member.name): raise SystemExit("local-ai: unsafe archive path")
            if member.isdir(): continue
            if not member.isfile(): raise SystemExit("local-ai: unsafe archive entry type")
            if pathlib.PurePosixPath(member.name).name=="local-ai":
                stream=t.extractfile(member)
                found.append(stream.read(134217729) if stream else b"")
if len(found)!=1 or not found[0] or len(found[0])>134217728:
    raise SystemExit("local-ai: manager executable is missing, duplicated, or invalid")
open(out,"xb").write(found[0])
PY
chmod 700 "$stage"
"$stage" version >/dev/null
mv -f "$stage" "$target"
"$target" version >/dev/null

case ":${PATH:-}:" in
  *":$bindir:"*) ;;
  *)
    profile=$HOME/.profile
    [ "$os" = darwin ] && profile=$HOME/.zprofile
    block='# >>> local-ai managed PATH >>>
export PATH="$HOME/.local/bin:$PATH"
# <<< local-ai managed PATH <<<'
    printf '%s\n%s\n' "local-ai can add this marker-owned PATH block to $profile:" "$block"
    answer=n
    if [ -t 0 ] && [ -r /dev/tty ]; then
      printf 'Add it? [y/N] ' >/dev/tty
      IFS= read -r answer </dev/tty || answer=n
    fi
    case "$answer" in
      y|Y|yes|YES)
        if grep -F '# >>> local-ai managed PATH >>>' "$profile" >/dev/null 2>&1; then
          echo "local-ai: existing PATH marker left unchanged" >&2
        else
          [ ! -e "$profile.local-ai.backup" ] || { echo "local-ai: refusing to overwrite PATH backup" >&2; exit 50; }
          [ ! -e "$profile" ] || cp -p "$profile" "$profile.local-ai.backup"
          { [ ! -e "$profile" ] || cat "$profile"; printf '\n%s\n' "$block"; } >"$profile.local-ai.new"
          chmod 600 "$profile.local-ai.new"
          mv "$profile.local-ai.new" "$profile"
        fi ;;
      *) echo "local-ai: PATH unchanged; invoke $target directly" ;;
    esac ;;
esac

if [ "$os" = darwin ]; then
  echo "Note: this friend-preview binary is not yet signed/notarized. Do not disable Gatekeeper; confirm provenance and use normal macOS prompts."
fi
exec "$target" setup "$@"
