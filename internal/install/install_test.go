package install

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// stageTestRepo stages the repo subset install.sh needs into a temp checkout.
func stageTestRepo(t *testing.T, srcRoot, dstRoot string) {
	t.Helper()
	pkg := filepath.Join(dstRoot, "plugins", "mcode-router")
	_ = os.MkdirAll(filepath.Join(dstRoot, "bin"), 0o755)
	_ = os.MkdirAll(filepath.Join(pkg, "config"), 0o755)
	for _, rel := range []string{"install.sh", "uninstall.sh"} {
		_ = os.WriteFile(filepath.Join(dstRoot, rel), readRaw(t, filepath.Join(srcRoot, rel)), 0o755)
	}
	qDef := filepath.Join("plugins", "mcode-router", "config", "quota.default.json")
	_ = os.WriteFile(filepath.Join(dstRoot, qDef), readRaw(t, filepath.Join(srcRoot, qDef)), 0o644)
	srcPkg := filepath.Join(srcRoot, "plugins", "mcode-router")
	for _, sub := range []string{"skills", "agents"} {
		entries, err := os.ReadDir(filepath.Join(srcPkg, sub))
		if err != nil {
			t.Fatalf("cannot read %s: %v", sub, err)
		}
		for _, entry := range entries {
			rel := filepath.Join(sub, entry.Name())
			if entry.IsDir() {
				rel = filepath.Join(sub, entry.Name(), "SKILL.md")
			}
			target := filepath.Join(pkg, rel)
			_ = os.MkdirAll(filepath.Dir(target), 0o755)
			_ = os.WriteFile(target, readRaw(t, filepath.Join(srcPkg, rel)), 0o644)
		}
	}
}

// runBash runs a script and returns its combined output.
func runBash(t *testing.T, dir string, env []string, script string, args ...string) string {
	t.Helper()
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", script, args, err, out)
	}
	return string(out)
}

