package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

var safeIDSubRe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// PluginRootPath resolves the plugins/mcode-router directory dynamically at call time.
func PluginRootPath() string {
	isPluginRoot := func(dir string) bool {
		for _, marker := range []string{
			filepath.Join(".minimax-plugin", "plugin.json"),
			filepath.Join(".claude-plugin", "plugin.json"),
			"plugin.json",
		} {
			if st, err := os.Stat(filepath.Join(dir, marker)); err == nil && !st.IsDir() {
				return true
			}
		}
		return false
	}
	for _, key := range []string{"MCODE_ROUTER_PLUGIN_ROOT", "OODA_PLUGIN_ROOT"} {
		if env := os.Getenv(key); env != "" {
			return env
		}
	}
	pluginDirs := func(base string) []string {
		return []string{base, filepath.Join(base, "plugins", "mcode-router")}
	}
	if exe, err := os.Executable(); err == nil {
		if realExe, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = realExe
		}
		base := filepath.Dir(filepath.Dir(exe))
		for _, cand := range pluginDirs(base) {
			if isPluginRoot(cand) {
				return cand
			}
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "plugins/mcode-router"
	}
	cur := cwd
	for {
		for _, cand := range pluginDirs(cur) {
			if isPluginRoot(cand) {
				return cand
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return filepath.Join(cwd, "plugins", "mcode-router")
}

// QuotaFilePath resolves quota.json via override, env vars, or XDG state home.
func QuotaFilePath(override string) string {
	if override != "" {
		return override
	}
	for _, key := range []string{"MCODE_ROUTER_QUOTA_FILE", "AGENT_QUOTA_FILE", "OODA_QUOTA_FILE"} {
		if env := os.Getenv(key); env != "" {
			return env
		}
	}
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, _ := os.UserHomeDir()
		stateHome = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateHome, "mcode-router", "quota.json")
}

// DiscoveryFilePath resolves the cached CLI capability JSON path beside quota.json.
func DiscoveryFilePath() string {
	for _, key := range []string{"MCODE_ROUTER_DISCOVERY_FILE", "OODA_DISCOVERY_FILE"} {
		if env := os.Getenv(key); env != "" {
			if strings.HasPrefix(env, "~/") {
				home, _ := os.UserHomeDir()
				return filepath.Join(home, env[2:])
			}
			return env
		}
	}
	return filepath.Join(filepath.Dir(QuotaFilePath("")), "discovery.json")
}

// ScratchDirPath returns the router scratch directory for telemetry and caches.
func ScratchDirPath() string {
	for _, key := range []string{"MCODE_ROUTER_SCRATCH_DIR", "ROUTER_SCRATCH_DIR"} {
		if env := os.Getenv(key); env != "" {
			return env
		}
	}
	return "/tmp/mcode-router-scratch-" + strconv.Itoa(os.Getuid())
}

// SlotsRootPath returns the worktree slot scratch directory.
func SlotsRootPath() string {
	for _, key := range []string{"MCODE_ROUTER_SLOTS_DIR", "ROUTER_SLOTS_DIR"} {
		if env := os.Getenv(key); env != "" {
			return env
		}
	}
	return "/tmp/mcode-router-slots-" + strconv.Itoa(os.Getuid())
}

// EnsureStateRoots creates the router's state roots when they are missing and
// refuses a root this user does not own, or one another user can write to. A
// root created under a shared parent such as /tmp would let them replace the
// router's cache and slot subtrees, so it is rejected instead of trusted.
var EnsureStateRoots = func() error {
	for _, dir := range []string{ScratchDirPath(), SlotsRootPath()} {
		if err := ensurePrivateRoot(dir); err != nil {
			return err
		}
	}
	return nil
}

// ensurePrivateRoot creates dir when absent and fails closed unless the
// directory belongs to this user and no other user can write to it.
var ensurePrivateRoot = func(dir string) error {
	st, err := os.Lstat(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("state root %s: %w", dir, err)
		}
		if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
			return fmt.Errorf("state root %s: %w", dir, mkErr)
		}
		if st, err = os.Lstat(dir); err != nil {
			return fmt.Errorf("state root %s: %w", dir, err)
		}
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("state root %s is a symlink, refusing to use it", dir)
	}
	if !st.IsDir() {
		return fmt.Errorf("state root %s is not a directory", dir)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("state root %s: cannot read ownership", dir)
	}
	if int(sys.Uid) != os.Getuid() {
		return fmt.Errorf("state root %s is owned by uid %d, refusing to use it", dir, sys.Uid)
	}
	if perm := st.Mode().Perm(); perm&0o022 != 0 {
		// Writable by another user means they can plant files here, so the
		// root is refused. The mode is never rewritten: the path may be one
		// the user chose and has no other business being re-permissioned.
		return fmt.Errorf("state root %s has mode %04o and is writable by other users; "+
			"tighten it to 0700 or point the router at a private path", dir, perm)
	}
	return nil
}

// SafeID sanitizes a string for safe filenames, defaulting to "default".
func SafeID(raw string) string {
	cleaned := safeIDSubRe.ReplaceAllString(strings.TrimSpace(raw), "_")
	if cleaned == "" {
		return "default"
	}
	return cleaned
}

// RouterDepth returns max nested dispatch depth across MCODE_ROUTER_DEPTH and ROUTER_DEPTH.
func RouterDepth() int {
	best := 0
	for _, key := range []string{"MCODE_ROUTER_DEPTH", "ROUTER_DEPTH"} {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil {
			best = max(best, 1)
			continue
		}
		best = max(best, n)
	}
	return best
}
