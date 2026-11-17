#!/usr/bin/env bash
# uninstall.sh — thin wrapper: `./uninstall.sh [--purge]` runs `./install.sh --uninstall [--purge]`.
# install.sh sources this file for removal logic; executing it directly re-enters install.sh.
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  exec "$(cd "$(dirname "$0")" && pwd)/install.sh" --uninstall "$@"
fi

owned_link() {
  local base="${2:-$PLUGIN_DIR}"
  if [ -x "$ROUTER_BIN" ]; then
    "$ROUTER_BIN" __setup owned-link "$1" "$base"
    return $?
  fi
  [ -L "$1" ] || return 1
  local target
  target="$(readlink "$1" || true)"
  case "$target" in
    "$base"|"$base"/*) return 0 ;;
    *) return 1 ;;
  esac
}

check_link_writable() {
  local p="$1" b="${2:-$PLUGIN_DIR}"
  if owned_link "$p" "$b" && { [ ! -w "$(dirname "$p")" ] || [ ! -x "$(dirname "$p")" ]; }; then
    echo "error: $(dirname "$p") is not writable (or searchable); cannot remove $p (nothing changed)" >&2
    return 1
  fi
}

mcode_router_uninstall() {
  local purge=0 arg path removed=0 mcode_out
  local links=("$HOME/.local/bin/mcode-router")
  for arg in "$@"; do
    case "$arg" in
      --purge) purge=1 ;;
      *) echo "error: unknown uninstall option: $arg (see --help)" >&2; return 2 ;;
    esac
  done
  for path in "${links[@]}"; do
    check_link_writable "$path" "$PLUGIN_DIR" || return 1
  done

  echo "mcode-router uninstall"
  for path in "${links[@]}"; do
    if owned_link "$path" "$PLUGIN_DIR"; then
      rm -f "$path"
      echo "  removed     : $path"
      removed=$((removed + 1))
    elif [ -e "$path" ] || [ -L "$path" ]; then
      echo "  left alone  : $path (not a symlink into $PLUGIN_DIR)"
    fi
  done
  [ "$removed" -gt 0 ] || echo "  symlinks    : nothing to remove"

  if [ -x "$ROUTER_BIN" ] && [ -d "$MCODE_DIR" ]; then
    if mcode_out="$("$ROUTER_BIN" __mcode uninstall --data-dir "$MCODE_DIR" --plugin-dir "$PLUGIN_DIR")"; then
      printf '%s\n' "$mcode_out" | sed 's/^/  mcode      : /'
    else
      echo "  mcode      : left $MCODE_DIR untouched (MiniMax Code adapter removal failed)"
    fi
  fi

  if [ "$purge" -eq 1 ]; then
    if [ -d "$STATE_DIR" ]; then
      rm -rf "$STATE_DIR"
      echo "  state       : purged $STATE_DIR (quota + backups)"
    else
      echo "  state       : $STATE_DIR already absent"
    fi
  elif [ -d "$STATE_DIR" ]; then
    echo "  state       : kept $STATE_DIR (re-run with --purge to delete)"
  fi
  echo "Next: restart MiniMax Code so the adapter unloads"
}
