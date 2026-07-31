// Package spinner draws a waving flag while HTTP requests are in flight.
package spinner

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// The flag is drawn in braille, whose eight dots per cell give four rows to
// ripple through at the resolution of one line of text.
const (
	// pole is one full cell: two dot columns, four rows.
	pole = "⣿"

	// erased returns to the start of the line and clears it, so whatever the
	// command prints next starts on clean ground.
	erased = "\r\x1b[K"

	// The cursor would otherwise sit blinking at the end of the flag. Hiding it
	// is a change to the terminal that outlives the process, so everything that
	// can end this one puts it back: Release, and a signal.
	hideCursor = "\x1b[?25l"
	showCursor = "\x1b[?25h"

	defaultInterval = 55 * time.Millisecond

	// defaultDelay is how long a request may take before it earns a flag. Most
	// are quicker than this, and would only flash one.
	defaultDelay = 150 * time.Millisecond
)

// cloth is five cells of flag in a gale: a wave six dots long against ten of
// flag, so there is always more than one crest in the air and the cloth is
// working rather than undulating. Its amplitude grows with distance, as a real
// one does, so the tip flutters while the cloth stays put where it is tied on.
//
// The table is one wave sampled at twenty phases with the stalls stripped out —
// at this width, rounding to whole dot rows makes five of those phases repeat
// their predecessor, and a repeated frame reads as a hitch. Dropping them cannot
// break the bound that matters: no column moves more than a single dot row
// between frames, which is what makes this ripple rather than flicker.
// TestClothRipples holds the table to it.
var cloth = []string{
	"⠛⠛⠛⣤⠛", "⠛⠛⠛⢦⠞", "⠛⠞⠛⢣⡜", "⠛⠞⠛⠳⡴", "⠛⠶⠛⠳⡴",
	"⠛⠶⠛⠛⣤", "⠛⠳⠞⠛⢦", "⠛⠳⠞⠛⢣", "⠛⠛⠶⠛⠳", "⠛⠛⠶⠛⠛",
	"⠛⠛⢦⠞⠛", "⠛⠛⠳⠞⠛", "⠛⠛⠳⡜⠛", "⠛⠛⠳⡴⠛", "⠛⠛⠛⡴⠛",
}

// shades light the cloth by how high it is flying, brightest first: a tint of
// $primary400, then $primary400 and $primary from the Flagsmith UI. Colour is a
// function of the shape rather than of the clock, so the crests carry the light
// with them as the wave travels and each cell is lit on its own — which is the
// whole of the shading. A time-based pulse on top of this only fought it.
var shades = []string{"#c9b3fd", "#906af6", "#6837fc"}

// rowBits are the two braille dots that share each row, top to bottom.
var rowBits = [4][2]uint{{0, 3}, {1, 4}, {2, 5}, {6, 7}}

// crest is the highest row a cell has ink in — how high the cloth flies there,
// and so which shade lights it. Ink-free cells report past the last shade and are
// clamped to the dimmest by the caller.
func crest(cell rune) int {
	mask := uint(cell - 0x2800)
	for row := range rowBits {
		if mask&(1<<rowBits[row][0]) != 0 || mask&(1<<rowBits[row][1]) != 0 {
			return row
		}
	}
	return len(shades) - 1
}

// poleHue is $text-icon-grey: the pole is furniture, only the cloth catches light.
const poleHue = "#656d7b"

// Spinner raises one flag for however many requests are in flight, and leaves it
// standing between them: a command making several requests in a row would
// otherwise flicker. Zero writes happen until a request has been slow enough to
// earn a flag, and the line is only given back by Release.
type Spinner struct {
	out      io.Writer
	delay    time.Duration
	interval time.Duration

	poleStyle  lipgloss.Style
	clothStyle []lipgloss.Style

	// tick is the frame counter, atomic because the animation goroutine owns it
	// while the methods below decide when that goroutine runs. It is never reset:
	// the ripple picks up where the last request left it, so a flag that stood
	// still between two requests resumes rather than restarting.
	tick atomic.Int64

	// mu guards the flight count and the channels belonging to the animation
	// goroutine it starts and stops.
	mu     sync.Mutex
	flying int
	stop   chan struct{}
	done   chan struct{}

	// lineMu guards the terminal line: whether a flag stands on it, whether the
	// cursor is hidden, and every write that changes either.
	lineMu   sync.Mutex
	standing bool
	finished bool
	guarded  sync.Once
}

// New returns a Spinner drawing to out, styled for whatever colour out can
// carry: lipgloss degrades truecolor to 256, to 16, to none, and honours
// NO_COLOR on its own.
func New(out io.Writer) *Spinner {
	return newSpinner(out, lipgloss.NewRenderer(out))
}

