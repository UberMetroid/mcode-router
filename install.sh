#!/usr/bin/env bash
# install.sh — install the mcode-router MiniMax Code host adapter: static CLI binary, plugin
# package, skills, agent profiles, MCP server entry, and context hooks.
# Idempotent: safe to re-run. `./install.sh --uninstall [--purge]` reverses it.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_DIR="$REPO_ROOT/plugins/mcode-router"
ROUTER_BIN="$REPO_ROOT/bin/mcode-router"
STATE_BASE="${XDG_STATE_HOME:-$HOME/.local/state}"
STATE_DIR="$STATE_BASE/mcode-router"
QUOTA_FILE="$STATE_DIR/quota.json"
DEFAULT_QUOTA="$PLUGIN_DIR/config/quota.default.json"
QUOTA_SOURCE="existing"
MCODE_DIR="${MINIMAX_DATA_DIR:-$HOME/.minimax}"
NO_BUILD="${MCODE_ROUTER_NO_BUILD:-${OODA_NO_BUILD:-0}}"
RELEASE_BASE="${MCODE_ROUTER_RELEASE_BASE:-${OODA_RELEASE_BASE:-https://github.com/UberMetroid/mcode-router/releases/latest/download}}"

usage() {
  cat <<'EOF'
usage: ./install.sh [--uninstall [--purge]] [--help]

  (no options)         build (if go present) or fetch the prebuilt binary, then install:
                       CLI symlink, skills, agent profiles, MCP server entry, context hooks
  --uninstall          remove the CLI symlink and unwire the MiniMax Code adapter
  --uninstall --purge  also delete ~/.local/state/mcode-router
  -h, --help           show this help

environment:
  MCODE_ROUTER_NO_BUILD=1    use the existing plugins/mcode-router/bin/mcode-router
  MCODE_ROUTER_RELEASE_BASE  override base URL for prebuilt binaries + SHA256SUMS
  MINIMAX_DATA_DIR           MiniMax Code data dir (default ~/.minimax)
  XDG_STATE_HOME             base for runtime state (default ~/.local/state)
EOF
}

MODE="install"
case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --uninstall) MODE="uninstall"; shift ;;
  "") ;;
  *) echo "error: unknown option: $1 (see --help)" >&2; exit 2 ;;
esac
[ "$MODE" = "uninstall" ] || [ "$#" -eq 0 ] || { echo "error: unexpected argument: $1" >&2; exit 2; }
[ -d "$PLUGIN_DIR" ] || { echo "error: plugin not found at $PLUGIN_DIR" >&2; exit 1; }