// readRaw reads a file or fails the test.
func readRaw(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSetupCheckStateAndDirs(t *testing.T) {
	tmp := t.TempDir()
	home, repo := filepath.Join(tmp, "home"), filepath.Join(tmp, "repo")
	state := filepath.Join(tmp, "state", "mcode-router")
	_ = os.MkdirAll(home, 0o755)
	_ = os.MkdirAll(repo, 0o755)
	if RunSetupCmd([]string{"check-state", filepath.Dir(state), state, home, repo}, os.Stderr) != 0 {
		t.Fatal("valid check-state must pass")
	}
	sym := filepath.Join(tmp, "sym-into-repo")
	_ = os.Symlink(repo, sym)
	for _, tc := range [][]string{
		{"check-state", "rel", state, home, repo},
		{"check-state", home, home, home, repo},
		{"check-state", repo, filepath.Join(repo, "sub"), home, repo},
		{"check-state", sym, filepath.Join(sym, "s"), home, repo},
	} {
		if RunSetupCmd(tc, os.Stderr) != 1 {
			t.Fatalf("expected check-state refusal for %v", tc)
		}
	}
	goodDir := filepath.Join(tmp, "nested", "newdir")
	if RunSetupCmd([]string{"check-dirs", goodDir}, os.Stderr) != 0 {
		t.Fatal("check-dirs on a missing leaf must pass")
	}
	_ = os.MkdirAll(goodDir, 0o755)
	symDir, badSym := filepath.Join(tmp, "symdir"), filepath.Join(tmp, "badsym")
	_ = os.Symlink(goodDir, symDir)
	_ = os.Symlink(filepath.Join(tmp, "missing-target"), badSym)
	if RunSetupCmd([]string{"check-dirs", filepath.Join(symDir, "sub")}, os.Stderr) != 0 {
		t.Fatal("symlinked dir must pass check-dirs")
	}
	if RunSetupCmd([]string{"check-dirs", badSym}, os.Stderr) != 1 {
		t.Fatal("broken symlink must fail check-dirs")
	}
	blocker := filepath.Join(tmp, "blocker")
	_ = os.WriteFile(blocker, []byte("x"), 0o644)
	if RunSetupCmd([]string{"check-dirs", filepath.Join(blocker, "child")}, os.Stderr) != 1 {
		t.Fatal("file blocker must fail check-dirs")
	}
	qOK := filepath.Join(tmp, "q_ok.json")
	_ = os.WriteFile(qOK, []byte(`{"providers":{"mcode":{"status":"active"}}}`), 0o644)
	if RunSetupCmd([]string{"valid-quota", qOK}, os.Stderr) != 0 {
		t.Fatal("valid-quota must pass")
	}
	_ = os.WriteFile(qOK, []byte(`{"providers":{}}`), 0o644)
	if RunSetupCmd([]string{"valid-quota", qOK}, os.Stderr) != 1 {
		t.Fatal("empty providers must fail valid-quota")
	}
}

func TestSetupOwnedLink(t *testing.T) {
	tmp := t.TempDir()
	pDir := filepath.Join(tmp, "plugins", "mcode-router")
	_ = os.MkdirAll(filepath.Join(pDir, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(pDir, "bin", "mcode-router"), []byte("x"), 0o755)
	ownLink := filepath.Join(tmp, "own")
	brokenOwn := filepath.Join(tmp, "broken-own")
	foreignLink := filepath.Join(tmp, "foreign")
	_ = os.Symlink(filepath.Join(pDir, "bin", "mcode-router"), ownLink)
	_ = os.Symlink(filepath.Join(pDir, "bin", "missing"), brokenOwn)
	_ = os.Symlink(filepath.Join(tmp, "other"), foreignLink)
	if RunSetupCmd([]string{"owned-link", ownLink, pDir}, os.Stderr) != 0 ||
		RunSetupCmd([]string{"owned-link", brokenOwn, pDir}, os.Stderr) != 0 ||
		RunSetupCmd([]string{"owned-link", foreignLink, pDir}, os.Stderr) != 1 {
		t.Fatal("owned-link classification mismatch")
	}
}

func TestMCodeInstallAndUninstall(t *testing.T) {
	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	if _, err := os.Stat(filepath.Join(repoRoot, "bin", "mcode-router")); err != nil {
		t.Fatalf("compiled binary missing: %v", err)
	}
	tmp := t.TempDir()
	staged, fakeHome := filepath.Join(tmp, "staged-repo"), filepath.Join(tmp, "home")
	stageTestRepo(t, repoRoot, staged)
	binRel := filepath.Join("bin", "mcode-router")
	_ = os.WriteFile(filepath.Join(staged, binRel), readRaw(t, filepath.Join(repoRoot, binRel)), 0o755)
	_ = os.MkdirAll(fakeHome, 0o755)
	env := []string{"HOME=" + fakeHome, "XDG_STATE_HOME=" + filepath.Join(fakeHome, ".local", "state"),
		"MCODE_ROUTER_NO_BUILD=1", "PATH=" + os.Getenv("PATH")}
	out := runBash(t, staged, env, filepath.Join(staged, "install.sh"))
	if !strings.Contains(out, "mcode-router install complete") {
		t.Fatalf("unexpected install output: %s", out)
	}
	mcodeRoot := filepath.Join(fakeHome, ".minimax")
	for _, rel := range []string{"skills/mcode-router/SKILL.md", "agents/ooda-auto/agent.md", "mcp.json", "hooks.json"} {
		if _, err := os.Stat(filepath.Join(mcodeRoot, rel)); err != nil {
			t.Fatalf("mcode adapter missing %s: %v", rel, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(fakeHome, ".local", "bin", "mcode-router")); err != nil {
		t.Fatalf("expected CLI symlink: %v", err)
	}
	unOut := runBash(t, staged, env, filepath.Join(staged, "uninstall.sh"), "--purge")
	if !strings.Contains(unOut, "mcode-router uninstall") {
		t.Fatalf("unexpected uninstall output: %s", unOut)
	}
	for _, rel := range []string{"skills/mcode-router", "agents/ooda-auto"} {
		if _, err := os.Stat(filepath.Join(mcodeRoot, rel)); !os.IsNotExist(err) {
			t.Fatalf("mcode artifact survived uninstall: %s", rel)
		}
	}
	raw := readRaw(t, filepath.Join(mcodeRoot, "mcp.json"))
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers["mcode-router"] != nil {
		t.Fatal("mcode MCP entry survived uninstall")
	}
}

func TestMCodeRoundTrip(t *testing.T) {
	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "minimax")
	pkg := filepath.Join(repoRoot, "plugins", "mcode-router")
	if rc := RunMCodeCmd([]string{"install", "--data-dir", dataDir, "--plugin-dir", pkg}, os.Stdout, os.Stderr); rc != 0 {
		t.Fatalf("install failed rc=%d", rc)
	}
	if rc := RunMCodeCmd([]string{"status", "--data-dir", dataDir, "--plugin-dir", pkg}, os.Stdout, os.Stderr); rc != 0 {
		t.Fatalf("status failed rc=%d", rc)
	}
	if rc := RunMCodeCmd([]string{"uninstall", "--data-dir", dataDir, "--plugin-dir", pkg}, os.Stdout, os.Stderr); rc != 0 {
		t.Fatalf("uninstall failed rc=%d", rc)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "skills", "mcode-router")); !os.IsNotExist(err) {
		t.Fatalf("skills survived uninstall: %v", err)
	}
}

func TestMCodePrebuiltWithoutGo(t *testing.T) {
	repoRoot, _ := filepath.Abs(filepath.Join("..", ".."))
	binSrc := readRaw(t, filepath.Join(repoRoot, "bin", "mcode-router"))
	tmp := t.TempDir()
	noGoBin, relDir, repoDir, fakeHome := filepath.Join(tmp, "bin"), filepath.Join(tmp, "rel"),
		filepath.Join(tmp, "repo"), filepath.Join(tmp, "home")
	stageTestRepo(t, repoRoot, repoDir)
	_ = os.MkdirAll(noGoBin, 0o755)
	_ = os.MkdirAll(relDir, 0o755)
	_ = os.MkdirAll(fakeHome, 0o755)
	resolveTool := func(name string) string {
		for _, dir := range []string{"/usr/bin", "/bin"} {
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && info.Mode()&0o111 != 0 {
				return candidate
			}
		}
		found, err := exec.LookPath(name)
		if err != nil {
			return ""
		}
		return found
	}
	tools := []string{"bash", "dirname", "cat", "uname", "mktemp", "curl", "awk", "sha256sum",
		"shasum", "install", "mkdir", "chmod", "cp", "mv", "ln", "date", "readlink", "sed", "rm"}
	for _, name := range tools {
		if p := resolveTool(name); p != "" {
			_ = os.Symlink(p, filepath.Join(noGoBin, name))
		}
	}
	asset := fmt.Sprintf("mcode-router-%s-%s", runtime.GOOS, runtime.GOARCH)
	_ = os.WriteFile(filepath.Join(relDir, asset), binSrc, 0o755)
	_ = os.WriteFile(filepath.Join(relDir, "SHA256SUMS"),
		[]byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(binSrc), asset)), 0o644)
	runInst := func() error {
		cmd := exec.Command("bash", filepath.Join(repoDir, "install.sh"))
		cmd.Dir = repoDir
		cmd.Env = []string{
			"HOME=" + fakeHome, "XDG_STATE_HOME=" + filepath.Join(fakeHome, ".local", "state"),
			"PATH=" + noGoBin, "MCODE_ROUTER_RELEASE_BASE=file://" + relDir,
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, out)
		}
		return nil
	}
	if err := runInst(); err != nil {
		t.Fatalf("prebuilt no-Go install failed: %v", err)
	}
	_ = os.Remove(filepath.Join(repoDir, "bin", "mcode-router"))
	_ = os.WriteFile(filepath.Join(relDir, "SHA256SUMS"), []byte(strings.Repeat("0", 64)+"  "+asset+"\n"), 0o644)
	if err := runInst(); err == nil {
		t.Fatal("expected SHA-256 mismatch to fail closed")
	}
}
