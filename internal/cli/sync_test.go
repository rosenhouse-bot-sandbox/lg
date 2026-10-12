package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"

	"github.com/rosenhouse/lg/internal/cli"
	"github.com/rosenhouse/lg/internal/clock"
	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/lock"
	"github.com/rosenhouse/lg/internal/store"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("lg sync", Label("store"), func() {
	It("exits 4 after waiting 5m for a busy state/write.lock", Label("sync"), func() {
		home := GinkgoT().TempDir()
		config := filepath.Join(GinkgoT().TempDir(), "config.yaml")
		Expect(os.WriteFile(config, []byte("repo: rosenhouse/lg\n"), 0o644)).To(Succeed())
		writeLock := filepath.Join(home, "state", "write.lock")
		Expect(os.Mkdir(filepath.Dir(writeLock), 0o755)).To(Succeed())
		held, err := lock.Wait(writeLock, time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)

		var stderr bytes.Buffer
		clk := &firedClock{}
		code := make(chan int, 1)
		go func() {
			code <- cli.Main([]string{"sync"}, cli.Deps{
				Env:    map[string]string{"LG_HOME": home, "LG_CONFIG": config},
				Stdout: &bytes.Buffer{},
				Stderr: &stderr,
				Clock:  clk,
			})
		}()

		Eventually(code, 5*time.Second).Should(Receive(Equal(4)))
		Expect(clk.requested()).To(ContainElement(5 * time.Minute))
		Expect(stderr.String()).To(HaveSuffix(fmt.Sprintf("lg: %s is held by pid %d; gave up after 5m0s\n", writeLock, os.Getpid())))
	})
})

var _ = DescribeTable("lg sync without a daemon, while a request is held,", Label("sync"),
	func(heldPath, doing string) {
		s := harness.NewCLI()
		clk := clock.NewFake(harness.DefaultNow())
		release := s.Fake.Hold(heldPath)
		DeferCleanup(release)
		stderr, code := startSync(s, clk)
		Eventually(s.Fake.Requests, 5*time.Second).Should(ContainElement(HaveField("Path", HaveSuffix(heldPath))))

		clk.Set(harness.DefaultNow().Add(10 * time.Second))
		Eventually(stderr, 5*time.Second).Should(gbytes.Say("^lg: warning: never synced\nlg: " + doing + ", 10s elapsed\n"))
		clk.Set(harness.DefaultNow().Add(20 * time.Second))
		Eventually(stderr, 5*time.Second).Should(gbytes.Say("^lg: " + doing + ", 20s elapsed\n"))
		release()

		Eventually(code, 5*time.Second).Should(Receive(Equal(0)))
		clk.Set(harness.DefaultNow().Add(30 * time.Second))
		Consistently(stderr).ShouldNot(gbytes.Say("."))
	},
	Entry("says every 10s that it is starting", "/repos/rosenhouse/lg", "starting"),
	Entry("says every 10s that it is listing runs", "/actions/runs", "listing runs"),
	Entry("says every 10s how many runs' artifacts it fetched", "/artifacts/11276272069/zip", "fetching artifacts, 0 of 1 runs done"),
	Entry("says every 10s how many attempts' logs it fetched", "jobs/111221289888/logs", "fetching logs, 0 of 1 attempts done"),
)

var _ = Describe("lg sync without a daemon, while lg.db is busy,", Label("sync"), func() {
	It("says every 10s that it is indexing", func() {
		s := harness.NewCLI()
		clk := clock.NewFake(harness.DefaultNow())
		state := filepath.Join(s.Home, "state")
		Expect(os.Mkdir(state, 0o755)).To(Succeed())
		held, err := lock.Wait(filepath.Join(state, "lg.db.lock"), time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = held.Release() })
		stderr, code := startSync(s, clk)
		Eventually(stderr, 5*time.Second).Should(gbytes.Say(`lg: waiting for \S+/lg.db.lock \(held by [^)]*\)\n`))

		clk.Set(harness.DefaultNow().Add(10 * time.Second))
		Eventually(stderr, 5*time.Second).Should(gbytes.Say("^lg: indexing, 10s elapsed\n"))
		Expect(held.Release()).To(Succeed())

		Eventually(code, 5*time.Second).Should(Receive(Equal(0)))
	})
})

