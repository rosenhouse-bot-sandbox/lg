package recordings_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse-bot-sandbox/lg/internal/model"
	"github.com/rosenhouse-bot-sandbox/lg/internal/testsupport/recordings"
)

// These specs pin the behavior of GitHub's Actions API that lg's design
// depends on, as testdata/recordings captured it from the lg-fixture
// workflow (.github/workflows/lg-fixture.yml). Run 37129390741 has three
// attempts: attempt 2 re-ran failed jobs and attempt 3 re-ran all jobs.
// GitHub deleted every log of run 37129738159.
var _ = Describe("GitHub's Actions API, as recorded", Label("github"), func() {
	const fixture, deletedLogs = int64(37129390741), int64(37129738159)

	Describe("re-run failed jobs", func() {
		It("lists every job again under a new id with run_attempt N", func() {
			first, second := rawJobs(fixture, "after-attempt-3", 1), rawJobs(fixture, "after-attempt-3", 2)
			Expect(second).To(HaveLen(len(first)))
			ids := map[int64]bool{}
			for _, job := range first {
				ids[job.ID] = true
			}
			for _, job := range second {
				Expect(ids).NotTo(HaveKey(job.ID))
				Expect(job.RunAttempt).To(Equal(2))
			}
		})

		It("copies the timestamps, runner and log of each job it did not re-run", func() {
			attempt, err := recordings.Attempt(fixture, "after-attempt-3", 2)
			Expect(err).NotTo(HaveOccurred())
			originals := map[string]rawJob{}
			for _, job := range rawJobs(fixture, "after-attempt-3", 1) {
				originals[job.copyKey()] = job
			}
			copied := 0
			for _, job := range rawJobs(fixture, "after-attempt-3", 2) {
				if !job.StartedAt.Before(attempt.RunStartedAt) {
					continue
				}
				original, ok := originals[job.copyKey()]
				Expect(ok).To(BeTrue(), "no attempt-1 job matches %s", job.copyKey())
				Expect(log(fixture, "after-attempt-3", 2, job.ID)).To(Equal(log(fixture, "after-attempt-3", 1, original.ID)))
				copied++
			}
			Expect(copied).To(BeNumerically(">", 0))
		})
	})

	It("never changes a completed attempt's attempt.json or jobs.json", func() {
		for attempt, stage := range map[int]string{1: "after-attempt-1", 2: "after-attempt-2"} {
			dir := fmt.Sprintf("attempt-%d", attempt)
			for _, name := range []string{"attempt.json", "jobs.json"} {
				before := read(recordings.Dir(fixture, stage), dir, name)
				Expect(read(recordings.Dir(fixture, "after-expiry"), dir, name)).To(Equal(before), dir+"/"+name)
			}
		}
	})

	It("lists the jobs of every attempt for jobs?filter=all", func() {
		var all struct{ Jobs []rawJob }
		Expect(json.Unmarshal(read(recordings.Dir(fixture, "after-attempt-3"), "jobs-all.json"), &all)).To(Succeed())
		Expect(all.Jobs).To(HaveLen(3 * len(rawJobs(fixture, "after-attempt-3", 1))))
	})

	It("allows two jobs of one name in an attempt", func() {
		names := map[string]int{}
		for _, job := range rawJobs(fixture, "after-attempt-1", 1) {
			names[job.Name]++
		}
		Expect(names).To(HaveKeyWithValue("same name", 2))
	})

	It("gives a skipped job no steps and no runner, but start and end times, and a log that blob storage 404s", func() {
		skipped := jobNamed(rawJobs(fixture, "after-attempt-1", 1), "skipped")
		Expect(skipped.Steps).To(BeEmpty())
		Expect(skipped.RunnerName).To(BeNil())
		Expect(skipped.StartedAt).NotTo(BeNil())
		Expect(skipped.CompletedAt).NotTo(BeNil())
		Expect(status(fixture, "after-attempt-1")).To(ContainElement(recordings.Line{First: 302, Final: 404, Path: fmt.Sprintf("jobs/%d/logs", skipped.ID)}))
		Expect(string(log(fixture, "after-attempt-1", 1, skipped.ID))).To(ContainSubstring("<Code>BlobNotFound</Code>"))
	})

	It("reports success for a continue-on-error step that fails", func() {
		pass := jobNamed(rawJobs(fixture, "after-attempt-1", 1), "pass")
		Expect(pass.Steps).To(ContainElement(SatisfyAll(
			HaveField("Name", "Failing step that does not fail the job"),
			HaveField("Conclusion", "success"))))
	})

	It("concludes a timed-out job cancelled", func() {
		Expect(jobNamed(rawJobs(fixture, "after-attempt-1", 1), "timeout").Conclusion).To(Equal("cancelled"))
	})

	Describe("logs", func() {
		It("start with a UTF-8 BOM and show annotations, masks and invalid UTF-8 as served", func() {
			for _, job := range served(fixture, "after-attempt-1", 1) {
				Expect(log(fixture, "after-attempt-1", 1, job.ID)).To(HavePrefix("\uFEFF"), job.Name)
			}
			pass := string(log(fixture, "after-attempt-1", 1, jobNamed(rawJobs(fixture, "after-attempt-1", 1), "pass").ID))
			for _, level := range []string{"warning", "notice", "error"} {
				Expect(pass).To(ContainSubstring("##[%s]LG_MARKER %s annotation", level, level))
			}
			Expect(pass).To(ContainSubstring("LG_MARKER masked ***"))
			Expect(pass).To(ContainSubstring("echo \"LG_MARKER masked lg-secret-value\""))
			Expect(pass).To(ContainSubstring("non-utf8 byte \uFFFD"))
		})

		It("404 at the API hop, without a redirect, once deleted", func() {
			for _, line := range status(deletedLogs, "logs-deleted") {
				if strings.HasSuffix(line.Path, "/logs") {
					Expect(line).To(HaveField("First", 404), line.Path)
				}
			}
			job := rawJobs(deletedLogs, "logs-deleted", 1)[0]
			Expect(string(log(deletedLogs, "logs-deleted", 1, job.ID))).To(ContainSubstring(`"message": "Not Found"`))
		})

		It("are in the attempt logs zip byte-identical, but under job names that replace / and drop :", func() {
			zipped := map[string]string{}
			for _, f := range logsZip(fixture, "after-attempt-1", 1) {
				if !strings.Contains(f.Name, "/") {
					zipped[f.Name] = digest(unzip(f))
				}
			}
			Expect(zipped).To(HaveKey(HavePrefix("9_ünïcode _ slash job with")))
			Expect(zipped).To(HaveKey(HaveSuffix("_same name.txt")))
			jobs := served(fixture, "after-attempt-1", 1)
			Expect(zipped).To(HaveLen(len(jobs)))
			for _, job := range jobs {
				Expect(zipped).To(ContainElement(digest(log(fixture, "after-attempt-1", 1, job.ID))), job.Name)
			}
		})

		It("leave the attempt logs zip, but not the per-job endpoint, within a day", func() {
			Expect(logsZip(fixture, "after-expiry", 1)).To(HaveLen(20))
			Expect(logsZip(fixture, "after-attempt-1", 1)).To(HaveLen(59))
			for _, job := range served(fixture, "after-expiry", 1) {
				Expect(log(fixture, "after-expiry", 1, job.ID)).To(Equal(log(fixture, "after-attempt-1", 1, job.ID)), job.Name)
			}
		})
	})

	Describe("artifacts", func() {
		It("are all deleted by re-run all jobs", func() {
			before := ids(listing(fixture, "after-attempt-2"))
			for _, artifact := range listing(fixture, "after-attempt-3") {
				Expect(before).NotTo(HaveKey(artifact.ID))
			}
			for id := range before {
				Expect(status(fixture, "after-expiry")).To(ContainElement(recordings.Line{First: 404, Final: 404, Path: fmt.Sprintf("artifacts/%d/zip", id)}))
			}
		})

		It("keep an earlier artifact of the same name under its own id, unless uploaded with overwrite: true", func() {
			const kept, overwritten = 11276401837, 11276182467
			first, second := ids(listing(fixture, "after-attempt-1")), ids(listing(fixture, "after-attempt-2"))
			Expect(first).To(SatisfyAll(HaveKey(int64(kept)), HaveKey(int64(overwritten))))
			Expect(second).To(HaveKey(int64(kept)))
			Expect(second).NotTo(HaveKey(int64(overwritten)))
			named := map[string]int{}
			for _, artifact := range listing(fixture, "after-attempt-2") {
				named[artifact.Name]++
			}
			Expect(named).To(HaveKeyWithValue("flaky-report", 2))
			Expect(named).To(HaveKeyWithValue("flaky-report-overwrite", 1))
		})

		It("have ids that are not ordered by creation time", func() {
			artifacts := listing(fixture, "after-attempt-1")
			Expect(artifacts).To(ContainElement(Satisfy(func(earlier model.Artifact) bool {
				return slices.ContainsFunc(artifacts, func(later model.Artifact) bool {
					return later.CreatedAt.After(earlier.CreatedAt) && later.ID < earlier.ID
				})
			})))
		})

		It("leave the listing once expired, with no expired: true entry, and their zip answers 410 at the API hop", func() {
			const expired = 11276327411
			Expect(ids(listing(fixture, "after-attempt-3"))).To(HaveKey(int64(expired)))
			Expect(ids(listing(fixture, "after-expiry"))).NotTo(HaveKey(int64(expired)))
			Expect(status(fixture, "after-expiry")).To(ContainElement(recordings.Line{First: 410, Final: 410, Path: fmt.Sprintf("artifacts/%d/zip", expired)}))
			Expect(string(read(recordings.Dir(fixture, "after-expiry"), "artifacts", fmt.Sprintf("%d.zip", expired)))).To(ContainSubstring(`"message": "Artifact has expired"`))
		})

		It("answer 404, not 410, past expires_at when a rerun deleted them first", func() {
			const deleted = 11275917910
			var artifact model.Artifact
			for _, a := range listing(fixture, "after-attempt-2") {
				if a.ID == deleted {
					artifact = a
				}
			}
			recordedAt, err := recordings.RecordedAt(fixture, "after-expiry")
			Expect(err).NotTo(HaveOccurred())
			Expect(artifact.ExpiresAt).To(BeTemporally("<", recordedAt))
			Expect(status(fixture, "after-expiry")).To(ContainElement(recordings.Line{First: 404, Final: 404, Path: fmt.Sprintf("artifacts/%d/zip", deleted)}))
			Expect(string(read(recordings.Dir(fixture, "after-expiry"), "artifacts", fmt.Sprintf("%d.zip", deleted)))).To(ContainSubstring(`"message": "Not Found"`))
		})
	})
})

