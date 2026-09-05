package ui

import (
	"fmt"
	"strings"
)

// bannerArt spells RZP-AI. Kept as a raw block so the glyphs stay exactly as
// drawn; the gradient is applied per line at render time.
var bannerArt = []string{
	`░█████████  ░█████████ ░█████████             ░███    ░██████`,
	`░██     ░██       ░██  ░██     ░██           ░██░██     ░██  `,
	`░██     ░██      ░██   ░██     ░██          ░██  ░██    ░██  `,
	`░█████████     ░███    ░█████████  ░██████ ░█████████   ░██  `,
	`░██   ░██     ░██      ░██                 ░██    ░██   ░██  `,
	`░██    ░██   ░██       ░██                 ░██    ░██   ░██  `,
	`░██     ░██ ░█████████ ░██                 ░██    ░██ ░██████`,
}

// gradientDepth is how far down toward Green Vogue the last line travels.
// Going the whole way would sink the bottom row into an unlit navy that
// disappears on a dark terminal, so it stops short.
const gradientDepth = 0.72

// Banner renders the wordmark in Razorpay's blues, top-lit in Dodger Blue and
// settling toward Green Vogue. Without colour it is still legible ASCII art.
func (p *Painter) Banner() string {
	var b strings.Builder
	b.WriteString("\n")
	n := len(bannerArt)
	for i, line := range bannerArt {
		t := 0.0
		if n > 1 {
			t = float64(i) / float64(n-1) * gradientDepth
		}
		fmt.Fprintf(&b, "  %s\n", p.FG(DodgerBlue.Blend(GreenVogue, t), line))
	}
	return b.String()
}

// Tagline is the one sentence the whole design serves.
func (p *Painter) Tagline() string {
	return "  " + p.Dim("the model plans and explains — it does not calculate, and it does not move money") + "\n"
}

// StatusLine paints the session banner: the mode reads loudest, because a
// write-enabled session is the one fact a user must never miss.
func (p *Painter) StatusLine(s string) string {
	parts := strings.Split(s, " · ")
	for i, part := range parts {
		switch {
		case strings.Contains(part, "WRITE ENABLED"):
			parts[i] = p.FG(RGB{0xFF, 0x8C, 0x2B}, p.Bold(part))
		case part == "live mode":
			parts[i] = p.FG(RGB{0xE5, 0x48, 0x4A}, p.Bold(part))
		case i == 0:
			parts[i] = p.FG(DodgerBlue, part)
		default:
			parts[i] = p.Dim(part)
		}
	}
	return "  " + strings.Join(parts, p.Dim(" · "))
}

// Prompt is the REPL's input prompt.
func (p *Painter) Prompt() string {
	return p.FG(DodgerBlue, "rzp-ai") + p.Dim("> ")
}
