package main

import "fmt"

// bumpCatalog rewrites the CLI version constant in the fleet catalog.
func bumpCatalog(content, version string) (string, error) {
	loc := catalogVersionRe.FindStringSubmatchIndex(content)
	if loc == nil {
		return "", fmt.Errorf("no Version constant found")
	}
	repl := `${1}"` + version + `"`
	return content[:loc[0]] + catalogVersionRe.ReplaceAllString(content[loc[0]:loc[1]], repl) +
		content[loc[1]:], nil
}

// bumpManifest rewrites the version field of the plugin manifest.
func bumpManifest(content, version string) (string, error) {
	loc := manifestVersionRe.FindStringSubmatchIndex(content)
	if loc == nil {
		return "", fmt.Errorf("no version field found")
	}
	repl := `${1}"` + version + `"`
	return content[:loc[0]] + manifestVersionRe.ReplaceAllString(content[loc[0]:loc[1]], repl) +
		content[loc[1]:], nil
}