// startSync runs lg sync in the background against s's fake, on clk, with a
// stderr that the spec can read while lg writes it. It uses the default
// transport, since ShortTimeouts would end a held request.
func startSync(s *harness.CLI, clk clock.Clock) (*gbytes.Buffer, chan int) {
	stderr, code := gbytes.NewBuffer(), make(chan int, 1)
	go func() {
		code <- cli.Main([]string{"sync"}, cli.Deps{
			Env:    map[string]string{"LG_HOME": s.Home, "LG_CONFIG": s.Config, "LG_GH": "gh"},
			Stdout: &bytes.Buffer{},
			Stderr: stderr,
			Clock:  clk,
			Runner: harness.TokenRunner{},
			NewGitHub: func(api *url.URL, repo, token string, cache *github.Cache, clk clock.Clock) github.Client {
				return github.NewHTTP(http.DefaultTransport, api, repo, token, clk).WithCache(cache)
			},
			StoreFS: s.FS,
		})
	}()
	return stderr, code
}

var _ = Describe("lg sync that cannot tell whether a daemon runs", Label("daemon"), func() {
	It("exits 1 naming state/daemon.lock, and neither syncs nor sends a request", func() {
		s := harness.NewCLI()
		state := filepath.Join(s.Home, "state")
		Expect(os.Mkdir(state, 0o755)).To(Succeed())
		daemonLock := filepath.Join(state, "daemon.lock")
		Expect(os.Symlink(daemonLock, daemonLock)).To(Succeed())

		Expect(s.Main("sync")).To(Equal(1))
		Expect(s.Stderr.String()).To(ContainSubstring(daemonLock))
		Expect(filepath.Join(state, "sync-request")).NotTo(BeAnExistingFile())
		Expect(s.StatusFile()).NotTo(BeAnExistingFile())
	})
})

var _ = Describe("lg sync with a loopback api_url", Label("transport"), func() {
	It("exits 2 and runs no gh unless LG_GH names a test gh", func() {
		config := filepath.Join(GinkgoT().TempDir(), "config.yaml")
		Expect(os.WriteFile(config, []byte("repo: rosenhouse/lg\napi_url: http://127.0.0.1:1\n"), 0o644)).To(Succeed())
		var stderr bytes.Buffer
		runner := &countingRunner{}

		code := cli.Main([]string{"sync"}, cli.Deps{
			Env:    map[string]string{"LG_HOME": GinkgoT().TempDir(), "LG_CONFIG": config},
			Stdout: &bytes.Buffer{},
			Stderr: &stderr,
			Clock:  clock.Real{},
			Runner: runner,
		})

		Expect(code).To(Equal(2))
		Expect(stderr.String()).To(Equal("lg: warning: never synced\nlg: api_url may be on a loopback address only when LG_GH is set: \"http://127.0.0.1:1\"\n"))
		Expect(runner.runs).To(BeZero())
	})
})

type countingRunner struct{ runs int }

func (r *countingRunner) Run(context.Context, string, []string, map[string]string) (stdout, stderr []byte, err error) {
	r.runs++
	return nil, nil, fmt.Errorf("countingRunner runs nothing")
}

// firedClock fires every After at once, recording the durations asked for.
type firedClock struct {
	clock.Real
	mu        sync.Mutex
	durations []time.Duration
}

func (c *firedClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.durations = append(c.durations, d)
	fired := make(chan time.Time, 1)
	fired <- time.Time{}
	return fired
}

func (c *firedClock) requested() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.durations...)
}

