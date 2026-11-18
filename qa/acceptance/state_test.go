package acceptance

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/verify"
)

// TestStateRootsUserScoped checks that the shared state roots are bound to the
// current user, so another local account cannot pre-create them and collide.
func TestStateRootsUserScoped(t *testing.T) {
	t.Setenv("MCODE_ROUTER_SCRATCH_DIR", "")
	t.Setenv("ROUTER_SCRATCH_DIR", "")
	t.Setenv("MCODE_ROUTER_SLOTS_DIR", "")
	t.Setenv("ROUTER_SLOTS_DIR", "")
	uid := strconv.Itoa(os.Getuid())
	for _, root := range []string{fleet.ScratchDirPath(), fleet.SlotsRootPath()} {
		if !strings.Contains(root, uid) {
			t.Fatalf("state root %q is not bound to uid %s", root, uid)
		}
	}
}

// TestStateRootRejectsUnsafeRoot checks that a state root which is a symlink
// or a plain file is refused instead of being written through.
func TestStateRootRejectsUnsafeRoot(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "real")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(tmp, "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(tmp, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	run := cliEnv(t, "exit 0")
	for name, root := range map[string]string{
		"symlinked": link, "plain file": plain, "world writable": shared,
	} {
		t.Setenv("MCODE_ROUTER_SCRATCH_DIR", root)
		t.Setenv("MCODE_ROUTER_SLOTS_DIR", filepath.Join(tmp, "slots"))
		rc, _, errOut := run("--status")
		if rc == 0 || !strings.Contains(errOut, "state root") {
			t.Fatalf("%s state root accepted: rc=%d err=%q", name, rc, errOut)
		}
	}
}

// TestCacheRejectsTamperedEntry checks that a cache entry whose recorded
// digest does not match its content is refused instead of trusted, so a
// planted entry cannot be served as a real review verdict.
func TestCacheRejectsTamperedEntry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCODE_ROUTER_SCRATCH_DIR", dir)
	t.Setenv("OODA_NO_CACHE", "0")
	t.Setenv("MCODE_ROUTER_NO_CACHE", "0")
	key := "0123456789abcdef0123456789abcdef"
	entry := verify.CacheEntry{
		Stage: "verify", Lane: "mcode", Model: "m", Verdict: "ACCEPT", Output: "clean",
	}
	verify.SaveStageCache(key, entry, false)
	if _, ok := verify.LoadStageCache(key, false); !ok {
		t.Fatal("freshly saved cache entry not loadable")
	}
	path := filepath.Join(dir, "cache", key+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), "clean", "forged", 1)
	if tampered == string(raw) {
		t.Fatal("cache entry payload not found")
	}
	if err := os.WriteFile(path, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	if hit, ok := verify.LoadStageCache(key, false); ok {
		t.Fatalf("tampered cache entry was trusted: %+v", hit)
	}
}
