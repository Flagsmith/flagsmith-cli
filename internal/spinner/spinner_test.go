package spinner

import (
	"bytes"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// recorder collects what the spinner draws and announces every paint, so tests
// can wait for the animation instead of sleeping for it.
type recorder struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	painted chan struct{}
}

func newRecorder() *recorder {
	return &recorder{painted: make(chan struct{}, 64)}
}

func (r *recorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.Contains(string(p), pole) {
		select {
		case r.painted <- struct{}{}:
		default: // a test that stopped watching must not block the animation
		}
	}
	return r.buf.Write(p)
}

func (r *recorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

// awaitPaint waits for the next frame to be drawn, failing rather than hanging.
func (r *recorder) awaitPaint(t *testing.T) {
	t.Helper()
	select {
	case <-r.painted:
	case <-time.After(5 * time.Second):
		t.Fatal("the flag was never drawn")
	}
}

// fly makes one request that stays in flight until the flag has been drawn, so a
// test can observe a flag without waiting on wall-clock time.
func fly(t *testing.T, s *Spinner, out *recorder) {
	t.Helper()
	b := &blocker{release: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		get(t, s.Wrap(b)) //nolint:errcheck
	}()
	out.awaitPaint(t)
	close(b.release)
	<-done
}

// blocker is a RoundTripper that hangs until released, standing in for a slow
// request.
type blocker struct {
	release chan struct{}
	err     error
}

func (b *blocker) RoundTrip(*http.Request) (*http.Response, error) {
	<-b.release
	if b.err != nil {
		return nil, b.err
	}
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

func get(t *testing.T, rt http.RoundTripper) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "https://api.flagsmith.example/", nil)
	if err != nil {
		t.Fatal(err)
	}
	return rt.RoundTrip(req)
}

// eager returns a spinner that animates immediately, so tests need not wait out
// the anti-flash delay.
func eager(out *recorder) *Spinner {
	s := New(out)
	s.delay = 0
	s.interval = time.Millisecond
	return s
}

func TestFlagFliesWhileRequestIsInFlight(t *testing.T) {
	// Given a request that has not come back yet
	out := newRecorder()
	s := eager(out)
	b := &blocker{release: make(chan struct{})}
	rt := s.Wrap(b)

	// When it is in flight
	done := make(chan struct{})
	go func() {
		defer close(done)
		get(t, rt) //nolint:errcheck
	}()
	out.awaitPaint(t)

	// Then the flag is up
	if !strings.Contains(out.String(), pole+cloth[0]) {
		t.Errorf("output = %q, want it to carry the flag", out.String())
	}

	// And the cursor is out of the way while it flies
	if !strings.HasPrefix(out.String(), hideCursor) {
		t.Errorf("output = %q, want it to start by hiding the cursor", out.String())
	}

	// And once the request returns the flag stands: the line is not given back
	// until something asks for it
	close(b.release)
	<-done
	standing := out.String()
	if strings.Contains(standing, erased) {
		t.Error("the line was erased when the request landed, rather than left standing")
	}
	if !strings.Contains(standing, pole) {
		t.Errorf("output = %q, want a flag still standing", standing)
	}

	// And releasing it puts back both the line and the cursor
	s.Release()
	if got := strings.TrimPrefix(out.String(), standing); got != erased+showCursor {
		t.Errorf("Release wrote %q, want %q", got, erased+showCursor)
	}
}

func TestFlagStandsAcrossConsecutiveRequests(t *testing.T) {
	// Given one request that has already raised a flag
	out := newRecorder()
	s := eager(out)
	fly(t, s, out)
	afterFirst := out.String()

	// When a second request follows it
	fly(t, s, out)

	// Then the flag never came down in between, and the cursor was hidden once:
	// a command making several requests in a row does not flicker
	whole := out.String()
	if strings.Contains(whole, erased) {
		t.Error("the flag came down between two requests")
	}
	if got := strings.Count(whole, hideCursor); got != 1 {
		t.Errorf("hid the cursor %d times, want 1", got)
	}
	// And the ripple carried on from where it stopped rather than restarting
	if !strings.HasPrefix(whole, afterFirst) {
		t.Error("the second request rewrote what the first had drawn")
	}
	if s.tick.Load() < 2 {
		t.Errorf("tick = %d after two requests, want the ripple to have advanced", s.tick.Load())
	}
}