var _ = DescribeTable("lg sync --timeout", Label("sync"),
	func(args []string, want time.Duration, busy ...string) {
		home := GinkgoT().TempDir()
		config := filepath.Join(GinkgoT().TempDir(), "config.yaml")
		Expect(os.WriteFile(config, []byte("repo: rosenhouse/lg\n"), 0o644)).To(Succeed())
		Expect(os.Mkdir(filepath.Join(home, "state"), 0o755)).To(Succeed())
		for _, name := range busy {
			held, err := lock.Wait(filepath.Join(home, "state", name), time.Second, clock.Real{}, func(string) {})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(held.Release)
		}

		var stderr bytes.Buffer
		clk := &firedClock{}
		code := make(chan int, 1)
		go func() {
			code <- cli.Main(append([]string{"sync"}, args...), cli.Deps{
				Env:     map[string]string{"LG_HOME": home, "LG_CONFIG": config},
				Stdout:  &bytes.Buffer{},
				Stderr:  &stderr,
				Clock:   clk,
				StoreFS: store.OSFS{},
			})
		}()

		Eventually(code, 5*time.Second).Should(Receive(Equal(4)))
		Expect(clk.requested()).To(ContainElement(BeNumerically("~", want, time.Second)))
		Expect(stderr.String()).To(HaveSuffix(fmt.Sprintf("gave up after %s\n", want)))
	},
	Entry("defaults to 15m for a --wait served by the daemon", []string{"--wait"}, 15*time.Minute, "daemon.lock"),
	Entry("defaults to 5m for the write-lock wait of a --wait with no daemon", []string{"--wait"}, 5*time.Minute, "write.lock"),
	Entry("bounds the wait for state/request.lock", []string{"--wait", "--timeout", "1s"}, time.Second, "daemon.lock", "request.lock"),
)

var _ = Describe("lg sync --wait with a daemon running", Label("sync"), func() {
	It("waits for the cycle even when it cannot write that it sent the request", func() {
		home := GinkgoT().TempDir()
		config := filepath.Join(GinkgoT().TempDir(), "config.yaml")
		Expect(os.WriteFile(config, []byte("repo: rosenhouse/lg\n"), 0o644)).To(Succeed())
		Expect(os.Mkdir(filepath.Join(home, "state"), 0o755)).To(Succeed())
		held, err := lock.Wait(filepath.Join(home, "state", "daemon.lock"), time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)

		code := cli.Main([]string{"sync", "--wait"}, cli.Deps{
			Env:     map[string]string{"LG_HOME": home, "LG_CONFIG": config},
			Stdout:  &bytes.Buffer{},
			Stderr:  fullWriter{},
			Clock:   &firedClock{},
			StoreFS: store.OSFS{},
		})

		Expect(code).To(Equal(4))
	})

	It("counts --timeout from before its wait for state/request.lock", func() {
		home := GinkgoT().TempDir()
		config := filepath.Join(GinkgoT().TempDir(), "config.yaml")
		Expect(os.WriteFile(config, []byte("repo: rosenhouse/lg\n"), 0o644)).To(Succeed())
		state := filepath.Join(home, "state")
		Expect(os.Mkdir(state, 0o755)).To(Succeed())
		held, err := lock.Wait(filepath.Join(state, "daemon.lock"), time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(held.Release)
		request, err := lock.Wait(filepath.Join(state, "request.lock"), time.Second, clock.Real{}, func(string) {})
		Expect(err).NotTo(HaveOccurred())
		t0 := time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)
		clk := clock.NewFake(t0)
		code := make(chan int, 1)
		go func() {
			code <- cli.Main([]string{"sync", "--wait", "--timeout", "1m"}, cli.Deps{
				Env:     map[string]string{"LG_HOME": home, "LG_CONFIG": config},
				Stdout:  &bytes.Buffer{},
				Stderr:  &bytes.Buffer{},
				Clock:   clk,
				StoreFS: store.OSFS{},
			})
		}()
		Eventually(clk.Waiting, time.Second).Should(Equal(2))
		Expect(request.Release()).To(Succeed())
		clk.Set(t0.Add(5 * time.Second))
		// The request's stale lock deadline, and WaitForCycle's deadline and poll.
		Eventually(clk.Waiting, time.Second).Should(Equal(3))

		clk.Set(t0.Add(time.Minute))

		Eventually(code, time.Second).Should(Receive(Equal(4)))
	})
})

// fullWriter fails every write, as a full disk does.
type fullWriter struct{}

func (fullWriter) Write([]byte) (int, error) { return 0, syscall.ENOSPC }

var _ = DescribeTable("a negative --timeout", Label("sync"),
	func(args ...string) {
		s := harness.NewCLI()

		Expect(s.Main(args...)).To(Equal(2))
		Expect(s.Stderr.String()).To(HavePrefix(fmt.Sprintf("lg: %s: --timeout must not be negative: -5s\n", args[0])))
	},
	Entry("is a usage error for sync", "sync", "--timeout=-5s"),
	Entry("is a usage error for gc", "gc", "--timeout=-5s"),
)
