package fakegithub_test

import (
	"cmp"
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse-bot-sandbox/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse-bot-sandbox/lg/internal/testsupport/recordings"
)

var _ = Describe("fakegithub's repository artifact listing", Label("artifacts"), func() {
	var (
		fake      *fakegithub.Server
		artifacts string
		all       []json.RawMessage
	)

	BeforeEach(func() {
		fake = fakegithub.Start(runID, "after-attempt-1")
		Expect(fake.Load(logsDeletedRun, "logs-deleted")).To(Succeed())
		artifacts = fake.URL() + "/repos/rosenhouse-bot-sandbox/lg/actions/artifacts"
		all = slices.Concat(
			recordedElements(filepath.Join(recordings.Dir(runID, "after-attempt-1"), "artifacts.json"), "artifacts"),
			recordedElements(filepath.Join(recordings.Dir(logsDeletedRun, "logs-deleted"), "artifacts.json"), "artifacts"),
		)
		slices.SortFunc(all, func(a, b json.RawMessage) int { return cmp.Compare(artifactID(b), artifactID(a)) })
	})

	page := func(query string) (total int, elements []json.RawMessage) {
		GinkgoHelper()
		resp := fetch(artifacts + query)
		Expect(resp.status).To(Equal(http.StatusOK), query)
		var body struct {
			TotalCount int               `json:"total_count"`
			Artifacts  []json.RawMessage `json:"artifacts"`
		}
		Expect(json.Unmarshal(resp.body, &body)).To(Succeed())
		return body.TotalCount, body.Artifacts
	}

	It("lists every loaded run's artifacts, highest id first", func() {
		total, listed := page("?per_page=100")
		Expect(total).To(Equal(len(all)))
		expectJSONElements(listed, all)

		fake.Remove(logsDeletedRun)
		total, _ = page("?per_page=100")
		Expect(total).To(Equal(len(all) - len(recordedElements(filepath.Join(recordings.Dir(logsDeletedRun, "logs-deleted"), "artifacts.json"), "artifacts"))))
	})

	It("serves page n of size per_page from offset (n-1)*per_page, as GitHub does", func() {
		_, listed := page("?per_page=3&page=2")
		expectJSONElements(listed, all[3:6])
		_, listed = page("?per_page=5&page=2")
		expectJSONElements(listed, all[5:])
	})

	It("ignores direction, which only the run's listing takes", func() {
		_, listed := page("?direction=asc&per_page=100")
		expectJSONElements(listed, all)
	})

	It("lists only the artifacts named by name, and counts only those", func() {
		var named []json.RawMessage
		for _, a := range all {
			var meta struct{ Name string }
			Expect(json.Unmarshal(a, &meta)).To(Succeed())
			if meta.Name == "flaky-report" {
				named = append(named, a)
			}
		}
		Expect(named).NotTo(BeEmpty())
		total, listed := page("?name=flaky-report&per_page=100")
		Expect(total).To(Equal(len(named)))
		expectJSONElements(listed, named)
	})

	It("HoldAfter lets the first n matching requests through and delays the rest until released", func() {
		release := fake.HoldAfter("/actions/artifacts", 1)
		page("?per_page=2")
		done := make(chan []json.RawMessage)
		go func() {
			defer GinkgoRecover()
			_, listed := page("?per_page=2&page=2")
			done <- listed
		}()
		Consistently(done, 200*time.Millisecond).ShouldNot(Receive())

		fake.Remove(logsDeletedRun)
		release()
		var listed []json.RawMessage
		Eventually(done, time.Second).Should(Receive(&listed))
		_, served := page("?per_page=4")
		expectJSONElements(listed, served[2:])
	})
})

func artifactID(raw json.RawMessage) int64 {
	GinkgoHelper()
	var meta struct{ ID int64 }
	Expect(json.Unmarshal(raw, &meta)).To(Succeed())
	return meta.ID
}
