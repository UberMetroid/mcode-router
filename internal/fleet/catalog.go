// Package fleet manages provider quotas, aliases, paths, and slot leases.
package fleet

import (
	"fmt"
	"os"
	"regexp"
)

// ExitError carries an explicit process exit code for CLI error returns.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string {
	return e.Msg
}

// UsageErr builds a code-2 usage/validation ExitError.
func UsageErr(format string, args ...any) error {
	return &ExitError{Code: 2, Msg: fmt.Sprintf(format, args...)}
}

const (
	Version            = "1.0.19"
	QuotaSchemaVersion = 9
	CornerWindowSecs   = 5400
	SoonWakeSecs       = 2700
	DevinFastMaxPar    = 4
	McodeMaxPar        = 4
	DefaultJobs        = 4
	MaxJobs            = 8
	MaxParallelCap     = 32
	MaxHoursCap        = 1080.0
	DefaultTimeout     = 480
	ReviewTimeout      = 240
	DefaultFusionModel = "fusion-claude-opus-5-5-high-sidekick-swe-2-medium"
	ReconPrefix        = "READ-ONLY RECONNAISSANCE: Do NOT modify, create, or delete files, and do NOT run builds or tests. " +
		"Answer concisely with exact file paths, line numbers, and findings:\n\n"
	OrientPrefix = "READ-ONLY ORIENT & ARCHITECTURE: Do NOT modify, create, or delete files, and do NOT run builds or tests. " +
		"Inspect only relevant files and propose a concise root-cause & step-by-step plan:\n\n"
)

var (
	// GeminiModelRe blocks Gemini/Orchestrator models on worker lanes.
	GeminiModelRe = regexp.MustCompile(`(?i)(?:gemini|orchestrator)`)
	SafeIDRe      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	DurationRe    = regexp.MustCompile(`(?i)^(\d+(?:\.\d+)?)\s*([smhd]?)$`)
	ResetTSRe     = regexp.MustCompile(
		`(?i)(?:resets?[\s_-]*at|try\s+again\s+at|available\s+at|until)(?:["']?\s*:\s*["']?|\s+)` +
			`(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2}))`,
	)
	VerdictRe = regexp.MustCompile(
		`(?i)^\s*(?:\*\*|__)?VERDICT(?:\*\*|__)?\s*:\s*(?:\*\*|__)?\s*(ACCEPT|REJECT)\b`,
	)
)

var (
	CoreHiddenSkills = map[string]bool{
		"mcode-router": true, "mcode-router:mcode-router": true, "lane": true, "mcode-router:lane": true,
		"harden": true, "mcode-router:harden": true, "router": true, "agent-router": true,
		"ooda": true, "ooda:ooda": true, "ooda:router": true,
		"run-workflow": true, "swarm": true, "swarm_monitor": true,
	}
	StageNames  = []string{"observe", "orient", "act", "verify"}
	DefaultPool = []string{
		"muse", "grok", "mistral",
		"devin-fast", "devin-pro", "mcode",
	}
	StripePool = []string{
		"muse", "grok", "mistral",
		"devin-fast", "devin-pro", "mcode",
	}
	FastOrder = []string{"devin-fast", "mcode", "devin-pro", "muse"}
	DeepOrder = []string{
		"muse", "grok", "mistral",
		"devin-pro", "devin-fast", "mcode",
	}
	ReviewOrder = []string{
		"mcode", "devin-fast", "devin-pro", "grok", "mistral", "muse",
	}
	ProviderOrder = []string{
		"devin-fusion", "devin-pro", "devin-fast",
		"muse", "mcode", "grok", "mistral",
	}
	ModelFamilies = map[string]string{
		"devin-fusion": "claude-opus",
		"devin-pro":    "swe-2", "devin-fast": "swe-2",
		"muse": "muse", "mcode": "minimax",
		"grok": "grok", "mistral": "glm",
	}
	FastComplexities = map[string]bool{
		"fast": true, "mechanical": true, "simple": true, "flash": true,
		"tests": true, "test": true, "docs": true, "doc": true, "recon": true,
	}
	DeepComplexities = map[string]bool{
		"deep": true, "architecture": true, "arch": true,
		"reasoning": true, "audit": true, "ember": true,
	}
	RegimeTimeouts = map[string]int{
		"observe": 180, "fast": 300, "act": 480,
		"heavy": 600, "deep": 900, "verify": 240,
	}
)

// ProviderSpec captures static baseline defaults for a fleet lane.
type ProviderSpec struct {
	CLI, Tier, Role string
	Windows         []string
	CycleHours      *float64
	OuterHours      *float64
	MaxParallel     *int
}

// ProviderConfig mirrors one provider entry in quota.json.
type ProviderConfig struct {
	CLI              string   `json:"cli"`
	Model            *string  `json:"model"`
	Effort           *string  `json:"effort"`
	Tier             string   `json:"tier"`
	Role             string   `json:"role"`
	Status           string   `json:"status"`
	Windows          []string `json:"windows,omitempty"`
	CooldownUntil    *string  `json:"cooldown_until"`
	Reason           *string  `json:"reason"`
	ManualDisable    bool     `json:"manual_disable"`
	MaxParallel      *int     `json:"max_parallel"`
	CycleHours       *float64 `json:"cycle_hours"`
	CycleStartedAt   *string  `json:"cycle_started_at"`
	NextResetAt      *string  `json:"next_reset_at"`
	OuterHours       *float64 `json:"outer_hours,omitempty"`
	OuterStartedAt   *string  `json:"outer_started_at,omitempty"`
	OuterResetAt     *string  `json:"outer_reset_at,omitempty"`
	CycleRuns        int      `json:"cycle_runs"`
	CycleTokens      int      `json:"cycle_tokens"`
	OuterTokens      int      `json:"outer_tokens,omitempty"`
	CycleTokensExact bool     `json:"cycle_tokens_exact"`
	CycleCostUSD     float64  `json:"cycle_cost_usd"`
	CycleTokenBudget *int     `json:"cycle_token_budget"`
	CycleCostBudget  *float64 `json:"cycle_cost_budget_usd"`
	ConsecCooldowns  int      `json:"consec_cooldowns,omitempty"`
	LastProbedAt     *string  `json:"last_probed_at,omitempty"`
	HealthEWMA       *float64 `json:"health_ewma,omitempty"`
}

// StageRoute pins or defaults one OODA stage (observe, orient, act, verify).
type StageRoute struct {
	Lane      string  `json:"lane"`
	Model     *string `json:"model,omitempty"`
	Effort    *string `json:"effort,omitempty"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

// QuotaState mirrors the top-level quota.json state schema.
type QuotaState struct {
	Version              int                        `json:"version"`
	UpdatedAt            *string                    `json:"updated_at"`
	DrainCappedFirst     bool                       `json:"drain_capped_first"`
	ParallelStripePool   []string                   `json:"parallel_stripe_pool"`
	DefaultFallbackChain []string                   `json:"default_fallback_chain"`
	Stages               map[string]StageRoute      `json:"stages,omitempty"`
	Providers            map[string]*ProviderConfig `json:"providers"`
}

// ReviewModelEnv returns the env-configured auto-review model or cli-default.
func ReviewModelEnv() string {
	if v := os.Getenv("AGENT_REVIEW_MODEL"); v != "" {
		return v
	}
	if v := os.Getenv("OODA_REVIEW_MODEL"); v != "" {
		return v
	}
	return "cli-default"
}

// Ptr returns a heap-allocated pointer to v for optional JSON fields.
func Ptr[T any](v T) *T {
	return &v
}
