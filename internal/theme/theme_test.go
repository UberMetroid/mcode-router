package theme

import (
	"os"
	"strings"
	"testing"
)

func TestPaletteForNormalizesNames(t *testing.T) {
	cases := map[string]string{
		"light":                    "light",
		"LIGHT":                    "light",
		"tokyo_night":              "tokyo night",
		"tokyo-night":              "tokyo night",
		"colorblind_friendly_dark": "colorblind-friendly dark",
		"halloween":                "halloween",
		"unknown-theme":            "dark",
		"":                         "dark",
	}
	for input, want := range cases {
		if got := PaletteFor(input).Name; got != want {
			t.Fatalf("PaletteFor(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestPaletteFieldsAreSet(t *testing.T) {
	for _, name := range []string{"dark", "light", "tokyo night", "halloween"} {
		p := PaletteFor(name)
		if p.Muted == "" || p.TUIAccent == "" || p.TUIHeader == "" || p.TUIOK == "" || p.FleetLow == "" {
			t.Fatalf("palette %q has empty fields: %+v", name, p)
		}
		if !strings.HasPrefix(p.TUIHeader, "\033[") || !strings.HasSuffix(p.TUIHeader, "m") {
			t.Fatalf("palette %q header is not an ANSI sequence: %q", name, p.TUIHeader)
		}
	}
}

func TestResolvePaletteEnvTerminal(t *testing.T) {
	t.Setenv("MCODE_ROUTER_THEME", "light")
	if got := ResolvePalette(nil).Name; got != "light" {
		t.Fatalf("env override ignored: %q", got)
	}
	t.Setenv("MCODE_ROUTER_THEME", "")
	t.Setenv("COLORFGBG", "0;15")
	if got := ResolvePalette(nil).Name; got != "light" {
		t.Fatalf("light terminal background ignored: %q", got)
	}
	t.Setenv("COLORFGBG", "15;0")
	if got := ResolvePalette(nil).Name; got != "dark" {
		t.Fatalf("dark terminal background ignored: %q", got)
	}
	_ = os.Unsetenv("COLORFGBG")
	if got := ResolvePalette(nil).Name; got != "dark" {
		t.Fatalf("default palette is not dark: %q", got)
	}
}

func TestResolvePaletteHostPayload(t *testing.T) {
	t.Setenv("MCODE_ROUTER_THEME", "")
	t.Setenv("COLORFGBG", "")
	got := ResolvePalette(map[string]any{"colorScheme": "solarized dark"})
	if got.Name != "solarized dark" {
		t.Fatalf("host payload theme ignored: %q", got.Name)
	}
}
