package cmd

import (
	"context"
	"io"
	"os"
	"strconv"
	"time"

	"golang.org/x/term"

	"github.com/Flagsmith/flagsmith-cli/v2/internal/selfflags"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/spinner"
)

// envAnimation answers loading_animation locally, in either direction.
const envAnimation = "FLAGSMITH_ANIMATION"

// selfFlagTimeout bounds the background evaluation. It is generous: nothing
// waits on it, and the only cost of it running long is a goroutine the process
// may exit out from under.
const selfFlagTimeout = 10 * time.Second

// stderrIsTTY reports whether there is a terminal to animate on; tests stub it.
var stderrIsTTY = func() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}

// animation decides whether requests raise a waving flag, and whether asking
// Flagsmith could change that answer next time.
//
// Both are false when the answer cannot matter: a pipe or a CI log has nothing
// to animate, and FLAGSMITH_DEBUG puts a trace line on stderr for every request,
// which a repainting flag would fight over. A local answer is also a final one —
// setting FLAGSMITH_ANIMATION is how someone who would rather the CLI did not
// ask about itself says so.
func animation() (draw, ask bool) {
	if !stderrIsTTY() || envBool("FLAGSMITH_DEBUG") {
		return false, false
	}
	if os.Getenv(envAnimation) != "" {
		return envBool(envAnimation), false
	}
	return selfflags.Enabled(selfflags.LoadingAnimation), true
}

// The flag for this invocation, and whether its value is worth refreshing. Both
// are decided once, in Execute: the decision reads a file and installs a signal
// handler, neither of which belongs in a code path a test may run in-process
// hundreds of times.
var (
	activeFlag  *spinner.Spinner
	refreshWant bool
)

// animationOut is where the flag is drawn: stderr, which is where progress
// belongs. A var so tests can watch it.
var animationOut io.Writer = os.Stderr

// startAnimation gives the flag the terminal, when there is one and it is
// wanted. cobra's writers are wrapped because the flag stands between requests
// rather than being erased after each: the command's own output is what takes the
// line back, whenever it has something to print.
func startAnimation() {
	draw, ask := animation()
	refreshWant = ask
	if !draw {
		return
	}
	activeFlag = spinner.New(animationOut)
	// Both writers, or a standing flag outlives whichever one the command happens
	// to print through.
	rootCmd.SetOut(activeFlag.Guard(os.Stdout))
	rootCmd.SetErr(activeFlag.Guard(os.Stderr))
}

// releaseLine takes the line back from a standing flag. Called before anything
// that writes to the terminal without going through cobra — an interactive
// prompt — and once more before the process exits, for a command that printed
// nothing at all.
func releaseLine() {
	if activeFlag != nil {
		activeFlag.Release()
	}
}

// refreshSelfFlags evaluates the CLI's own flags in the background, for the next
// invocation to read. It builds its own client rather than sharing the command's,
// so the request cannot raise a flag about itself, and is abandoned if the
// process exits first — which only happens on commands too fast to have animated
// anything.
func refreshSelfFlags() {
	aud := selfAudience
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), selfFlagTimeout)
		defer cancel()
		selfflags.Refresh(ctx, aud) //nolint:errcheck // best-effort, and nothing to report
	}()
}

// selfAudience is what the resolved context contributes to the CLI's own
// evaluation.
var selfAudience selfflags.Audience

func noteAudience(pc *projectContext) {
	aud := selfflags.Audience{IsSaas: pc.apiURL() == defaultAPIURL}
	if id, ok := pc.Organisation.Value.(int); ok {
		aud.Organisation = strconv.Itoa(id)
	}
	selfAudience = aud
}
