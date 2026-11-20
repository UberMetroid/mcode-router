package main

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// changelogSectionRe matches a release heading in the changelog.
	changelogSectionRe = regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\]`)
	// changelogLinkRe matches a release link reference in the changelog.
	changelogLinkRe = regexp.MustCompile(`(?m)^\[(\d+\.\d+\.\d+)\]: (\S+)$`)
)

// bumpChangelog adds the release heading and its link reference.
func bumpChangelog(content, version, date string) (string, error) {
	out := content
	if !hasChangelogSection(out, version) {
		out = insertChangelogSection(out, version, date)
	}
	if !hasChangelogLink(out, version) {
		out = insertChangelogLink(out, version)
	}
	return out, nil
}

// hasChangelogSection reports whether the changelog already has the release.
func hasChangelogSection(content, version string) bool {
	for _, m := range changelogSectionRe.FindAllStringSubmatch(content, -1) {
		if m[1] == version {
			return true
		}
	}
	return false
}

// hasChangelogLink reports whether the changelog links the release.
func hasChangelogLink(content, version string) bool {
	for _, m := range changelogLinkRe.FindAllStringSubmatch(content, -1) {
		if m[1] == version {
			return true
		}
	}
	return false
}

// insertChangelogSection renames the open [Unreleased] heading into the new
// release, so the cycle's prose lands in the release it was written for, and
// falls back to putting a new release heading above the newest one when no
// open heading exists.
func insertChangelogSection(content, version, date string) string {
	heading := fmt.Sprintf("## [%s] — %s", version, date)
	open := unreleasedSectionRe.FindStringIndex(content)
	released := changelogSectionRe.FindStringIndex(content)
	if open != nil && (released == nil || open[0] < released[0]) {
		return content[:open[0]] + heading + content[open[1]:]
	}
	block := heading + "\n\n"
	if released == nil {
		return appendBlock(content, block)
	}
	return content[:released[0]] + block + content[released[0]:]
}

// insertChangelogLink puts a new release link above the older ones.
func insertChangelogLink(content, version string) string {
	block := fmt.Sprintf("[%s]: https://github.com/UberMetroid/mcode-router/releases/tag/v%s\n",
		version, version)
	target, err := parseVersion(version)
	if err != nil {
		return content
	}
	for _, m := range changelogLinkRe.FindAllStringSubmatchIndex(content, -1) {
		other, perr := parseVersion(content[m[2]:m[3]])
		if perr != nil {
			continue
		}
		if versionLess(other, target) {
			return content[:m[0]] + block + content[m[0]:]
		}
	}
	return appendBlock(content, block)
}

// findChangelogSection locates a release heading and the text under it.
func findChangelogSection(content, version string) (int, int, bool) {
	all := changelogSectionRe.FindAllStringSubmatchIndex(content, -1)
	for i, m := range all {
		if content[m[2]:m[3]] != version {
			continue
		}
		end := len(content)
		if i+1 < len(all) {
			end = all[i+1][0]
		}
		return m[0], end, true
	}
	return 0, 0, false
}

// changelogBodyEmpty reports whether a release heading has no prose under it.
func changelogBodyEmpty(content, version string) bool {
	start, end, found := findChangelogSection(content, version)
	if !found {
		return true
	}
	for _, ln := range strings.Split(content[start:end], "\n")[1:] {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" {
			continue
		}
		return strings.HasPrefix(trimmed, "## ")
	}
	return true
}

// readChangelogVersion extracts the newest release link version.
func readChangelogVersion(path string) (string, error) {
	content, err := readText(path)
	if err != nil {
		return "", err
	}
	best := ""
	for _, m := range changelogLinkRe.FindAllStringSubmatch(content, -1) {
		if best == "" {
			best = m[1]
			continue
		}
		a, err1 := parseVersion(best)
		b, err2 := parseVersion(m[1])
		if err1 != nil || err2 != nil {
			continue
		}
		if versionLess(a, b) {
			best = m[1]
		}
	}
	if best == "" {
		return "", fmt.Errorf("no release link in %s", path)
	}
	return best, nil
}
