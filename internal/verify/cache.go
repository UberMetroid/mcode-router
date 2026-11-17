package verify

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

const cacheTTLSecs int64 = 21600

// CacheEntry stores a cached read-only observe, orient, or verify output.
type CacheEntry struct {
	Stage   string `json:"stage"`
	Lane    string `json:"lane"`
	Model   string `json:"model"`
	Effort  string `json:"effort,omitempty"`
	Verdict string `json:"verdict,omitempty"`
	Output  string `json:"output"`
	SavedAt int64  `json:"saved_at"`
}

func cacheEnabled(noCache bool) bool {
	return !noCache && os.Getenv("OODA_NO_CACHE") != "1" && os.Getenv("MCODE_ROUTER_NO_CACHE") != "1"
}

// CacheKey builds a content-addressed key binding stage inputs and workspace snapshot.
func CacheKey(stage, payload string, snap map[string]string) string {
	if len(snap) == 0 || snap["@ERR"] != "" {
		return ""
	}
	keys := make([]string, 0, len(snap))
	for k := range snap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	_, _ = h.Write([]byte("v1\x00" + stage + "\x00" + payload + "\x00"))
	for _, k := range keys {
		_, _ = h.Write([]byte(k + "=" + snap[k] + "\x00"))
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:32]
}

// LoadStageCache returns a fresh cached entry when enabled and within TTL.
func LoadStageCache(key string, noCache bool) (CacheEntry, bool) {
	if !cacheEnabled(noCache) || key == "" {
		return CacheEntry{}, false
	}
	path := filepath.Join(fleet.ScratchDirPath(), "cache", key+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return CacheEntry{}, false
	}
	var e CacheEntry
	if json.Unmarshal(raw, &e) != nil || e.SavedAt <= 0 || strings.TrimSpace(e.Output) == "" {
		_ = os.Remove(path)
		return CacheEntry{}, false
	}
	if age := time.Now().Unix() - e.SavedAt; age < -60 || age > cacheTTLSecs {
		_ = os.Remove(path)
		return CacheEntry{}, false
	}
	return e, true
}

func pruneStageCache(dir string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type item struct {
		name string
		mod  int64
	}
	var items []item
	for _, ent := range ents {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		if info, ierr := ent.Info(); ierr == nil {
			items = append(items, item{name: ent.Name(), mod: info.ModTime().UnixNano()})
		}
	}
	if len(items) <= 64 {
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod < items[j].mod })
	for i := 0; i < len(items)-48; i++ {
		_ = os.Remove(filepath.Join(dir, items[i].name))
	}
}

// SaveStageCache persists a verified read-only stage result and prunes stale files.
func SaveStageCache(key string, e CacheEntry, noCache bool) {
	if !cacheEnabled(noCache) || key == "" || strings.TrimSpace(e.Output) == "" {
		return
	}
	e.SavedAt = time.Now().Unix()
	dir := filepath.Join(fleet.ScratchDirPath(), "cache")
	_ = fleet.AtomicWriteJSON(filepath.Join(dir, key+".json"), e, true, true)
	pruneStageCache(dir)
}

// TryReviewCache resolves a content-addressed cache hit for an unchanged review tree.
func TryReviewCache(req ReviewReq, prompt string, snap map[string]string) (string, ReviewResult, bool) {
	pay := fmt.Sprintf("%s|%s|%s|%s|%v|%v|%s",
		req.ReqLane, req.Model, req.Effort, req.AuthorLane, req.AuthorLanes, req.Exclude, prompt)
	cKey := CacheKey("verify", pay, snap)
	hit, ok := LoadStageCache(cKey, req.NoCache)
	if !ok || hit.Verdict != "ACCEPT" {
		return cKey, ReviewResult{}, false
	}
	if req.LogWriter != nil {
		fmt.Fprintf(req.LogWriter, "▸ [OODA:ACT verify] cache=hit (%s, merkle match, 0 tokens)\n", hit.Lane)
	}
	return cKey, ReviewResult{
		Ran: true, Lane: hit.Lane, Model: hit.Model, Verdict: hit.Verdict, ExitCode: 0, Output: hit.Output,
	}, true
}

// SaveReviewCache stores verified ACCEPT review verdicts keyed by Merkle digest.
func SaveReviewCache(cKey string, req ReviewReq, lane, model, verdict string, res ReviewExecRes) {
	if res.ExitCode == 0 && !res.TimedOut && verdict == "ACCEPT" {
		SaveStageCache(cKey, CacheEntry{
			Stage: "verify", Lane: lane, Model: model, Verdict: verdict, Output: res.Output,
		}, req.NoCache)
	}
}
