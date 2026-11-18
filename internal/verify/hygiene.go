package verify

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/worktree"
)

const hygieneMaxBytes = 1 << 20

// HygieneFinding records one deterministic credential or hardening hygiene issue.
type HygieneFinding struct {
	Severity, Rule, File, Detail string
	Line                         int
}

var (
	placeholderRe   = regexp.MustCompile(`(?i)(?:EXAMPLE|DUMMY|REDACTED|FAKE)`)
	hunkHdrRe       = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)
	curlWgetRe      = regexp.MustCompile(`\b(?:cu` + `rl|wg` + `et)\b`)
	httpFetchRe     = regexp.MustCompile(`\bht` + `tp://([^\s/"')` + "`" + `]+)`)
	skipHygieneDirs = map[string]bool{".git": true, ".worktrees": true, "bin": true, "node_modules": true}
	loopbackHosts   = map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true, "0.0.0.0": true}
	secretNames     = map[string]bool{".env": true, "id_rsa": true, "id_ed25519": true, "id_ecdsa": true}
	p1SkipExt       = map[string]bool{".md": true, ".txt": true, ".rst": true, ".html": true, ".svg": true, ".css": true}
	p1SkipSuf       = []string{"_test.go", "_test.py", ".test.js", ".test.ts", ".spec.js", ".spec.ts"}
	pkPat           = "-----BEGIN " + "(?:RSA |EC |OPENSSH |DSA |ENCRYPTED )?" + "PRIVATE " + "KEY-----"
	akPat           = `\bs` + `k-[A-Za-z0-9_-]{20,}|\bxo` + `x[baprs]-[A-Za-z0-9-]{16,}|\bAK` + `IA[0-9A-Z]{16}\b`
	pipePat         = `\b(?:cu` + `rl|wg` + `et)\b[^|\n]*\|\s*(?:sudo\s+)?(?:ba|z|da|k)?sh\b`
	urlCredPat      = `(?i)\b(?:postgres|postgresql|mysql|mongodb|redis|amqp|ftp|sftp)` +
		`://[^:\s/]+:[^@\s/]+@`
	credAssignPat = `(?i)\b(?:pass(?:word|wd)?|secret|api[_-]?key|auth[_-]?token|` +
		`access[_-]?token|client[_-]?secret)\s*[:=]\s*["']?[^"'\s]{6,}`
	p0Rules = []struct {
		name string
		re   *regexp.Regexp
	}{
		{"private-key", regexp.MustCompile(pkPat)},
		{"github-token", regexp.MustCompile("gh" + "(?:p_|[ousr]_)[A-Za-z0-9]{20,}|github" + "_pat_[A-Za-z0-9_]{20,}")},
		{"api-key", regexp.MustCompile(akPat)},
		{"url-credential", regexp.MustCompile(urlCredPat)},
		{"password-assignment", regexp.MustCompile(credAssignPat)},
		{"gitlab-token", regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{16,}`)},
		{"npm-token", regexp.MustCompile(`\bnpm_[A-Za-z0-9]{30,}`)},
		{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,}`)},
		{"aws-secret-key", regexp.MustCompile(`(?i)\baws_secret_access_key\s*[:=]\s*["']?[A-Za-z0-9/+=]{40}`)},
	}
	p1Rules = []struct {
		name, detail string
		re           *regexp.Regexp
	}{
		{"world-writable-mode", "world-writable permission bits", regexp.MustCompile(`\b0o?(?:777|666)\b`)},
		{"tls-skip-verify", "TLS verification disabled", regexp.MustCompile("InsecureSkip" + `Verify\s*:\s*true`)},
		{"pipe-to-shell", "remote script piped directly to shell", regexp.MustCompile(pipePat)},
	}
)

func isSecretPath(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	if placeholderRe.MatchString(base) {
		return false
	}
	return secretNames[base] || base == "credentials.json" ||
		strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") ||
		(strings.HasPrefix(base, ".env.") && !strings.HasSuffix(base, ".example") &&
			!strings.HasSuffix(base, ".sample") && !strings.HasSuffix(base, ".template"))
}

