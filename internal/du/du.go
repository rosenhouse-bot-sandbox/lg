// Package du measures disk usage the way du does.
package du

import (
	"fmt"
	"io/fs"
	"syscall"
)

// Usage sums the disk space that files allocate, as du does.
type Usage struct {
	Bytes int64
	seen  map[inode]bool
}

type inode struct {
	dev int64
	ino uint64
}

// Add counts info's blocks, unless u has counted another link to its file.
func (u *Usage) Add(path string, info fs.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s: no block count", path)
	}
	if st.Nlink > 1 && !info.IsDir() {
		id := inode{int64(st.Dev), st.Ino}
		if u.seen[id] {
			return nil
		}
		if u.seen == nil {
			u.seen = map[inode]bool{}
		}
		u.seen[id] = true
	}
	u.Bytes += st.Blocks * 512
	return nil
}

// Human formats n as GNU du -h does: in powers of 1024, rounded up.
func Human(n int64) string {
	suffixes := []string{"K", "M", "G", "T", "P"}
	for k := len(suffixes); k > 0; k-- {
		unit := int64(1) << (10 * k)
		if n < unit {
			continue
		}
		if n < 10*unit {
			if tenths := ceilDiv(n*10, unit); tenths < 100 {
				return fmt.Sprintf("%d.%d%s", tenths/10, tenths%10, suffixes[k-1])
			}
		}
		if whole := ceilDiv(n, unit); whole < 1024 || k == len(suffixes) {
			return fmt.Sprintf("%d%s", whole, suffixes[k-1])
		}
		return "1.0" + suffixes[k]
	}
	return fmt.Sprint(n)
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }
