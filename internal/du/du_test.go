package du_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse-bot-sandbox/lg/internal/du"
)

var _ = Describe("Bytes", func() {
	duK := func(dir string) int64 {
		out, err := exec.Command("du", "-sk", dir).Output()
		Expect(err).NotTo(HaveOccurred())
		k, err := strconv.ParseInt(strings.Fields(string(out))[0], 10, 64)
		Expect(err).NotTo(HaveOccurred())
		return k
	}

	It("agrees with du -sk, counting a hard-linked file once and a sparse file by its blocks", func() {
		dir := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "a", "small"), []byte("x"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "a", "b", "big"), make([]byte, 300_000), 0o644)).To(Succeed())
		Expect(os.Link(filepath.Join(dir, "a", "b", "big"), filepath.Join(dir, "big-link"))).To(Succeed())
		Expect(os.Symlink("a", filepath.Join(dir, "symlink"))).To(Succeed())
		sparse, err := os.Create(filepath.Join(dir, "sparse"))
		Expect(err).NotTo(HaveOccurred())
		Expect(sparse.Truncate(1 << 30)).To(Succeed())
		Expect(sparse.Close()).To(Succeed())

		n, err := du.Bytes(dir)

		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeNumerically(">", 300_000))
		Expect((n + 1023) / 1024).To(Equal(duK(dir)))
	})

	It("follows a symlinked dir", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "f"), make([]byte, 300_000), 0o644)).To(Succeed())
		link := filepath.Join(GinkgoT().TempDir(), "link")
		Expect(os.Symlink(dir, link)).To(Succeed())

		Expect(du.Bytes(link)).To(Equal(must(du.Bytes(dir))))
	})

	It("counts what it can read, and gives the error of a dir it cannot", func() {
		if os.Geteuid() == 0 {
			Skip("root reads any dir")
		}
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "f"), make([]byte, 300_000), 0o644)).To(Succeed())
		locked := filepath.Join(dir, "locked")
		Expect(os.Mkdir(locked, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(locked, "g"), make([]byte, 300_000), 0o644)).To(Succeed())
		Expect(os.Chmod(locked, 0)).To(Succeed())
		DeferCleanup(os.Chmod, locked, os.FileMode(0o755))

		n, err := du.Bytes(dir)

		Expect(err).To(MatchError(ContainSubstring(locked)))
		Expect(n).To(BeNumerically(">", 300_000))
		Expect(n).To(BeNumerically("<", 600_000))
	})

	It("gives 0 for a missing dir", func() {
		Expect(du.Bytes(filepath.Join(GinkgoT().TempDir(), "missing"))).To(BeZero())
	})
})

func must(n int64, err error) int64 {
	GinkgoHelper()
	Expect(err).NotTo(HaveOccurred())
	return n
}

var _ = DescribeTable("Human matches GNU du -h",
	func(n int64, want string) { Expect(du.Human(n)).To(Equal(want)) },
	Entry(nil, int64(0), "0"),
	Entry(nil, int64(1023), "1023"),
	Entry(nil, int64(1024), "1.0K"),
	Entry(nil, int64(1025), "1.1K"),
	Entry(nil, int64(4096), "4.0K"),
	Entry(nil, int64(10*1024-1), "10K"),
	Entry(nil, int64(10*1024), "10K"),
	Entry(nil, int64(10*1024+1), "11K"),
	Entry(nil, int64(1023*1024+1), "1.0M"),
	Entry(nil, int64(1<<20-1), "1.0M"),
	Entry(nil, int64(1<<20+1), "1.1M"),
	Entry(nil, int64(10<<20+1), "11M"),
	Entry(nil, int64(1536<<20), "1.5G"),
	Entry(nil, int64(3<<40), "3.0T"),
)