func TestGuardTakesTheLineBack(t *testing.T) {
	// Given a flag standing after a request
	out := newRecorder()
	s := eager(out)
	fly(t, s, out)
	standing := out.String()

	// When the command writes its own output through a guarded writer
	if _, err := s.Guard(out).Write([]byte("NAME   ID\n")); err != nil {
		t.Fatal(err)
	}

	// Then the flag was cleared first, so the output starts on a clean line
	if got, want := strings.TrimPrefix(out.String(), standing), erased+showCursor+"NAME   ID\n"; got != want {
		t.Errorf("guarded write produced %q, want %q", got, want)
	}
}

func TestReleaseWithoutAFlagIsSilent(t *testing.T) {
	// Given nothing has been drawn
	out := newRecorder()
	s := eager(out)

	// When the line is released anyway — as it is at the end of every invocation
	s.Release()
	s.Release()

	// Then the terminal is left entirely alone: no stray erase, no cursor it
	// never hid
	if got := out.String(); got != "" {
		t.Errorf("Release wrote %q with no flag standing, want nothing", got)
	}
}

func TestFastRequestNeverFlashes(t *testing.T) {
	// Given a spinner that only animates a request slower than an hour
	out := newRecorder()
	s := New(out)
	s.delay = time.Hour
	rt := s.Wrap(&blocker{release: closed()})

	// When a request completes immediately
	if _, err := get(t, rt); err != nil {
		t.Fatal(err)
	}

	// Then nothing was drawn — no flash, and no stray line to erase
	if got := out.String(); got != "" {
		t.Errorf("output = %q, want nothing", got)
	}
}

func TestConcurrentRequestsShareOneFlag(t *testing.T) {
	// Given two overlapping requests
	out := newRecorder()
	s := eager(out)
	first := &blocker{release: make(chan struct{})}
	second := &blocker{release: make(chan struct{})}
	firstDone, secondDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(firstDone); get(t, s.Wrap(first)) }()   //nolint:errcheck
	go func() { defer close(secondDone); get(t, s.Wrap(second)) }() //nolint:errcheck
	out.awaitPaint(t)

	// When the first one finishes while the second is still waiting
	close(first.release)
	<-firstDone

	// Then it did not take the flag down with it: one flag flies for all of them,
	// raised once and never lowered on the way.
	close(second.release)
	<-secondDone
	if got := strings.Count(out.String(), hideCursor); got != 1 {
		t.Errorf("raised %d flags, want 1 between them", got)
	}
	if strings.Contains(out.String(), erased) {
		t.Error("a request landing took the flag down while another was in flight")
	}
}

func TestWrapPassesResponsesAndErrorsThrough(t *testing.T) {
	// Given a transport that fails
	out := newRecorder()
	sentinel := errors.New("dial tcp: nope")
	rt := eager(out).Wrap(&blocker{release: closed(), err: sentinel})

	// When a request is made through the spinner
	_, err := get(t, rt)

	// Then the caller sees exactly what the wrapped transport returned
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want %v", err, sentinel)
	}
}

func TestFrameRipplesThroughEveryPhase(t *testing.T) {
	// Given a spinner drawing to a terminal that can show every colour
	out := &bytes.Buffer{}
	r := lipgloss.NewRenderer(out)
	r.SetColorProfile(termenv.TrueColor)
	s := newSpinner(out, r)

	// When a full cycle of frames is drawn
	seen := map[string]int{}
	cycle := len(cloth)
	for i := range cycle {
		seen[s.frame(i)]++
	}

	// Then no frame in the cycle repeats: every phase of the wave looks different
	// from every other, so nothing reads as a stall
	if len(seen) != cycle {
		t.Errorf("distinct frames = %d, want %d", len(seen), cycle)
	}
	if s.frame(0) != s.frame(cycle) {
		t.Errorf("the cycle does not close: frame(0) != frame(%d)", cycle)
	}
	// And every frame is one flag on one pole
	for i := range cycle {
		if f := s.frame(i); !strings.Contains(f, pole) {
			t.Errorf("frame(%d) = %q, want a pole in it", i, f)
		}
	}
}

