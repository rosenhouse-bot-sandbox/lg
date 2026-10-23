package mirror_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse-bot-sandbox/lg/internal/failure"
	"github.com/rosenhouse-bot-sandbox/lg/internal/mirror"
	"github.com/rosenhouse-bot-sandbox/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse-bot-sandbox/lg/internal/testsupport/harness"
	"github.com/rosenhouse-bot-sandbox/lg/internal/testsupport/scenario"
)

var _ = Describe("mirror.Cycle catching up on many completed runs", Label("artifacts"), func() {
	const inProgressRun = 5

	var env *harness.InProcessEnv

	BeforeEach(func() {
		mirror.SetCatchUpRuns(2)
		DeferCleanup(mirror.SetCatchUpRuns, mirror.DefaultCatchUpRuns)
		env = harness.InProcess()
		Expect(env.Fake.Load(runID, "after-attempt-1")).To(Succeed())
		Expect(env.Fake.Load(deletedRun, "logs-deleted")).To(Succeed())
	})

	listingsOf := func(suffix string) int {
		n := 0
		for _, r := range env.Fake.Requests() {
			if strings.HasSuffix(r.Path, suffix) {
				n++
			}
		}
		return n
	}
	runListings := func() int {
		n := 0
		for _, r := range env.Fake.Requests() {
			if strings.HasPrefix(r.Path, "/repos/rosenhouse-bot-sandbox/lg/actions/runs/") && strings.HasSuffix(r.Path, "/artifacts") {
				n++
			}
		}
		return n
	}

	It("lists the repo's artifacts once in place of each run's listing, and publishes them as the run's listing would", func(ctx SpecContext) {
		Expect(env.Sync(ctx)).To(Succeed())
		Expect(listingsOf("/actions/artifacts")).To(Equal(1))
		Expect(runListings()).To(BeZero())

		repoListing := env.Fake.URL() + "/repos/rosenhouse-bot-sandbox/lg/actions/artifacts?per_page=100"
		for _, id := range []int64{runID, deletedRun} {
			recorded := scenario.Recorded(id, map[int64]string{runID: "after-attempt-1", deletedRun: "logs-deleted"}[id])
			var listing struct{ Artifacts []json.RawMessage }
			Expect(json.Unmarshal(recorded.Files["artifacts.json"].Data, &listing)).To(Succeed())
			Expect(env.ArtifactDirs(id)).To(HaveLen(len(listing.Artifacts)))

			attempts := env.AttemptDirs(id)
			Expect(attempts).To(HaveLen(1))
			snapshot, err := os.ReadFile(filepath.Join(attempts[0], "artifacts.json"))
			Expect(err).NotTo(HaveOccurred())
			recordedArtifacts, err := json.Marshal(listing.Artifacts)
			Expect(err).NotTo(HaveOccurred())
			Expect(snapshot).To(MatchJSON(recordedArtifacts))
			Expect(readJSONFile(filepath.Join(attempts[0], "fetch.json"))).To(HaveKeyWithValue("sources",
				HaveKeyWithValue("artifacts.json", SatisfyAll(HaveKeyWithValue("url", repoListing), HaveKeyWithValue("pages", BeEquivalentTo(1))))))
			for _, dir := range env.ArtifactDirs(id) {
				Expect(readJSONFile(filepath.Join(dir, "fetch.json"))).To(HaveKeyWithValue("sources",
					HaveKeyWithValue("artifact.json", HaveKeyWithValue("url", repoListing))))
			}
		}
	})

	It("lists the artifacts of a run not completed when listed from the run, which may still add some", func(ctx SpecContext) {
		Expect(env.Fake.AddRun(scenario.InProgress(scenario.Clone(scenario.Recorded(runID, "after-attempt-1"), inProgressRun), 1))).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(listingsOf("/actions/artifacts")).To(Equal(1))
		Expect(runListings()).To(Equal(1))
		Expect(listingsOf(fmt.Sprintf("/runs/%d/artifacts", inProgressRun))).To(Equal(1))
	})

	It("lists no repo artifacts for fewer completed runs than catchUpRuns", func(ctx SpecContext) {
		mirror.SetCatchUpRuns(3)
		Expect(env.Fake.AddRun(scenario.InProgress(scenario.Clone(scenario.Recorded(runID, "after-attempt-1"), inProgressRun), 1))).To(Succeed())

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(listingsOf("/actions/artifacts")).To(BeZero())
		Expect(runListings()).To(Equal(3))
	})

	It("lists each run's artifacts when the repo's listing needs as many pages as there are runs", func(ctx SpecContext) {
		env.Fake.SetPageCap(7)

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(listingsOf("/actions/artifacts")).To(Equal(1))
		Expect(runListings()).To(Equal(2))
		Expect(env.ArtifactDirs(runID)).To(HaveLen(4))
	})

	It("lists each run's artifacts when the repo's listing fails for a reason confined to it", func(ctx SpecContext) {
		env.Fake.Fail("api", "/lg/actions/artifacts", fakegithub.Fault{Status: http.StatusInternalServerError})

		Expect(env.Sync(ctx)).To(Succeed())
		Expect(runListings()).To(Equal(2))
		Expect(env.ArtifactDirs(runID)).To(HaveLen(4))
	})

	It("stops the cycle when the repo's listing fails for every run", func(ctx SpecContext) {
		env.Fake.Fail("api", "/lg/actions/artifacts", fakegithub.Fault{Status: http.StatusUnauthorized})

		err := env.Sync(ctx)
		var blocked failure.Blocked
		Expect(err).To(BeAssignableToTypeOf(blocked))
		Expect(runListings()).To(BeZero())
	})
})

var _ = Describe("mirror.Cycle over 100 completed runs", Label("artifacts"), func() {
	It("lists their 400 artifacts in 5 overlapping pages in place of 100 run listings", func(ctx SpecContext) {
		env := harness.InProcess()
		for id := int64(1); id <= mirror.DefaultCatchUpRuns; id++ {
			Expect(env.Fake.AddRun(scenario.Clone(scenario.Recorded(runID, "after-attempt-1"), id))).To(Succeed())
		}

		Expect(env.Sync(ctx)).To(Succeed())
		listings := map[bool]int{}
		for _, r := range env.Fake.Requests() {
			if strings.HasSuffix(r.Path, "/artifacts") {
				listings[strings.HasSuffix(r.Path, "/lg/actions/artifacts")]++
			}
		}
		Expect(listings).To(Equal(map[bool]int{true: 5}))
		Expect(env.ArtifactDirs(mirror.DefaultCatchUpRuns)).To(HaveLen(4))
	})
})
