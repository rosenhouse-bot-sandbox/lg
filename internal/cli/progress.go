package cli

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/rosenhouse-bot-sandbox/lg/internal/clock"
)

// progressEvery is how often lg sync prints what its cycle is doing.
const progressEvery = 10 * time.Second

// progress prints what a sync is doing every progressEvery.
type progress struct {
	mu      sync.Mutex
	doing   string
	stopped chan struct{}
	exited  chan struct{}
}

func startProgress(w io.Writer, clk clock.Clock) *progress {
	p := &progress{doing: "starting", stopped: make(chan struct{}), exited: make(chan struct{})}
	start, tick := clk.Now(), clk.After(progressEvery)
	go func() {
		defer close(p.exited)
		for {
			select {
			case <-p.stopped:
				return
			case <-tick:
				// A fake clock that sees the line can then fire the next tick.
				tick = clk.After(progressEvery)
				_, _ = fmt.Fprintf(w, "lg: %s, %s elapsed\n", p.current(), clk.Now().Sub(start).Round(time.Second))
			}
		}
	}()
	return p
}

func (p *progress) Report(doing string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.doing = doing
}

func (p *progress) current() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.doing
}

// Stop returns once no progress line can follow.
func (p *progress) Stop() {
	close(p.stopped)
	<-p.exited
}
