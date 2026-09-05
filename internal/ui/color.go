// Package ui owns everything the terminal renders as decoration: colour
// detection, the launch banner, and the accents on the status line. It degrades
// all the way down to plain ASCII, because a pipe, a CI log and a dumb terminal
// all have to stay readable.
package ui

import (
	"fmt"
	"os"
	"strings"
)

// Razorpay's brand colours.
var (
	DodgerBlue = RGB{0x0D, 0x94, 0xFB} // #0D94FB
	GreenVogue = RGB{0x01, 0x26, 0x52} // #012652
)

type RGB struct{ R, G, B uint8 }

// Blend moves c toward d by t (0..1).
func (c RGB) Blend(d RGB, t float64) RGB {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	lerp := func(a, b uint8) uint8 { return uint8(float64(a) + t*(float64(b)-float64(a))) }
	return RGB{lerp(c.R, d.R), lerp(c.G, d.G), lerp(c.B, d.B)}
}

// Level is how much colour the attached terminal can take.
type Level int

const (
	None Level = iota
	Basic
	TrueColor
)

// Detect resolves the colour level for a stream, honouring NO_COLOR (any
// value disables colour) and FORCE_COLOR.
func Detect(f *os.File) Level {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return None
	}
	if _, ok := os.LookupEnv("FORCE_COLOR"); ok {
		return TrueColor
	}
	if os.Getenv("TERM") == "dumb" {
		return None
	}
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return None // piped or redirected: escape codes would be noise
	}
	if !enableVT(f) {
		return None
	}
	switch {
	case strings.Contains(os.Getenv("COLORTERM"), "truecolor"),
		strings.Contains(os.Getenv("COLORTERM"), "24bit"),
		os.Getenv("WT_SESSION") != "",
		os.Getenv("TERM_PROGRAM") != "":
		return TrueColor
	case os.Getenv("TERM") != "":
		return Basic
	}
	// A Windows console that accepted the VT mode switch can do truecolor.
	return TrueColor
}

// Painter renders styled text at whatever level the terminal supports.
type Painter struct{ Level Level }

func NewPainter(f *os.File) *Painter { return &Painter{Level: Detect(f)} }

const reset = "\x1b[0m"

// FG paints text in an exact colour, falling back to bright blue and then to
// no styling at all.
func (p *Painter) FG(c RGB, s string) string {
	switch p.Level {
	case TrueColor:
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s%s", c.R, c.G, c.B, s, reset)
	case Basic:
		return "\x1b[94m" + s + reset
	default:
		return s
	}
}

// Dim renders secondary text.
func (p *Painter) Dim(s string) string {
	if p.Level == None {
		return s
	}
	return "\x1b[2m" + s + reset
}

// Bold renders emphasis.
func (p *Painter) Bold(s string) string {
	if p.Level == None {
		return s
	}
	return "\x1b[1m" + s + reset
}
