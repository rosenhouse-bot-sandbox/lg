package github_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse-bot-sandbox/lg/internal/clock"
	"github.com/rosenhouse-bot-sandbox/lg/internal/failure"
	"github.com/rosenhouse-bot-sandbox/lg/internal/github"
	"github.com/rosenhouse-bot-sandbox/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse-bot-sandbox/lg/internal/testsupport/scenario"
)

var _ = Describe("ListRepoArtifacts", Label("artifacts"), func() {
	const (
		runID          = 37129390741
		logsDeletedRun = 37129738159
	)
	// Every artifact of both runs, highest id first.
	all := []int64{11276546973, 11276517061, 11276401837, 11276272069, 11276237903, 11276182467, 11276128157, 11275917910}

	var (
		fake   *fakegithub.Server
		client *github.HTTP
	)

	BeforeEach(func() {
		fake = fakegithub.Start(runID, "after-attempt-1")
		Expect(fake.Load(logsDeletedRun, "logs-deleted")).To(Succeed())
		client = github.NewHTTP(http.DefaultTransport, mustParse(fake.URL()), "rosenhouse-bot-sandbox/lg", "lg-test-token", clock.Real{})
	})

	ids := func(artifacts []github.Artifact) []int64 {
		listed := make([]int64, len(artifacts))
		for i, a := range artifacts {
			listed[i] = a.ID
		}
		return listed
	}

	listings := func() []string {
		var queries []string
		for _, r := range fake.Requests() {
			if strings.HasSuffix(r.Path, "/actions/artifacts") {
				queries = append(queries, r.Query)
			}
		}
		return queries
	}

	It("lists every artifact of the repo with its run, highest id first, from one page of 100", func(ctx SpecContext) {
		artifacts, source, err := client.ListRepoArtifacts(ctx, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(artifacts)).To(Equal(all))
		Expect(artifacts[0].WorkflowRun.ID).To(BeEquivalentTo(logsDeletedRun))
		Expect(artifacts[2].WorkflowRun.ID).To(BeEquivalentTo(runID))
		Expect(source).To(Equal(github.Source{URL: fake.URL() + "/repos/rosenhouse-bot-sandbox/lg/actions/artifacts?per_page=100", Pages: 1}))
		Expect(listings()).To(Equal([]string{"per_page=100"}))
	})

	It("starts each later page at least 2 artifacts before the last one read, and counts its pages", func(ctx SpecContext) {
		fake.SetPageCap(5)
		artifacts, source, err := client.ListRepoArtifacts(ctx, 3)
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(artifacts)).To(Equal(all))
		Expect(source.Pages).To(Equal(3))
		Expect(listings()).To(Equal([]string{"per_page=100", "page=2&per_page=3", "page=2&per_page=4"}))
	})

	It("lists an artifact that a deletion between pages shifts onto a page already read", func(ctx SpecContext) {
		fake.SetPageCap(5)
		release := fake.HoldAfter("/actions/artifacts", 1)
		type result struct {
			artifacts []github.Artifact
			err       error
		}
		done := make(chan result)
		go func() {
			artifacts, _, err := client.ListRepoArtifacts(ctx, 3)
			done <- result{artifacts, err}
		}()
		Eventually(listings).Should(HaveLen(2))
		Expect(fake.AddRun(scenario.WithoutArtifact(scenario.Recorded(runID, "after-attempt-1"), 11276401837))).To(Succeed())
		release()

		var got result
		Eventually(done).Should(Receive(&got))
		Expect(got.err).NotTo(HaveOccurred())
		Expect(ids(got.artifacts)).To(Equal(all))
	})

	It("fails rather than skip artifacts when more were deleted between pages than its pages overlap", func(ctx SpecContext) {
		fake.SetPageCap(5)
		release := fake.HoldAfter("/actions/artifacts", 1)
		done := make(chan error)
		go func() {
			_, _, err := client.ListRepoArtifacts(ctx, 3)
			done <- err
		}()
		Eventually(listings).Should(HaveLen(2))
		fake.Remove(logsDeletedRun)
		release()

		var err error
		Eventually(done).Should(Receive(&err))
		Expect(err).To(MatchError(ContainSubstring("shifted")))
		Expect(err).To(BeAssignableToTypeOf(failure.Transient{}))
	})

	It("declines after its first page a listing whose overlapping pages need more than allowed", func(ctx SpecContext) {
		fake.SetPageCap(5)
		_, _, err := client.ListRepoArtifacts(ctx, 2)
		Expect(err).To(MatchError(github.ErrTooManyPages))
		Expect(listings()).To(HaveLen(1))
	})

	It("stops at the page budget when artifacts added during the listing need more pages", func(ctx SpecContext) {
		fake.SetPageCap(5)
		release := fake.HoldAfter("/actions/artifacts", 1)
		done := make(chan error)
		go func() {
			_, _, err := client.ListRepoArtifacts(ctx, 3)
			done <- err
		}()
		Eventually(listings).Should(HaveLen(2))
		Expect(fake.AddRun(scenario.Clone(scenario.Recorded(runID, "after-attempt-1"), 1))).To(Succeed())
		release()

		var err error
		Eventually(done).Should(Receive(&err))
		Expect(err).To(MatchError(github.ErrTooManyPages))
		Expect(listings()).To(HaveLen(3))
	})

	It("reads on past a page of artifacts all read already, which additions before them shift there", func(ctx SpecContext) {
		fake.SetPageCap(5)
		release := fake.HoldAfter("/actions/artifacts", 1)
		type result struct {
			artifacts []github.Artifact
			err       error
		}
		done := make(chan result)
		go func() {
			artifacts, _, err := client.ListRepoArtifacts(ctx, 10)
			done <- result{artifacts, err}
		}()
		Eventually(listings).Should(HaveLen(2))
		Expect(fake.AddRun(scenario.Clone(scenario.Recorded(runID, "after-attempt-1"), 999_999))).To(Succeed())
		release()

		var got result
		Eventually(done).Should(Receive(&got))
		Expect(got.err).NotTo(HaveOccurred())
		Expect(ids(got.artifacts)).To(Equal(all))
	})

	It("fails when a page after the first lists nothing, since every artifact it would hold was deleted", func(ctx SpecContext) {
		fake.SetPageCap(5)
		release := fake.HoldAfter("/actions/artifacts", 1)
		done := make(chan error)
		go func() {
			_, _, err := client.ListRepoArtifacts(ctx, 3)
			done <- err
		}()
		Eventually(listings).Should(HaveLen(2))
		fake.Remove(runID)
		fake.Remove(logsDeletedRun)
		release()

		var err error
		Eventually(done).Should(Receive(&err))
		Expect(err).To(MatchError(ContainSubstring("shifted")))
	})

	It("fails as Transient when pages are too small to overlap", func(ctx SpecContext) {
		fake.SetPageCap(4)
		_, _, err := client.ListRepoArtifacts(ctx, 10)
		Expect(err).To(MatchError(ContainSubstring("too small to overlap")))
		Expect(err).To(BeAssignableToTypeOf(failure.Transient{}))
		Expect(listings()).To(HaveLen(1))
	})

	It("fails as Transient when its first page lists none of total_count", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, `{"total_count":3,"artifacts":[]}`)
		}))
		DeferCleanup(server.Close)
		client := github.NewHTTP(http.DefaultTransport, mustParse(server.URL), "o/r", "lg-test-token", clock.Real{})

		_, _, err := client.ListRepoArtifacts(context.Background(), 5)
		Expect(err).To(MatchError(ContainSubstring("listed no artifacts of 3")))
		Expect(err).To(BeAssignableToTypeOf(failure.Transient{}))
	})

	DescribeTable("refuses a page it cannot trust",
		func(artifacts, message string) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprintf(w, `{"total_count":2,"artifacts":[%s]}`, artifacts)
			}))
			DeferCleanup(server.Close)
			client := github.NewHTTP(http.DefaultTransport, mustParse(server.URL), "o/r", "lg-test-token", clock.Real{})

			_, _, err := client.ListRepoArtifacts(context.Background(), 1)
			var malformed *github.MalformedError
			Expect(errors.As(err, &malformed)).To(BeTrue(), "%v", err)
			Expect(err).To(MatchError(ContainSubstring(message)))
		},
		Entry("ids out of order", artifact(1, 7)+","+artifact(2, 7), "artifact 2 follows artifact 1"),
		Entry("an id repeated", artifact(2, 7)+","+artifact(2, 7), "artifact 2 follows artifact 2"),
		Entry("no id", artifact(0, 7)+","+artifact(2, 7), "has no id"),
		Entry("no workflow_run id", artifact(2, 7)+","+artifact(1, 0), "artifact 1 has no workflow_run id"),
		Entry("no created_at", artifact(2, 7)+`,{"id":1,"workflow_run":{"id":7}}`, "artifact 1 has no created_at"),
	)
})

func artifact(id, runID int64) string {
	return fmt.Sprintf(`{"id":%d,"created_at":"2026-10-03T14:24:12Z","workflow_run":{"id":%d}}`, id, runID)
}
