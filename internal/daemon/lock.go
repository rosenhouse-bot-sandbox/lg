package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/rosenhouse-bot-sandbox/lg/internal/clock"
	"github.com/rosenhouse-bot-sandbox/lg/internal/lock"
	"github.com/rosenhouse-bot-sandbox/lg/internal/store"
)

// instanceWait outlasts the moment lock.Held holds a lock, so lg status
// never makes a daemon think another one runs.
const instanceWait = time.Second

// InstanceLock is state/daemon.lock, which one daemon holds for its life,
// and state/daemon.pid, which names it.
type InstanceLock struct {
	held  *lock.Lock
	fsys  store.FS
	state string
}

// LockInstance takes state/daemon.lock and writes state/daemon.pid. It
// passes a failure to write daemon.pid to warn, since a full disk must not
// stop the daemon whose retention frees space.
func LockInstance(fsys store.FS, state string, clk clock.Clock, warn func(error)) (*InstanceLock, error) {
	pid := filepath.Join(state, "daemon.pid")
	held, err := lock.Wait(instanceFile(state), instanceWait, clk, func(string) {})
	if errors.Is(err, lock.ErrTimeout) {
		return nil, fmt.Errorf("already running (%s)", RunningPID(state))
	}
	if err != nil {
		return nil, err
	}
	if err := store.ReplaceFileFS(fsys, pid, fmt.Appendf(nil, "%d\n", os.Getpid())); err != nil {
		warn(errors.Join(err, fsys.RemoveAll(pid)))
	}
	return &InstanceLock{held: held, fsys: fsys, state: state}, nil
}

// Running reports whether a daemon holds state/daemon.lock.
func Running(state string) (bool, error) { return lock.Held(instanceFile(state)) }

func instanceFile(state string) string { return filepath.Join(state, "daemon.lock") }

// RunningPID names the daemon by Holder, as "pid N" or "pid unknown".
func RunningPID(state string) string {
	if pid, ok := Holder(state); ok {
		return "pid " + strconv.Itoa(pid)
	}
	return "pid unknown"
}

// Holder gives the pid of the daemon by state/daemon.pid, else by the pid
// state/daemon.lock records.
func Holder(state string) (pid int, ok bool) {
	for _, name := range []string{"daemon.pid", "daemon.lock"} {
		content, _ := os.ReadFile(filepath.Join(state, name))
		if pid, err := strconv.Atoi(string(bytes.TrimSpace(content))); err == nil {
			return pid, true
		}
	}
	return 0, false
}

// Lost gives an error once state/daemon.lock is no longer the file l holds,
// as after state/ is removed, since another daemon could then lock it.
func (l *InstanceLock) Lost() error {
	held, err := l.held.Stat()
	if err != nil {
		return err
	}
	path := instanceFile(l.state)
	named, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) || err == nil && !os.SameFile(held, named) {
		return fmt.Errorf("%s was removed", path)
	}
	return err
}

func (l *InstanceLock) Release() error {
	return errors.Join(l.fsys.RemoveAll(filepath.Join(l.state, "daemon.pid")), l.held.Release())
}
