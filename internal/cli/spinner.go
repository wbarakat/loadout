package cli

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// isTerminal reports whether w is a real character device (a TTY). Only
// then does progress UI — carriage returns and clear-line codes — get
// written to it. A pipe, a redirected file, or a test's bytes.Buffer is
// not a terminal, so it gets the final summary alone, with no control
// characters mixed in.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// spinnerFrames are the Braille dot frames the spinner cycles through.
var spinnerFrames = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

// spinner draws one animated status line to a terminal, redrawing on its
// own ticker so the frame keeps moving even while the caller is busy in
// a long call. setLabel changes the text after the frame; stop freezes
// the animation and clears the line. Every method is safe to call from
// another goroutine, and safe to call on a no-op spinner — the one
// startSpinner returns when its writer is not a terminal, which does
// nothing at all.
type spinner struct {
	w      io.Writer
	mu     sync.Mutex
	label  string
	stopCh chan struct{}
	doneCh chan struct{}
	once   sync.Once
}

// startSpinner starts an animated spinner on w with an initial label. It
// returns a no-op spinner — one that draws nothing — when w is not a
// terminal, so a caller can always start one and never has to branch on
// whether output is interactive.
func startSpinner(w io.Writer, label string) *spinner {
	s := &spinner{w: w, label: label}
	if !isTerminal(w) {
		return s
	}
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	go s.run()
	return s
}

func (s *spinner) run() {
	defer close(s.doneCh)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	frame := 0
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.mu.Lock()
			label := s.label
			s.mu.Unlock()
			fmt.Fprintf(s.w, "\r\033[K%c %s", spinnerFrames[frame%len(spinnerFrames)], label)
			frame++
		}
	}
}

// setLabel changes the text shown after the spinner frame. It is a no-op
// on a spinner that is not animating.
func (s *spinner) setLabel(label string) {
	s.mu.Lock()
	s.label = label
	s.mu.Unlock()
}

// stop freezes the spinner and clears its line. It is safe to call more
// than once, and safe to call on a no-op spinner.
func (s *spinner) stop() {
	if s.stopCh == nil {
		return
	}
	s.once.Do(func() {
		close(s.stopCh)
		<-s.doneCh
		fmt.Fprint(s.w, "\r\033[K")
	})
}