func p1SkipFile(rel string) bool {
	base, ext := strings.ToLower(filepath.Base(rel)), strings.ToLower(filepath.Ext(rel))
	for _, suf := range p1SkipSuf {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	return p1SkipExt[ext] || (strings.HasPrefix(base, "test_") && ext == ".py") ||
		base == "license" || base == "notice" || base == "changelog" || strings.HasPrefix(base, "license.")
}

func scanLine(file string, ln int, text string, withP1 bool) []HygieneFinding {
	var out []HygieneFinding
p0Loop:
	for _, r := range p0Rules {
		for _, m := range r.re.FindAllString(text, -1) {
			if placeholderRe.MatchString(m) || (r.name == "private-key" && placeholderRe.MatchString(text)) {
				continue
			}
			det := r.name + " pattern detected"
			if len(m) >= 6 && r.name != "private-key" {
				det = r.name + " (" + m[:4] + "*** redacted)"
			}
			out = append(out, HygieneFinding{"P0", r.name, file, det, ln})
			break p0Loop
		}
	}
	trim := strings.TrimSpace(text)
	if low := strings.ToLower(trim); !withP1 || trim == "" || strings.HasPrefix(trim, "#") ||
		strings.HasPrefix(trim, "//") || strings.HasPrefix(low, "usage:") || strings.Contains(low, "hygiene:allow") {
		return out
	}
	for _, r := range p1Rules {
		if r.re.MatchString(text) {
			out = append(out, HygieneFinding{"P1", r.name, file, r.detail, ln})
		}
	}
	if curlWgetRe.MatchString(text) {
		for _, m := range httpFetchRe.FindAllStringSubmatch(text, -1) {
			rawH := strings.ToLower(m[1])
			host, _, ok := strings.Cut(strings.TrimPrefix(rawH, "["), "]")
			if !ok {
				host, _, _ = strings.Cut(rawH, ":")
			}
			if !loopbackHosts[host] {
				out = append(out, HygieneFinding{"P1", "insecure-http-fetch", file, "non-loopback HTTP (" + host + ")", ln})
				break
			}
		}
	}
	return out
}

func scanDiffSection(part string, seen, deleted map[string]bool) []HygieneFinding {
	lines := strings.Split(strings.ReplaceAll(part, "\r\n", "\n"), "\n")
	filePath, isDel, inHunk, curLn, oldLn, out := "", false, false, 0, 0, []HygieneFinding(nil)
	if idx := strings.LastIndex(lines[0], " b/"); idx >= 0 {
		filePath, _ = worktree.NormalizeRelPath(strings.TrimSpace(lines[0][idx+3:]))
	}
	for _, ln := range lines[1:] {
		if !inHunk && (strings.HasPrefix(ln, "deleted file mode ") || ln == "+++ /dev/null") {
			isDel = true
		} else if !inHunk && strings.HasPrefix(ln, "+++ b/") {
			filePath, _ = worktree.NormalizeRelPath(strings.TrimSpace(ln[6:]))
		} else if m := hunkHdrRe.FindStringSubmatch(ln); len(m) == 3 {
			oldLn, _ = strconv.Atoi(m[1])
			curLn, _ = strconv.Atoi(m[2])
			inHunk = true
		} else if inHunk && strings.HasPrefix(ln, "+") {
			out = append(out, scanLine(filePath, curLn, ln[1:], false)...)
			curLn++
		} else if inHunk && strings.HasPrefix(ln, " ") {
			// Context lines travel to the reviewer too, so a credential on an
			// unchanged line crosses the same boundary as one on an added line.
			out = append(out, scanLine(filePath, curLn, ln[1:], false)...)
			curLn++
			oldLn++
		} else if inHunk && strings.HasPrefix(ln, "-") {
			// A removed line still rides along in the patch that reaches the
			// reviewer, so deleting a credential does not un-leak it. It is
			// numbered against the old file, which is where it used to live.
			out = append(out, scanLine(filePath, oldLn, ln[1:], false)...)
			oldLn++
		}
	}
	if filePath != "" && isDel {
		deleted[filePath] = true
		return out
	}
	if filePath != "" {
		seen[filePath] = true
		if isSecretPath(filePath) {
			out = append([]HygieneFinding{{"P0", "secret-file", filePath, "sensitive credential filename", 0}}, out...)
		}
	}
	return out
}

// CheckDiffHygiene scans added lines and filenames in dc for P0 secrets and credentials.
func CheckDiffHygiene(dc DiffContext) []HygieneFinding {
	seen, deleted, out := make(map[string]bool), make(map[string]bool), []HygieneFinding(nil)
	for _, part := range strings.Split("\n"+dc.Diff, "\ndiff --git ")[1:] {
		out = append(out, scanDiffSection(part, seen, deleted)...)
	}
	for _, rel := range dc.ChangedFiles {
		if n, err := worktree.NormalizeRelPath(rel); err == nil && n != "" && !deleted[n] && !seen[n] && isSecretPath(n) {
			out = append(out, HygieneFinding{"P0", "secret-file", n, "sensitive credential filename", 0})
		}
	}
	return out
}

func listHygieneFiles(root string, files []string) ([]string, error) {
	var cands, kept []string
	if fleet.TryRepoRoot(root) != "" {
		raw, ok := gitOutOK(root, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", ".")
		if !ok {
			return nil, fmt.Errorf("hygiene: git ls-files failed in %s", root)
		}
		cands = strings.Split(raw, "\x00")
	} else if err := filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() && p != root && skipHygieneDirs[d.Name()] {
			return filepath.SkipDir
		}
		if rel, rerr := filepath.Rel(root, p); !d.IsDir() && rerr == nil && rel != "." {
			cands = append(cands, filepath.ToSlash(rel))
		}
		return nil
	}); err != nil {
		return nil, err
	}
	for _, raw := range cands {
		rel, err := worktree.NormalizeRelPath(raw)
		if err != nil || rel == "" || worktree.IsIgnoredArtifact(rel) ||
			(len(files) > 0 && len(worktree.FindScopeViolations([]string{rel}, files)) > 0) {
			continue
		}
		if kept = append(kept, rel); len(kept) > maxSnapshotFiles {
			return nil, fmt.Errorf("hygiene: file count exceeds limit %d", maxSnapshotFiles)
		}
	}
	sort.Strings(kept)
	return kept, nil
}