// rawJob holds the fields of a listed job that model.Job leaves out.
type rawJob struct {
	model.Job
	RunAttempt int `json:"run_attempt"`
}

// copyKey identifies the job a rerun copied: names repeat, so it adds the times and runner.
func (j rawJob) copyKey() string {
	runner := ""
	if j.RunnerName != nil {
		runner = *j.RunnerName
	}
	return fmt.Sprintf("%s|%s|%s|%s", j.Name, j.StartedAt, j.CompletedAt, runner)
}

func rawJobs(runID int64, stage string, attempt int) []rawJob {
	var listing struct{ Jobs []rawJob }
	ExpectWithOffset(1, json.Unmarshal(read(recordings.Dir(runID, stage), fmt.Sprintf("attempt-%d", attempt), "jobs.json"), &listing)).To(Succeed())
	return listing.Jobs
}

// served gives the jobs of an attempt whose log GitHub served.
func served(runID int64, stage string, attempt int) []rawJob {
	ok := map[string]bool{}
	for _, line := range status(runID, stage) {
		ok[line.Path] = line.Final == 200
	}
	var jobs []rawJob
	for _, job := range rawJobs(runID, stage, attempt) {
		if ok[fmt.Sprintf("jobs/%d/logs", job.ID)] {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

func digest(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }

func jobNamed(jobs []rawJob, name string) rawJob {
	for _, job := range jobs {
		if job.Name == name {
			return job
		}
	}
	Fail("no job named " + name)
	return rawJob{}
}

func log(runID int64, stage string, attempt int, jobID int64) []byte {
	return read(recordings.Dir(runID, stage), fmt.Sprintf("attempt-%d", attempt), "logs", fmt.Sprintf("%d.txt", jobID))
}

func listing(runID int64, stage string) []model.Artifact {
	artifacts, err := recordings.Artifacts(runID, stage)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return artifacts
}

func ids(artifacts []model.Artifact) map[int64]bool {
	set := map[int64]bool{}
	for _, a := range artifacts {
		set[a.ID] = true
	}
	return set
}

func status(runID int64, stage string) []recordings.Line {
	lines, err := recordings.ParseStatus(bytes.NewReader(read(recordings.Dir(runID, stage), "status.txt")))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return lines
}

func read(elem ...string) []byte {
	raw, err := os.ReadFile(filepath.Join(elem...))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return raw
}

func logsZip(runID int64, stage string, attempt int) []*zip.File {
	zr, err := zip.OpenReader(filepath.Join(recordings.Dir(runID, stage), fmt.Sprintf("attempt-%d", attempt), "logs.zip"))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	DeferCleanup(zr.Close)
	return zr.File
}

func unzip(f *zip.File) []byte {
	r, err := f.Open()
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	defer func() { _ = r.Close() }()
	raw, err := io.ReadAll(r)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return raw
}
