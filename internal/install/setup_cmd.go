package install

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func cmdCheckState(argv []string, errW io.Writer) int {
	if len(argv) < 4 {
		return 2
	}
	base, state, home, repo := argv[0], argv[1], argv[2], argv[3]
	if !filepath.IsAbs(base) {
		fmt.Fprintf(errW, "error: state base %q (XDG_STATE_HOME) must be an absolute path\n", base)
		return 1
	}
	realState := evalSymlinksBest(state)
	realRepo := evalSymlinksBest(repo)
	realHome := evalSymlinksBest(home)
	if realState == "/" || realState == realHome || realState == realRepo ||
		strings.HasPrefix(realState, realRepo+string(os.PathSeparator)) ||
		strings.HasPrefix(realRepo, realState+string(os.PathSeparator)) {
		fmt.Fprintf(errW,
			"error: unsafe state dir %s (resolves to %s); must not be /, $HOME, or overlap repo %s. Fix XDG_STATE_HOME.\n",
			state, realState, realRepo)
		return 1
	}
	return 0
}

func cmdCheckDirs(argv []string, errW io.Writer) int {
	for _, target := range argv {
		cur, err := filepath.Abs(target)
		if err != nil {
			cur = filepath.Clean(target)
		}
		for {
			st, serr := os.Stat(cur)
			if serr != nil {
				if _, lerr := os.Lstat(cur); lerr == nil {
					fmt.Fprintf(errW, "error: %s is a broken symlink; refusing to install\n", cur)
					return 1
				}
				parent := filepath.Dir(cur)
				if parent == cur {
					break
				}
				cur = parent
				continue
			}
			if !st.IsDir() {
				fmt.Fprintf(errW, "error: %s exists and is not a directory; refusing to install\n", cur)
				return 1
			}
			if os.Geteuid() != 0 && syscall.Access(cur, 3) != nil {
				fmt.Fprintf(errW, "error: directory %s is not writable; cannot create %s\n", cur, target)
				return 1
			}
			break
		}
	}
	return 0
}

func cmdValidQuota(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 1
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return 1
	}
	provs, ok := m["providers"].(map[string]any)
	if !ok || len(provs) == 0 {
		return 1
	}
	return 0
}

func cmdOwnedLink(linkPath, pluginDir string) int {
	st, err := os.Lstat(linkPath)
	if err != nil || (st.Mode()&os.ModeSymlink) == 0 {
		return 1
	}
	dest, err := os.Readlink(linkPath)
	if err != nil {
		return 1
	}
	if !filepath.IsAbs(dest) {
		absLink, _ := filepath.Abs(linkPath)
		dest = filepath.Join(filepath.Dir(absLink), dest)
	}
	target := evalSymlinksBest(dest)
	root := evalSymlinksBest(pluginDir)
	if target == root || strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return 0
	}
	return 1
}

// RunSetupCmd executes install/uninstall preflight and JSON helpers without Python.
func RunSetupCmd(argv []string, errW io.Writer) int {
	if errW == nil {
		errW = os.Stderr
	}
	if len(argv) == 0 {
		return 2
	}
	switch argv[0] {
	case "check-state":
		return cmdCheckState(argv[1:], errW)
	case "check-dirs":
		return cmdCheckDirs(argv[1:], errW)
	case "valid-quota":
		if len(argv) < 2 {
			return 2
		}
		return cmdValidQuota(argv[1])
	case "owned-link":
		if len(argv) < 3 {
			return 2
		}
		return cmdOwnedLink(argv[1], argv[2])
	default:
		fmt.Fprintf(errW, "error: unknown __setup command: %s\n", argv[0])
		return 2
	}
}
