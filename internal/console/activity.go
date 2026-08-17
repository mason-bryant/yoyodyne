package console

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Activity is the harness saying what it is doing while the operator waits.
// Without one, a turn that takes a minute and a turn that has hung look exactly
// alike, which is what an operator reasonably reads as a broken command.
type Activity interface {
	// Doing says what is happening now. An empty phase leaves what is on screen
	// as it is; either way the call is evidence that something arrived, which is
	// what separates a turn that is progressing from one that is not.
	Doing(phase string)
	// Close ends the account and takes whatever it drew off the screen. It is
	// safe to call more than once.
	Close()
}

// spinnerFrames animate while the work is moving. quietAfter is how long
// nothing may arrive before the display says so: past it the frame stops
// advancing, because an indicator that animates through a stall looks like
// progress, and looking like progress is worse than silence.
var spinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const (
	spinnerInterval = 120 * time.Millisecond
	quietAfter      = 20 * time.Second
)

// working is what the display says at a given moment: the phase it was last
// told about, how long the whole thing has taken, and when it last heard
// anything.
type working struct {
	phase   string
	frame   int
	started time.Time
	heard   time.Time
}

// line is the activity line as it stands now. A turn nothing has arrived for is
// described as exactly that, with how long it has been quiet, so the operator
// can tell a slow turn from a stalled one by reading rather than by guessing at
// an animation.
func (w working) line(now time.Time) string {
	if quiet := now.Sub(w.heard); quiet >= quietAfter {
		return fmt.Sprintf("%s %s — nothing from the provider for %s", w.mark(), w.phase, whole(quiet))
	}
	return fmt.Sprintf("%s %s (%s)", w.mark(), w.phase, whole(now.Sub(w.started)))
}

func (w working) mark() string { return spinnerFrames[w.frame%len(spinnerFrames)] }

// tick advances the animation, and deliberately does not while nothing is
// arriving.
func (w *working) tick(now time.Time) {
	if now.Sub(w.heard) >= quietAfter {
		return
	}
	w.frame = (w.frame + 1) % len(spinnerFrames)
}

// whole is a duration as the operator reads it: whole seconds, because a
// display that counts milliseconds is telling them about the harness rather
// than about their turn.
func whole(elapsed time.Duration) string {
	if elapsed < time.Second {
		return "0s"
	}
	return elapsed.Truncate(time.Second).String()
}

// spinner is the account of work in progress on a terminal, drawn on a line of
// its own below the conversation and erased when the work is over. It updates
// on a timer as well as on what arrives, so the elapsed time keeps moving even
// when nothing else does: the harness saying it is still here is not the same
// claim as the provider saying it is working.
type spinner struct {
	mu     sync.Mutex
	state  working
	show   func(string)
	now    func() time.Time
	stop   chan struct{}
	done   chan struct{}
	closed bool
}

func newSpinner(phase string, show func(string), now func() time.Time, interval time.Duration) *spinner {
	started := now()
	animation := &spinner{
		state: working{phase: phase, started: started, heard: started},
		show:  show,
		now:   now,
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	animation.draw()
	go animation.animate(interval)
	return animation
}

func (s *spinner) Doing(phase string) {
	s.mu.Lock()
	if strings.TrimSpace(phase) != "" {
		s.state.phase = phase
	}
	s.state.heard = s.now()
	s.mu.Unlock()
	s.draw()
}

func (s *spinner) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	close(s.stop)
	// The animation is waited for before the line is cleared, so nothing draws
	// the display back after it has been taken away.
	<-s.done
	s.show("")
}

func (s *spinner) animate(interval time.Duration) {
	defer close(s.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.mu.Lock()
			s.state.tick(s.now())
			s.mu.Unlock()
			s.draw()
		}
	}
}

func (s *spinner) draw() {
	s.mu.Lock()
	line := s.state.line(s.now())
	s.mu.Unlock()
	s.show(line)
}

// narration is the account of work in progress on an ordinary stream. There is
// nothing to animate and nothing to erase, so each phase is said once, as a
// line like any other. What it deliberately does not say is anything measured
// by a clock: a redirected transcript whose contents depend on how long the
// provider took is not one anybody can compare against another.
type narration struct {
	mu   sync.Mutex
	out  io.Writer
	said string
}

func (n *narration) Doing(phase string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if phase == "" || phase == n.said {
		return
	}
	n.said = phase
	fmt.Fprintf(n.out, "… %s\n", phase)
}

func (n *narration) Close() {}