func newSpinner(out io.Writer, r *lipgloss.Renderer) *Spinner {
	s := &Spinner{
		out:       out,
		delay:     defaultDelay,
		interval:  defaultInterval,
		poleStyle: r.NewStyle().Foreground(lipgloss.Color(poleHue)),
	}
	for _, shade := range shades {
		s.clothStyle = append(s.clothStyle, r.NewStyle().Foreground(lipgloss.Color(shade)))
	}
	return s
}

// Wrap returns rt with the flag flying for the duration of every round trip.
// Wrapping the transport rather than the calls means every request the CLI makes
// is covered, at the cost of a generic message: a RoundTripper knows a URL, not
// what the command is doing with it.
func (s *Spinner) Wrap(rt http.RoundTripper) http.RoundTripper {
	return &transport{base: rt, spinner: s}
}

type transport struct {
	base    http.RoundTripper
	spinner *Spinner
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.spinner.raise()
	defer t.spinner.lower()
	return t.base.RoundTrip(req)
}

// raise counts one request in, starting the animation if it is the first.
func (s *Spinner) raise() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flying++
	if s.flying > 1 {
		return
	}
	s.stop, s.done = make(chan struct{}), make(chan struct{})
	go s.animate(s.stop, s.done)
}

// lower counts one request out, stopping the animation once the last one has
// landed. The flag it drew is left standing: only Release takes the line back.
// The wait is what makes that frame the last one written, so nothing arrives
// after whatever takes the line next.
func (s *Spinner) lower() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flying--
	if s.flying > 0 {
		return
	}
	close(s.stop)
	<-s.done
}

// Release takes the line back: it erases the standing flag and restores the
// cursor, doing nothing if no flag stands. Everything that writes where a flag
// might be calls it first — see Guard — and it is safe to call as often as that
// implies.
func (s *Spinner) Release() {
	s.lineMu.Lock()
	defer s.lineMu.Unlock()
	if !s.standing {
		return
	}
	fmt.Fprint(s.out, erased+showCursor) //nolint:errcheck // nothing to do if the terminal has gone
	s.standing = false
}

// Guard returns w wrapped so that anything written to it takes the line back
// from the flag first. This is how a standing flag gets cleared: the flag holds
// the line between requests, and the next thing with something to say clears it.
func (s *Spinner) Guard(w io.Writer) io.Writer {
	return &guard{base: w, spinner: s}
}

type guard struct {
	base    io.Writer
	spinner *Spinner
}

func (g *guard) Write(p []byte) (int, error) {
	g.spinner.Release()
	return g.base.Write(p)
}

// animate draws a frame every interval until stopped, leaving the last one
// standing. A request that finishes inside the delay never draws anything at all.
func (s *Spinner) animate(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	select {
	case <-stop:
		return
	case <-time.After(s.delay):
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		s.paint()
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}

// paint draws the current frame and advances the ripple, taking the line — and
// the cursor with it — the first time.
func (s *Spinner) paint() {
	s.lineMu.Lock()
	defer s.lineMu.Unlock()
	if s.finished {
		return // a signal has already put the terminal back; do not undo that
	}
	if !s.standing {
		s.guarded.Do(s.guardCursor)
		fmt.Fprint(s.out, hideCursor) //nolint:errcheck
		s.standing = true
	}
	fmt.Fprint(s.out, "\r"+s.frame(int(s.tick.Add(1)-1))) //nolint:errcheck
}

// guardCursor restores the cursor if this process is interrupted while a flag
// stands. Hiding the cursor outlives the CLI, so being killed mid-flight would
// otherwise leave the user's terminal with no cursor and no clue — and the only
// reason to handle a signal here is to prevent exactly that, so once the
// terminal is back it exits the way an unhandled signal would have.
func (s *Spinner) guardCursor() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		received := <-signals
		s.Release()
		s.lineMu.Lock()
		s.finished = true
		s.lineMu.Unlock()
		signal.Stop(signals)
		os.Exit(signalExit(received))
	}()
}

// signalExit is the code a shell expects from a process a signal ended: 128 plus
// the signal's number.
func signalExit(sig os.Signal) int {
	if s, ok := sig.(syscall.Signal); ok {
		return 128 + int(s)
	}
	return 1
}

// frame renders tick i: a pole, then the cloth cell by cell, each lit by how high
// it is flying.
func (s *Spinner) frame(i int) string {
	var b strings.Builder
	b.WriteString(s.poleStyle.Render(pole))
	for _, cell := range cloth[i%len(cloth)] {
		b.WriteString(s.clothStyle[min(crest(cell), len(s.clothStyle)-1)].Render(string(cell)))
	}
	return b.String()
}
