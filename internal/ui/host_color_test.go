// Tests that the status line only emits colours MiniMax Code keeps.
//
// MiniMax Code filters the custom status line output through its own SGR
// parser: it keeps reset, default, standard and 256-colour parameters and
// drops every other sequence whole. A dropped sequence takes its colour with
// it, so a bold step would leave the fastest workers unstyled.
package ui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/UberMetroid/mcode-router/internal/dispatch"
)

// hostKeepsColor reports whether MiniMax Code keeps an SGR sequence.
func hostKeepsColor(seq string) bool {
	if !strings.HasPrefix(seq, "\033[") || !strings.HasSuffix(seq, "m") {
		return false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(seq, "\033["), "m")
	params := []string{}
	if body != "" {
		params = strings.Split(body, ";")
	}
	nums := make([]int, 0, len(params))
	for _, p := range params {
		n, err := strconv.Atoi(p)
		if err != nil {
			return false
		}
		nums = append(nums, n)
	}
	for i := 0; i < len(nums); i++ {
		s := nums[i]
		if s == 38 || s == 48 {
			if i+1 >= len(nums) {
				return false
			}
			count := 0
			switch nums[i+1] {
			case 5:
				count = 1
			case 2:
				count = 3
			default:
				return false
			}
			if i+2+count > len(nums) {
				return false
			}
			for _, v := range nums[i+2 : i+2+count] {
				if v < 0 || v > 255 {
					return false
				}
			}
			i += count + 1
			continue
		}
		if s == 0 || s == 39 || s == 49 {
			continue
		}
		if (s >= 30 && s <= 37) || (s >= 40 && s <= 47) ||
			(s >= 90 && s <= 97) || (s >= 100 && s <= 107) {
			continue
		}
		return false
	}
	return true
}

// colorSequences pulls the SGR sequences out of a rendered row.
func colorSequences(row string) []string {
	var out []string
	for i := 0; i < len(row); i++ {
		if row[i] != '\033' {
			continue
		}
		end := i + 1
		for end < len(row) && row[end] != 'm' {
			end++
		}
		if end < len(row) {
			out = append(out, row[i:end+1])
			i = end
		}
	}
	return out
}

// TestHostKeepsEveryStatusColour checks that the ramp and the accent colours
// survive MiniMax Code's status line filter.
func TestHostKeepsEveryStatusColour(t *testing.T) {
	for _, name := range []string{"dark", "light", "tokyo night", "solarized dark", "halloween"} {
		pal := PaletteFor(name)
		for label, seq := range map[string]string{
			"muted": pal.Muted, "accent": pal.StatusAccent,
		} {
			if !hostKeepsColor(seq) {
				t.Fatalf("palette %q %s colour %q is dropped by the host", name, label, seq)
			}
		}
	}
	for i, seq := range rateRamp {
		if !hostKeepsColor(seq) {
			t.Fatalf("ramp step %d colour %q is dropped by the host", i, seq)
		}
	}
}

// TestWorkerRowColoursSurviveHost checks that a rendered worker row carries no
// colour the host would strip.
func TestWorkerRowColoursSurviveHost(t *testing.T) {
	pal := PaletteFor("dark")
	m := dispatch.WorkerMarker{
		TaskID: "task", Lane: "devin-fast", CLI: "devin",
		Model: "swe-2-flash-max", Effort: "max", Phase: "stream", TokS: 400,
	}
	row := FormatWorkerLine(pal, m, time.Now())
	for _, seq := range colorSequences(row) {
		if !hostKeepsColor(seq) {
			t.Fatalf("row colour %q is dropped by the host: %q", seq, row)
		}
	}
	if len(colorSequences(row)) < 3 {
		t.Fatalf("row should colour the rate and the model: %q", row)
	}
}
