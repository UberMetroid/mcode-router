package fleet

import (
	"os"
	"path/filepath"
	"time"
)

// CLIFallbackPaths returns default install locations for headless agent CLIs.
func CLIFallbackPaths(cliName string) []string {
	home, _ := os.UserHomeDir()
	localBin := filepath.Join(home, ".local", "bin")
	if cliName == "muse" {
		return []string{filepath.Join(localBin, cliName)}
	}
	table := map[string][]string{
		"devin": {filepath.Join(localBin, "devin"),
			filepath.Join(home, ".local", "share", "devin", "cli", "_versions", "current", "bin", "devin")},
		"mcode": {filepath.Join(home, ".minimax-code", "bin", "mcode"), filepath.Join(localBin, "mcode")},
		"grok":  {filepath.Join(localBin, "grok"), filepath.Join(home, ".grok", "bin", "grok")},
		"vibe": {filepath.Join(localBin, "vibe"),
			filepath.Join(home, ".local", "share", "uv", "tools", "mistral-vibe", "bin", "vibe")},
	}
	return table[cliName]
}

// ProviderSpecs returns static defaults keyed by canonical provider name.
func ProviderSpecs() map[string]ProviderSpec {
	w57, w247 := []string{"5h", "7d"}, []string{"24h", "7d"}
	return map[string]ProviderSpec{
		"devin-fusion": {CLI: "devin", Tier: "credits", Role: "Devin Fusion pool (paid credits; opt-in --lane only)"},
		"devin-pro": {CLI: "devin", Tier: "unlimited", CycleHours: Ptr(24.0), OuterHours: Ptr(168.0),
			Windows: w247, Role: "Heavy implementation & refactors (Devin Pro 24h daily + 7d weekly)"},
		"devin-fast": {CLI: "devin", Tier: "unlimited", CycleHours: Ptr(24.0), OuterHours: Ptr(168.0),
			Windows: w247, MaxParallel: Ptr(DevinFastMaxPar),
			Role: "Fast mechanical sweeps, tests, recon (Devin Pro 24h + 7d, max 4 parallel)"},
		"muse": {CLI: "muse", Tier: "capped", CycleHours: Ptr(5.0), OuterHours: Ptr(168.0),
			Windows: w57, Role: "Meta Muse Power (muse-ember/wonder; 5h + 7d; skips PAYG api_key)"},
		"mcode": {CLI: "mcode", Tier: "high-cap", CycleHours: Ptr(5.0), OuterHours: Ptr(168.0),
			Windows: w57, MaxParallel: Ptr(McodeMaxPar),
			Role: "Primary review + parallel coding & recon (5h + 7d, max 4 parallel)"},
		"grok": {CLI: "grok", Tier: "capped", CycleHours: Ptr(168.0), Windows: []string{"7d"},
			Role: "xAI SuperGrok coding agent (shared 7d rolling weekly pool)"},
		"mistral": {CLI: "vibe", Tier: "capped", CycleHours: Ptr(720.0), Windows: []string{"30d"},
			Role: "Mistral Vibe / GLM agent (30d monthly pool; off-peak aware for Z.ai)"},
	}
}

// LaneCLIName resolves the binary a lane is allowed to run. The lane decides,
// not the mutable quota file: a quota entry naming a different binary is
// refused rather than followed.
var LaneCLIName = func(lane, cliName string) (string, error) {
	spec, ok := ProviderSpecs()[lane]
	if !ok || spec.CLI == "" {
		// A lane the router does not know has no business running a binary
		// at all: the name would come from mutable state alone.
		return "", UsageErr("error: unknown lane %q cannot be dispatched", lane)
	}
	if cliName != "" && cliName != spec.CLI {
		return "", UsageErr("error: lane %s: cli %q does not match the canonical %q",
			lane, cliName, spec.CLI)
	}
	return spec.CLI, nil
}

// DefaultProviders builds fresh active ProviderConfig entries.
func DefaultProviders() map[string]*ProviderConfig {
	specs := ProviderSpecs()
	out := make(map[string]*ProviderConfig, len(ProviderOrder))
	for _, name := range ProviderOrder {
		sp := specs[name]
		out[name] = &ProviderConfig{
			CLI: sp.CLI, Tier: sp.Tier, Role: sp.Role, Status: "active",
			Windows: append([]string(nil), sp.Windows...), MaxParallel: sp.MaxParallel,
			CycleHours: sp.CycleHours, OuterHours: sp.OuterHours,
		}
	}
	return out
}

// DefaultQuotaData returns baseline schema-v9 quota state.
func DefaultQuotaData() *QuotaState {
	now := time.Now().Format(time.RFC3339)
	stages := make(map[string]StageRoute, len(StageNames))
	for _, st := range StageNames {
		stages[st] = StageRoute{Lane: "auto"}
	}
	return &QuotaState{
		Version: QuotaSchemaVersion, UpdatedAt: &now, DrainCappedFirst: true,
		ParallelStripePool:   append([]string(nil), StripePool...),
		DefaultFallbackChain: append([]string(nil), DefaultPool...),
		Stages:               stages, Providers: DefaultProviders(),
	}
}
