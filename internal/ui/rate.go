package ui

// rateBolt is the high-voltage sign MiniMax Code puts in front of a token rate.
const rateBolt = "\u26a1"

// rateRamp grades worker throughput from deep green to white.
// Every step is a plain 256-colour foreground on purpose: MiniMax Code keeps
// only the SGR parameters it recognises and drops any other sequence whole,
// so a bold colour would take the rate row's colour with it and the fastest
// workers would render unstyled.
var rateRamp = []string{
	"\033[38;5;28m",
	"\033[38;5;34m",
	"\033[38;5;40m",
	"\033[38;5;46m",
	"\033[38;5;82m",
	"\033[38;5;84m",
	"\033[38;5;157m",
	"\033[38;5;231m",
}

// rateThresholds are the exclusive upper bounds of each ramp step.
var rateThresholds = []float64{15, 40, 80, 150, 300, 500, 750}

// rateColor picks the colour for a worker token rate. An idle worker stays in
// the theme's muted tone so it blends into the status bar, and the colour
// brightens step by step as throughput climbs.
func rateColor(pal Palette, rate float64) string {
	if rate <= 0 {
		return pal.Muted
	}
	for i, lim := range rateThresholds {
		if rate < lim {
			return rateRamp[i]
		}
	}
	return rateRamp[len(rateRamp)-1]
}
