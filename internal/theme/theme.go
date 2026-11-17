// Package theme resolves the ANSI 256-color palette used by the terminal UI.
package theme

import (
	"fmt"
	"os"
	"strings"
)

// Palette holds the ANSI 256-color sequences used by the lane selector.
type Palette struct {
	Name      string
	Muted     string
	TUIAccent string
	TUIHeader string
	TUIOK     string
	FleetLow  string
}

// palette specs are muted, accent, header, ok, fleet-low color numbers.
var palettes = func() map[string]Palette {
	fg := func(n int) string { return fmt.Sprintf("\033[38;5;%dm", n) }
	bd := func(n int) string { return fmt.Sprintf("\033[1;38;5;%dm", n) }
	specs := map[string][5]int{
		"light":                     {241, 25, 90, 54, 160},
		"tokyo night":               {60, 111, 141, 149, 210},
		"solarized dark":            {242, 33, 61, 64, 160},
		"solarized light":           {244, 33, 61, 64, 160},
		"colorblind-friendly dark":  {244, 117, 176, 43, 202},
		"colorblind-friendly light": {241, 26, 90, 54, 124},
		"halloween":                 {244, 135, 196, 118, 196},
	}
	out := make(map[string]Palette, len(specs)+1)
	out["dark"] = Palette{
		Name: "dark", Muted: fg(244), TUIAccent: "\033[1;36m",
		TUIHeader: bd(117), TUIOK: bd(78), FleetLow: bd(203),
	}
	for name, spec := range specs {
		out[name] = Palette{
			Name: name, Muted: fg(spec[0]), TUIAccent: bd(spec[1]),
			TUIHeader: bd(spec[2]), TUIOK: bd(spec[3]), FleetLow: bd(spec[4]),
		}
	}
	return out
}()

// PaletteFor returns the ANSI Palette for a normalized theme name, defaulting to dark.
func PaletteFor(name string) Palette {
	key := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "_", "-")
	if strings.HasPrefix(key, "halloween") {
		key = "halloween"
	} else {
		key = strings.ReplaceAll(key, "-", " ")
		key = strings.ReplaceAll(key, "colorblind friendly", "colorblind-friendly")
		key = strings.ReplaceAll(key, "colorblind dark", "colorblind-friendly dark")
		key = strings.ReplaceAll(key, "colorblind light", "colorblind-friendly light")
	}
	if p, ok := palettes[key]; ok {
		return p
	}
	return palettes["dark"]
}

// ResolvePalette picks the palette from the env override, host payload, or terminal background.
func ResolvePalette(data map[string]any) Palette {
	return PaletteFor(resolveName(data))
}

// resolveName selects the active palette name from env, host payload, and terminal background.
func resolveName(data map[string]any) string {
	pick := strings.ToLower(strings.TrimSpace(os.Getenv("MCODE_ROUTER_THEME")))
	if pick == "" && data != nil {
		for _, key := range []string{"colorScheme", "scheme", "theme"} {
			if s, _ := data[key].(string); strings.TrimSpace(s) != "" {
				pick = strings.ToLower(strings.TrimSpace(s))
				break
			}
		}
	}
	if pick == "" || pick == "terminal" || pick == "auto" {
		if fgbg := strings.TrimSpace(os.Getenv("COLORFGBG")); fgbg != "" {
			parts := strings.Split(fgbg, ";")
			if bg := strings.TrimSpace(parts[len(parts)-1]); bg == "7" || bg == "15" {
				return "light"
			}
		}
		return "dark"
	}
	return PaletteFor(pick).Name
}