// ScanRepoHygiene performs a bounded deterministic P0 secret + P1 hardening scan.
func ScanRepoHygiene(root string, files []string) ([]HygieneFinding, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	list, err := listHygieneFiles(absRoot, files)
	if err != nil {
		return nil, err
	}
	var out []HygieneFinding
	for _, rel := range list {
		full := filepath.Join(absRoot, filepath.FromSlash(rel))
		if st, lerr := os.Lstat(full); lerr != nil || !st.Mode().IsRegular() {
			continue
		}
		if isSecretPath(rel) {
			out = append(out, HygieneFinding{"P0", "secret-file", rel, "sensitive credential filename", 0})
		}
		raw, rerr := readBounded(full, hygieneMaxBytes)
		if rerr != nil || bytes.IndexByte(raw[:min(len(raw), sniffBytes)], 0) >= 0 {
			continue
		}
		for idx, ln := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
			out = append(out, scanLine(rel, idx+1, ln, !p1SkipFile(rel))...)
		}
	}
	return out, nil
}

// FormatHygieneReport renders deterministic human-readable findings output.
func FormatHygieneReport(findings []HygieneFinding) string {
	if len(findings) == 0 {
		return "hygiene: clean (0 findings)"
	}
	cp := append([]HygieneFinding(nil), findings...)
	sort.Slice(cp, func(i, j int) bool {
		return cp[i].Severity < cp[j].Severity || (cp[i].Severity == cp[j].Severity &&
			(cp[i].File < cp[j].File || (cp[i].File == cp[j].File && cp[i].Line < cp[j].Line)))
	})
	var b strings.Builder
	for _, f := range cp {
		loc := f.File
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		fmt.Fprintf(&b, "%s [%s] %s — %s\n", loc, f.Severity, f.Rule, f.Detail)
	}
	return strings.TrimRight(b.String(), "\n")
}
