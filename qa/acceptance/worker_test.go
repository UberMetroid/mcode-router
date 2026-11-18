package acceptance

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/UberMetroid/mcode-router/internal/dispatch"
)

// TestWorkerEnvForeignCreds checks that a write-mode worker does not inherit
// the credentials of providers other than the one it runs.
func TestWorkerEnvForeignCreds(t *testing.T) {
	t.Setenv("XAI_API_KEY", "foreign-value-1234")
	t.Setenv("MUSE_TOKEN", "foreign-value-1234")
	t.Setenv("MINIMAX_API_KEY", "own-value-1234")
	built, err := dispatch.BuildWorkerCommand(dispatch.CmdReq{
		Provider: "mcode", CLIPath: "/bin/mcode", Model: "m", Prompt: "fix",
		TaskID: "t1", SlotDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"XAI_API_KEY=", "MUSE_TOKEN="} {
		if slices.ContainsFunc(built.Env, func(s string) bool { return strings.HasPrefix(s, bad) }) {
			t.Fatalf("write worker inherited %s", bad)
		}
	}
	if !slices.ContainsFunc(built.Env, func(s string) bool {
		return strings.HasPrefix(s, "MINIMAX_API_KEY=")
	}) {
		t.Fatalf("write worker lost its own lane credential: %v", built.Env)
	}
}

// TestMcodePromptNotInArgv checks that the worker prompt travels over stdin
// rather than on argv, where any local user could read it from /proc, and
// that a prompt shaped like an option is never read as one.
func TestMcodePromptNotInArgv(t *testing.T) {
	for _, prompt := range []string{"CANARY-PROMPT", "--danger"} {
		slot := t.TempDir()
		built, err := dispatch.BuildWorkerCommand(dispatch.CmdReq{
			Provider: "mcode", CLIPath: "/bin/mcode", Model: "m", Prompt: prompt,
			TaskID: "t1", SlotDir: slot,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, arg := range built.Argv {
			if strings.Contains(arg, prompt) {
				t.Fatalf("prompt %q exposed on argv: %v", prompt, built.Argv)
			}
		}
		if !slices.Contains(built.Argv, "-") {
			t.Fatalf("stdin transport not requested: %v", built.Argv)
		}
	}
}

// TestVibeExportBounded checks that an oversized worker export is refused
// instead of being read into memory whole.
func TestVibeExportBounded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "export.json")
	big := `{"usage":{"total_tokens":7},"pad":"` + strings.Repeat("a", 2<<20) + `"}`
	if err := os.WriteFile(path, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	if usage := dispatch.ParseVibeExport(dir); usage != nil {
		t.Fatalf("oversize export accepted: %+v", usage)
	}
	small := `{"usage":{"total_tokens":7}}`
	if err := os.WriteFile(path, []byte(small), 0o600); err != nil {
		t.Fatal(err)
	}
	if usage := dispatch.ParseVibeExport(dir); usage == nil {
		t.Fatal("normal export not parsed")
	}
}