if [ "$MODE" = "uninstall" ]; then
  if [ -x "$ROUTER_BIN" ]; then
    "$ROUTER_BIN" __setup check-state "$STATE_BASE" "$STATE_DIR" "$HOME" "$REPO_ROOT" || exit 1
  else
    case "$STATE_BASE" in /*) ;; *) echo "error: state base must be absolute: $STATE_BASE" >&2; exit 1 ;; esac
    case "$STATE_DIR" in
      "$HOME"|"$HOME/"|"$REPO_ROOT"|"$REPO_ROOT"/*)
        echo "error: refusing unsafe state dir: $STATE_DIR" >&2; exit 1 ;;
    esac
  fi
  # shellcheck source=uninstall.sh
  . "$REPO_ROOT/uninstall.sh"
  mcode_router_uninstall "$@"
  exit $?
fi

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "error: sha256sum or shasum required to verify downloaded binary" >&2
    return 1
  fi
}

download_prebuilt_binary() {
  local base os arch asset
  base="${RELEASE_BASE%/}"
  case "$(uname -s)" in
    Linux) os="linux" ;;
    Darwin) os="darwin" ;;
    *) echo "error: unsupported OS '$(uname -s)' for prebuilt mcode-router (install Go 1.22+)" >&2; return 1 ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) echo "error: unsupported arch '$(uname -m)' for prebuilt mcode-router (install Go 1.22+)" >&2; return 1 ;;
  esac
  command -v curl >/dev/null 2>&1 || {
    echo "error: curl required to download prebuilt mcode-router binary (or install Go 1.22+)" >&2
    return 1
  }
  asset="mcode-router-${os}-${arch}"
  (
    local stage expected actual cur_hash
    stage="$(mktemp -d)"
    trap 'rm -rf -- "$stage"' EXIT
    curl -fsSL "$base/SHA256SUMS" -o "$stage/SHA256SUMS" || {
      echo "error: failed to download $base/SHA256SUMS" >&2
      return 1
    }
    expected="$(awk -v f="$asset" '($2 == f || $2 == "*"f) {print $1; exit}' "$stage/SHA256SUMS")"
    [ -n "$expected" ] || { echo "error: checksum for $asset missing in SHA256SUMS" >&2; return 1; }
    if [ -x "$ROUTER_BIN" ]; then
      cur_hash="$(sha256_file "$ROUTER_BIN")"
      if [ "$cur_hash" = "$expected" ]; then
        return 0
      fi
    fi
    echo "downloading prebuilt $asset from $base/$asset ..."
    curl -fsSL "$base/$asset" -o "$stage/$asset" || {
      echo "error: failed to download $base/$asset" >&2
      return 1
    }
    actual="$(sha256_file "$stage/$asset")"
    if [ "$expected" != "$actual" ]; then
      echo "error: SHA-256 mismatch for $asset (expected $expected, got $actual)" >&2
      return 1
    fi
    install -m 0755 "$stage/$asset" "$ROUTER_BIN"
  )
}

mkdir -p "$REPO_ROOT/bin"
if [ "$NO_BUILD" != "1" ]; then
  if command -v go >/dev/null 2>&1 && [ -w "$REPO_ROOT/bin" ]; then
    (cd "$REPO_ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$ROUTER_BIN" ./cmd/mcode-router)
  else
    download_prebuilt_binary
  fi
fi
[ -x "$ROUTER_BIN" ] || { echo "error: executable not found at $ROUTER_BIN" >&2; exit 1; }
ln -sfn mcode-router "$REPO_ROOT/bin/mcode-router-mcp"

"$ROUTER_BIN" __setup check-state "$STATE_BASE" "$STATE_DIR" "$HOME" "$REPO_ROOT" || exit 1

BACKUP_ROOT="$STATE_DIR/backups"
BACKUP_DIR=""

archive_path() {
  if [ -z "$BACKUP_DIR" ]; then
    BACKUP_DIR="$BACKUP_ROOT/$(date -u +%Y%m%dT%H%M%SZ)-$$"
    mkdir -p "$BACKUP_DIR"
    chmod 700 "$BACKUP_ROOT" "$BACKUP_DIR"
  fi
  mv "$2" "$BACKUP_DIR/$1"
}

link() {
  if [ -L "$2" ] && [ "$(readlink "$2")" = "$1" ]; then
    return 0
  fi
  if [ -e "$2" ] || [ -L "$2" ]; then
    archive_path "$3" "$2"
  fi
  ln -sfn "$1" "$2"
}

# Preflight every directory we will write into before mutating any state.
"$ROUTER_BIN" __setup check-dirs "$STATE_DIR" "$HOME/.local/bin" "$MCODE_DIR" || exit 1

# 1. Mutable state lives outside git, private to the user.
mkdir -p "$STATE_DIR"
chmod 700 "$STATE_DIR"

# 2. Seed quota.json from the packaged defaults on first install.
if [ ! -f "$QUOTA_FILE" ]; then
  QUOTA_SOURCE="$DEFAULT_QUOTA"
  cp -p "$QUOTA_SOURCE" "$QUOTA_FILE"
fi
if ! "$ROUTER_BIN" __setup valid-quota "$QUOTA_FILE"; then
  echo "error: $QUOTA_FILE is not a JSON object with 'providers'; leaving state untouched" >&2
  exit 1
fi
chmod 600 "$QUOTA_FILE"

# 3. CLI binary on PATH.
mkdir -p "$HOME/.local/bin"
link "$ROUTER_BIN" "$HOME/.local/bin/mcode-router" "bin-mcode-router"

# 4. MiniMax Code host adapter: skills, agent profiles, MCP server and hooks.
MCODE_OUT="$("$ROUTER_BIN" __mcode install --data-dir "$MCODE_DIR" --plugin-dir "$PLUGIN_DIR")" || {
  echo "error: mcode host adapter wiring failed" >&2
  exit 1
}

ACTIVE_BIN="$(command -v mcode-router 2>/dev/null || true)"
PATH_NOTE=""
if [ -n "$ACTIVE_BIN" ] && [ "$ACTIVE_BIN" != "$HOME/.local/bin/mcode-router" ] && [ "$ACTIVE_BIN" != "$ROUTER_BIN" ]; then
  PATH_NOTE="  note        : 'mcode-router' on PATH resolves to $ACTIVE_BIN; use ~/.local/bin/mcode-router"
fi

cat <<EOF
mcode-router install complete
  repo        : $REPO_ROOT
  quota       : $QUOTA_FILE (source: $QUOTA_SOURCE)
  cli         : ~/.local/bin/mcode-router -> $ROUTER_BIN
  mcode       : $MCODE_DIR
  archived    : ${BACKUP_DIR:-(none displaced)}
EOF
[ -z "$MCODE_OUT" ] || printf '%s\n' "$MCODE_OUT" | sed 's/^/  mcode      : /'
[ -n "$PATH_NOTE" ] && printf '%s\n' "$PATH_NOTE"
cat <<EOF

  statusline : optional - a live worker indicator in the MiniMax Code status bar.
               Add this to $MCODE_DIR/config.yaml (tui is merged, not replaced):

  tui:
    statusLine:
      - current-dir
      - session-title
      - git-branch
      - model-with-reasoning
      - context-window
      - token-quota
      - context-remaining
      - custom-command
    customStatusLine:
      command: mcode-router --statusline
      display: block
      position: below
      colorMode: ansi
      maxLines: 6
      timeoutMs: 2000
      intervalSeconds: 2
EOF
echo "Next: restart MiniMax Code, then: mcode-router --status && mcode-router --usage"
