#!/usr/bin/env bash
# install.sh (web installer) — one-line install, update, or uninstall for UberMetroid/mcode-router.
# Usage:
#   curl -fsSL https://ubermetroid.github.io/mcode-router/install.sh | bash
#   curl -fsSL https://ubermetroid.github.io/mcode-router/install.sh | bash -s -- --uninstall [--purge]
set -euo pipefail

case "${1:-}" in
  -h|--help)
    cat <<'EOF'
usage: curl -fsSL https://ubermetroid.github.io/mcode-router/install.sh | bash [-s -- OPTIONS]

  (no options)         clone/update mcode-router into ~/.local/share/mcode-router and run ./install.sh
  --uninstall          remove mcode-router symlinks and unwire the MiniMax Code adapter
  --uninstall --purge  also delete ~/.local/state/mcode-router and ~/.local/share/mcode-router

environment:
  MCODE_ROUTER_HOME           override checkout path (default ${XDG_DATA_HOME:-~/.local/share}/mcode-router)
  MCODE_ROUTER_REPO_URL       override git repository URL
  MCODE_ROUTER_RELEASE_BASE   override prebuilt binary + SHA256SUMS base URL
EOF
    exit 0 ;;
esac

REPO_URL="${MCODE_ROUTER_REPO_URL:-https://github.com/UberMetroid/mcode-router.git}"
INSTALL_HOME="${MCODE_ROUTER_HOME:-${XDG_DATA_HOME:-$HOME/.local/share}/mcode-router}"

clean_home="${HOME%/}"
clean_dest="${INSTALL_HOME%/}"
case "$INSTALL_HOME" in
  /*) ;;
  *) echo "error: MCODE_ROUTER_HOME must be absolute: $INSTALL_HOME" >&2; exit 1 ;;
esac
if [ -z "$clean_dest" ] || [ "$clean_dest" = "$clean_home" ]; then
  echo "error: refusing unsafe MCODE_ROUTER_HOME: $INSTALL_HOME" >&2
  exit 1
fi

check_clean_clone() {
  [ -d "$INSTALL_HOME/.git" ] || return 0
  if [ -n "$(git -C "$INSTALL_HOME" status --porcelain)" ]; then
    echo "error: local modifications in $INSTALL_HOME; commit or stash before $1" >&2
    exit 1
  fi
}

is_shallow_clone() {
  [ "$(git -C "$INSTALL_HOME" rev-parse --is-shallow-repository 2>/dev/null || echo false)" = "true" ]
}

if [ "${1:-}" = "--uninstall" ]; then
  shift
  purge=0
  for arg in ${@+"$@"}; do
    case "$arg" in
      --purge) purge=1 ;;
    esac
  done
  if [ "$purge" -eq 1 ] && [ -d "$INSTALL_HOME/.git" ]; then
    check_clean_clone purging
    refspec="+refs/heads/main:refs/remotes/origin/main"
    unpushed="$(git -C "$INSTALL_HOME" rev-list --count --branches --not --remotes 2>/dev/null || echo 0)"
    if [ "$unpushed" -gt 0 ] && is_shallow_clone; then
      git -C "$INSTALL_HOME" fetch -q --unshallow "$REPO_URL" "$refspec" >/dev/null 2>&1 || true
      unpushed="$(git -C "$INSTALL_HOME" rev-list --count --branches --not --remotes 2>/dev/null || echo 0)"
    fi
    if [ "$unpushed" -gt 0 ]; then
      echo "error: $INSTALL_HOME has $unpushed unpushed local commit(s); refusing --purge" >&2
      exit 1
    fi
  fi
  if [ -x "$INSTALL_HOME/install.sh" ]; then
    "$INSTALL_HOME/install.sh" --uninstall ${@+"$@"}
  else
    echo "note: $INSTALL_HOME/install.sh not found; nothing to uninstall"
  fi
  if [ "$purge" -eq 1 ] && [ -d "$INSTALL_HOME/.git" ]; then
    rm -rf -- "$INSTALL_HOME"
    echo "  checkout    : removed $INSTALL_HOME"
  fi
  exit 0
fi

command -v git >/dev/null 2>&1 || { echo "error: git is required" >&2; exit 1; }

if [ -d "$INSTALL_HOME/.git" ]; then
  check_clean_clone updating
  echo "updating $INSTALL_HOME from $REPO_URL ..."
  git -C "$INSTALL_HOME" remote set-url origin "$REPO_URL" >/dev/null 2>&1 || true
  if is_shallow_clone; then
    git -C "$INSTALL_HOME" fetch --unshallow "$REPO_URL" +refs/heads/main:refs/remotes/origin/main
  else
    git -C "$INSTALL_HOME" fetch "$REPO_URL" +refs/heads/main:refs/remotes/origin/main
  fi
  ahead="$(git -C "$INSTALL_HOME" rev-list --count FETCH_HEAD..HEAD 2>/dev/null || echo 0)"
  if [ "$ahead" -gt 0 ]; then
    echo "error: $INSTALL_HOME has $ahead unpushed local commit(s); push or reset before updating" >&2
    exit 1
  fi
  git -C "$INSTALL_HOME" checkout -q -B main FETCH_HEAD
elif [ -e "$INSTALL_HOME" ]; then
  echo "error: $INSTALL_HOME exists and is not a git checkout of mcode-router" >&2
  exit 1
else
  mkdir -p "$(dirname "$INSTALL_HOME")"
  echo "cloning $REPO_URL into $INSTALL_HOME ..."
  git clone "$REPO_URL" "$INSTALL_HOME"
fi

exec "$INSTALL_HOME/install.sh" ${@+"$@"}
