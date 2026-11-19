package dispatch

import (
	"slices"
	"testing"
)

func TestNativeReviewAsksForJSON(t *testing.T) {
	wt, slot := t.TempDir(), t.TempDir()
	built, err := BuildWorkerCommand(CmdReq{Provider: "mcode", CLIPath: "/bin/mcode",
		Prompt: "note", Worktree: wt, SlotDir: slot, TaskID: "nr", ReviewMode: "native"})
	if err != nil {
		t.Fatalf("native review command failed: %v", err)
	}
	if !slices.Contains(built.Argv, "json") {
		t.Fatalf("native review must request json output: %v", built.Argv)
	}
	for i, a := range built.Argv {
		if a == "--output-format" && i+1 < len(built.Argv) && built.Argv[i+1] != "json" {
			t.Fatalf("native review output format: %v", built.Argv)
		}
	}
}
