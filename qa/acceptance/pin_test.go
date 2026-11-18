package acceptance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/UberMetroid/mcode-router/internal/envelope"
	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/worktree"
)

// TestQuotaStagePinValidatedOnRead checks that the global stage pins in the
// mutable quota file get the same read-side validation as the workspace file,
// so a forged pin cannot silently route every run to a paid lane.
func TestQuotaStagePinValidatedOnRead(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("AGENT_QUOTA_FILE", filepath.Join(tmp, "quota.json"))
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	q := fleet.DefaultQuotaData()
	if q.Stages == nil {
		t.Fatal("default quota carries no stage map")
	}
	q.Stages["act"] = fleet.StageRoute{Lane: "devin-fusion"}
	q.Stages["orient"] = fleet.StageRoute{Lane: "not-a-real-lane"}
	spec := worktree.ResolveStage("act", "", "", "", "", tmp, q)
	if spec.Lane == "devin-fusion" {
		t.Fatalf("forged paid-lane pin was honoured: %+v", spec)
	}
	spec = worktree.ResolveStage("orient", "", "", "", "", tmp, q)
	if spec.Lane == "not-a-real-lane" {
		t.Fatalf("forged lane pin was honoured: %+v", spec)
	}
}

// TestUnknownLaneRefused checks that a lane the router does not know cannot
// be dispatched, because the binary for it would come from mutable state alone.
func TestUnknownLaneRefused(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	t.Setenv("MCODE_ROUTER_SCRATCH_DIR", filepath.Join(tmp, "scratch"))
	t.Setenv("MCODE_ROUTER_SLOTS_DIR", filepath.Join(tmp, "slots"))
	t.Setenv("PATH", filepath.Join(tmp, "bin"))
	q := fleet.DefaultQuotaData()
	for _, p := range q.Providers {
		if p != nil {
			p.Status = "cooldown"
		}
	}
	q.Providers["evil"] = &fleet.ProviderConfig{CLI: "sh", Status: "active", Tier: "unlimited"}
	q.DefaultFallbackChain = []string{"evil"}
	q.ParallelStripePool = []string{"evil"}
	var asked []string
	res, err := envelope.SelectProvider(envelope.SelectReq{
		RequestedLane: "auto", Quota: q,
		ResolveCLI: func(name string) string {
			asked = append(asked, name)
			return filepath.Join(tmp, "bin", name)
		},
	})
	if err != nil {
		return
	}
	if res.Lane == "evil" {
		t.Fatalf("an unknown lane was dispatched: %+v", res)
	}
	for _, name := range asked {
		if name == "sh" {
			t.Fatal("a forged cli name was resolved for an unknown lane")
		}
	}
}

// TestForgedTierCannotBuyPaidLane checks that a forged tier in the quota
// file cannot make a paid lane look free and get it dispatched without an
// explicit --lane.
func TestForgedTierCannotBuyPaidLane(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	t.Setenv("MCODE_ROUTER_SCRATCH_DIR", filepath.Join(tmp, "scratch"))
	t.Setenv("MCODE_ROUTER_SLOTS_DIR", filepath.Join(tmp, "slots"))
	t.Setenv("PATH", filepath.Join(tmp, "bin"))
	q := fleet.DefaultQuotaData()
	for _, p := range q.Providers {
		if p != nil {
			p.Status = "cooldown"
		}
	}
	q.Providers["devin-fusion"] = &fleet.ProviderConfig{
		CLI: "devin", Status: "active", Tier: "unlimited",
	}
	q.DefaultFallbackChain = []string{"devin-fusion"}
	q.ParallelStripePool = []string{"devin-fusion"}
	res, err := envelope.SelectProvider(envelope.SelectReq{
		RequestedLane: "auto", Quota: q,
		ResolveCLI: func(name string) string { return filepath.Join(tmp, "bin", name) },
	})
	if err != nil {
		return
	}
	if res.Lane == "devin-fusion" {
		t.Fatalf("a paid lane was dispatched with a forged tier: %+v", res)
	}
}

// TestStagePinDropped checks that an invalid pin is dropped instead of being
// honoured.
func TestStagePinDropped(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("AGENT_QUOTA_FILE", filepath.Join(tmp, "quota.json"))
	cwd := initRepo(t)
	path := worktree.WorkspaceStagesPath(cwd)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"act":{"lane":"devin-fusion"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stages := worktree.LoadWorkspaceStages(cwd)
	if _, ok := stages["act"]; ok {
		t.Fatal("a paid lane pin survived read-side validation")
	}
}
