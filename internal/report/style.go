package report

import (
	"os"
	"strings"

	"golang.org/x/term"
)

// Style describes the output terminal. The zero value is plain output
// (pipes, files, tests): no color, no width limit.
type Style struct {
	Width int  // terminal columns; 0 = not a terminal
	Color bool // ANSI colors allowed
}

// MinWidth is the narrowest layout we try to render; narrower terminals
// are treated as this wide and will wrap.
const MinWidth = 40

// StyleFor inspects f: a terminal gets its width and, unless NO_COLOR is set
// or TERM is dumb, colors.
func StyleFor(f *os.File) Style {
	if f == nil || !term.IsTerminal(int(f.Fd())) {
		return Style{}
	}
	w, _, err := term.GetSize(int(f.Fd()))
	if err != nil || w <= 0 {
		w = 80
	}
	return Style{
		Width: max(w, MinWidth),
		Color: os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb",
	}
}

func (s Style) wrap(code, text string) string {
	if !s.Color || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s Style) Bold(t string) string   { return s.wrap("1", t) }
func (s Style) Dim(t string) string    { return s.wrap("2", t) }
func (s Style) Red(t string) string    { return s.wrap("1;31", t) }
func (s Style) Green(t string) string  { return s.wrap("32", t) }
func (s Style) Yellow(t string) string { return s.wrap("1;33", t) }
func (s Style) Cyan(t string) string   { return s.wrap("1;36", t) }

// Fit truncates t (already cleaned) to n columns with an ellipsis and pads
// it with spaces to exactly n. Width is counted in runes.
// ponytail: wide (CJK/emoji) runes count as 1 column; use go-runewidth if misaligned.
func Fit(t string, n int) string {
	r := []rune(t)
	if len(r) > n {
		return string(r[:max(n-1, 0)]) + "…"
	}
	return t + strings.Repeat(" ", n-len(r))
}