func TestFramePlainWithoutColour(t *testing.T) {
	// Given output that is not a terminal, so lipgloss renders no colour
	out := &bytes.Buffer{}
	s := New(out)

	// Then the frame is the flag and nothing else: no escape codes to leak into
	// a log or a pipe
	if got, want := s.frame(0), pole+cloth[0]; got != want {
		t.Errorf("frame(0) = %q, want %q", got, want)
	}
}

// TestClothRipples pins what makes the wave read as cloth rather than noise:
// every cell of every frame is a braille glyph, and no dot column jumps more
// than one row between consecutive frames.
func TestClothRipples(t *testing.T) {
	width := len([]rune(cloth[0]))
	for i, frame := range cloth {
		if got := len([]rune(frame)); got != width {
			t.Errorf("cloth[%d] = %q, %d cells, want %d", i, frame, got, width)
		}
		for _, r := range frame {
			if r < 0x2800 || r > 0x28ff {
				t.Errorf("cloth[%d] = %q contains %U, which is not braille", i, frame, r)
			}
		}
	}
	for i := range cloth {
		next := (i + 1) % len(cloth)
		for column, jump := range travel(cloth[i], cloth[next]) {
			if jump > 1 {
				t.Errorf("dot column %d jumps %d rows between cloth[%d] and cloth[%d]",
					column, jump, i, next)
			}
		}
	}
}

// travel reports, per dot column, how far the cloth's top edge moves between two
// frames.
func travel(from, to string) []int {
	fromTop, toTop := topEdge(from), topEdge(to)
	jumps := make([]int, len(fromTop))
	for i := range fromTop {
		if fromTop[i] < 0 || toTop[i] < 0 {
			continue // an empty column has no edge to have moved
		}
		jumps[i] = abs(fromTop[i] - toTop[i])
	}
	return jumps
}

// topEdge is the highest filled dot row per dot column of a braille frame, or -1
// where a column is empty.
func topEdge(frame string) []int {
	// Braille dot bits, by (row, column within the cell).
	bit := [4][2]uint{{0, 3}, {1, 4}, {2, 5}, {6, 7}}
	var edges []int
	for _, r := range frame {
		mask := uint(r - 0x2800)
		for col := range 2 {
			top := -1
			for row := 3; row >= 0; row-- {
				if mask&(1<<bit[row][col]) != 0 {
					top = row
				}
			}
			edges = append(edges, top)
		}
	}
	return edges
}

func abs(i int) int {
	if i < 0 {
		return -i
	}
	return i
}

// closed is an already-released blocker channel.
func closed() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

// foregroundSGR matches one truecolor foreground sequence.
var foregroundSGR = regexp.MustCompile(`\x1b\[38;2;\d+;\d+;\d+m`)

func TestClothIsShadedByHowHighItFlies(t *testing.T) {
	// Given the braille cells the table is built from
	for _, tc := range []struct {
		cell rune
		want int
	}{
		{'⠛', 0}, // ink in the top row: the cloth is flying high here
		{'⠶', 1},
		{'⣤', 2},
		{'⡀', 3}, // only the bottom row, past the last shade — the caller clamps
	} {
		// Then each reports the row its highest ink sits in
		if got := crest(tc.cell); got != tc.want {
			t.Errorf("crest(%q) = %d, want %d", tc.cell, got, tc.want)
		}
	}

	// Given a spinner on a terminal that can show every colour
	out := &bytes.Buffer{}
	r := lipgloss.NewRenderer(out)
	r.SetColorProfile(termenv.TrueColor)
	s := newSpinner(out, r)

	// Then every frame is painted with one colour per height its cloth reaches,
	// plus the pole's — cells at different heights are never flattened together
	lit := map[string]bool{}
	for i, frame := range cloth {
		heights := map[int]bool{}
		for _, cell := range frame {
			heights[min(crest(cell), len(shades)-1)] = true
		}
		colours := map[string]bool{}
		for _, sgr := range foregroundSGR.FindAllString(s.frame(i), -1) {
			colours[sgr] = true
			lit[sgr] = true
		}
		if want := len(heights) + 1; len(colours) != want {
			t.Errorf("frame(%d) uses %d colours for %d heights (+ the pole), want %d",
				i, len(colours), len(heights), want)
		}
	}

	// And the table exercises the whole ramp, or the shading would be decoration
	// nobody sees
	if want := len(shades) + 1; len(lit) != want {
		t.Errorf("the wave is lit by %d colours across a full cycle, want %d (every shade, plus the pole)", len(lit), want)
	}
}
