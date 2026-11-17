package ui

// rateBolt is the high-voltage sign MiniMax Code puts in front of a token rate.
const rateBolt = "\u26a1"

// rateRamp grades worker throughput from dim green to bright green.
var rateRamp = []string{
	"\033[38;5;66m",
	"\033[38;5;78m",
	"\033[38;5;120m",
	"\033[1;38;5;158m",
}

// rateColor picks the colour for a worker token rate. An idle worker stays in
// the theme's muted tone so it blends into the status bar, and the colour
// brightens as throughput climbs.
func rateColor(pal Palette, rate float64) string {
	switch {
	case rate <= 0:
		return pal.Muted
	case rate < 25:
		return rateRamp[0]
	case rate < 75:
		return rateRamp[1]
	case rate < 150:
		return rateRamp[2]
	default:
		return rateRamp[3]
	}
}
