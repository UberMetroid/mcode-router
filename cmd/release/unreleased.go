package main

import (
	"fmt"
	"regexp"
)

var (
	// unreleasedSectionRe matches an open [Unreleased] heading in the changelog.
	unreleasedSectionRe = regexp.MustCompile(`(?m)^## \[Unreleased\][ \t]*$`)
)

// checkUnreleased reports a changelog whose open [Unreleased] heading is
// duplicated, or stranded below a released heading. Either way the prose under
// it belongs to no release: the bump renamed the first heading and left this
// one behind, so the entries read as pending work that already shipped.
func checkUnreleased(content string) error {
	open := unreleasedSectionRe.FindAllStringIndex(content, -1)
	if len(open) == 0 {
		return nil
	}
	if len(open) > 1 {
		return fmt.Errorf("changelog has %d [Unreleased] headings; merge them into one", len(open))
	}
	released := changelogSectionRe.FindStringIndex(content)
	if released != nil && released[0] < open[0][0] {
		return fmt.Errorf("changelog [Unreleased] heading is stranded below a released heading")
	}
	return nil
}
