// MCode plugin package validation for the MiniMax Code host adapter.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	mcodeNameRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	mcodeIconRe = regexp.MustCompile(`\.(png|jpg|jpeg|webp)$`)
	mcodeCats   = map[string]bool{
		"Office": true, "Studio": true, "Design & Sites": true, "Code": true,
		"Business": true, "Sales": true, "Productivity": true,
		"Science & Healthcare": true, "Education": true, "Other": true,
	}
)

// checkMCodePlugin validates plugins/mcode-router against host packaging rules.
func checkMCodePlugin(root string) []string {
	dir := filepath.Join(root, "plugins", "mcode-router")
	manifestPath := filepath.Join(dir, ".minimax-plugin", "plugin.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return []string{"plugins/mcode-router: manifest not readable: " + err.Error()}
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return []string{"plugins/mcode-router: manifest is not a JSON object: " + err.Error()}
	}
	var viols []string
	viols = append(viols, mcodeManifestFields(manifest)...)
	viols = append(viols, mcodeIconField(dir, manifest)...)
	viols = append(viols, mcodeRefFiles(dir, manifest, "skills")...)
	viols = append(viols, mcodeRefFiles(dir, manifest, "mcpServers")...)
	viols = append(viols, mcodeRefFiles(dir, manifest, "agents")...)
	viols = append(viols, mcodeSkillFrontmatter(dir, manifest)...)
	viols = append(viols, mcodePackageHygiene(dir)...)
	return viols
}

// mcodeManifestFields checks required manifest scalars and enumerations.
func mcodeManifestFields(manifest map[string]any) []string {
	var viols []string
	if version, _ := manifest["schemaVersion"].(float64); version != 1 {
		viols = append(viols, "plugin.json: schemaVersion must be 1")
	}
	name, _ := manifest["name"].(string)
	if name == "" || len(name) > 80 || !mcodeNameRe.MatchString(name) {
		viols = append(viols, "plugin.json: name must be lowercase kebab-case and <= 80 chars")
	}
	for _, key := range []string{"version", "description", "author"} {
		if value, _ := manifest[key].(string); strings.TrimSpace(value) == "" {
			viols = append(viols, "plugin.json: "+key+" must be a non-empty string")
		}
	}
	category, _ := manifest["category"].(string)
	if !mcodeCats[category] {
		viols = append(viols, "plugin.json: category must be one of the fixed host categories")
	}
	queries := mcodeList(manifest["exampleQueries"])
	if len(queries) > 3 {
		viols = append(viols, "plugin.json: exampleQueries allows at most 3 entries")
	}
	for _, key := range []string{"apps", "mcpServers", "skills"} {
		if _, ok := manifest[key].([]any); !ok {
			viols = append(viols, "plugin.json: "+key+" must be an array")
		}
	}
	return viols
}

// mcodeIconField checks the icon reference and that darkIcon is absent.
func mcodeIconField(dir string, manifest map[string]any) []string {
	var viols []string
	if _, present := manifest["darkIcon"]; present {
		viols = append(viols, "plugin.json: darkIcon must be omitted entirely")
	}
	icon, _ := manifest["icon"].(string)
	if icon == "" {
		viols = append(viols, "plugin.json: icon is required")
		return viols
	}
	if !mcodeIconRe.MatchString(icon) || icon != strings.ToLower(icon) {
		viols = append(viols, "plugin.json: icon must be a lowercase .png/.jpg/.jpeg/.webp path")
	}
	if _, err := os.Stat(filepath.Join(dir, icon)); err != nil {
		viols = append(viols, "plugin.json: icon file not found: "+icon)
	}
	return viols
}

// mcodeRefFiles checks that every declared capability file exists and is shaped
// the way the host loader expects.
func mcodeRefFiles(dir string, manifest map[string]any, key string) []string {
	var viols []string
	for _, raw := range mcodeList(manifest[key]) {
		rel, ok := raw.(string)
		if !ok || strings.TrimSpace(rel) == "" {
			viols = append(viols, "plugin.json: "+key+" entries must be non-empty strings")
			continue
		}
		if strings.HasPrefix(rel, "/") || strings.Contains(rel, "..") {
			viols = append(viols, "plugin.json: "+key+" path must be package-relative: "+rel)
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			viols = append(viols, "plugin.json: declared "+key+" file not found: "+rel)
			continue
		}
		viols = append(viols, mcodeRefShape(key, rel)...)
	}
	return viols
}

// mcodeRefShape checks per-capability path conventions.
func mcodeRefShape(key, rel string) []string {
	var viols []string
	switch key {
	case "skills":
		if !strings.HasPrefix(rel, "skills/") || !strings.HasSuffix(rel, "/SKILL.md") {
			viols = append(viols, "plugin.json: skill path must be skills/<name>/SKILL.md: "+rel)
		}
	case "mcpServers":
		if !strings.HasSuffix(rel, ".mcp.json") {
			viols = append(viols, "plugin.json: mcpServers path must end with .mcp.json: "+rel)
		}
	case "agents":
		if !strings.HasSuffix(rel, ".md") {
			viols = append(viols, "plugin.json: agents path must end with .md: "+rel)
		}
	}
	return viols
}

// mcodeSkillFrontmatter checks that each skill declares a name and description.
func mcodeSkillFrontmatter(dir string, manifest map[string]any) []string {
	var viols []string
	for _, raw := range mcodeList(manifest["skills"]) {
		rel, ok := raw.(string)
		if !ok {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		text := string(body)
		if !strings.HasPrefix(text, "---\n") {
			viols = append(viols, rel+": missing YAML frontmatter")
			continue
		}
		parts := strings.SplitN(text, "\n---\n", 2)
		if len(parts) < 2 {
			viols = append(viols, rel+": unterminated YAML frontmatter")
			continue
		}
		head := parts[0]
		if !strings.Contains(head, "name:") {
			viols = append(viols, rel+": frontmatter needs a name")
		}
		if !strings.Contains(head, "description:") {
			viols = append(viols, rel+": frontmatter needs a description")
		}
	}
	return viols
}

// mcodePackageHygiene rejects symlinks, executable-bit files and secret files.
func mcodePackageHygiene(dir string) []string {
	var viols []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		if info.Mode()&os.ModeSymlink != 0 {
			viols = append(viols, rel+": symlinks are not accepted in a plugin package")
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if info.Mode()&0o111 != 0 {
			viols = append(viols, rel+": files depending on the executable bit are not accepted")
		}
		lower := strings.ToLower(rel)
		if strings.HasPrefix(lower, ".env") || strings.HasSuffix(lower, ".pem") ||
			strings.HasSuffix(lower, ".key") {
			viols = append(viols, rel+": secret-bearing files must not ship in a plugin package")
		}
		return nil
	})
	if err != nil {
		viols = append(viols, "plugins/mcode-router: package walk failed: "+err.Error())
	}
	return viols
}

// mcodeList coerces a manifest array field into a slice.
func mcodeList(value any) []any {
	items, _ := value.([]any)
	return items
}
