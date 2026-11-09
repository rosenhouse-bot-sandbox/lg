// Package du measures disk usage the way du does.
package du

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"syscall"
)

// Bytes gives the disk space that the tree at dir allocates, counting each
// hard-linked file once. It gives 0 when dir does not exist, and skips
// entries that vanish during the walk. Like du, it counts what it can read,
// and returns that total with the errors of what it could not.
func Bytes(dir string) (int64, error) {
	root, err := filepath.EvalSymlinks(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	type inode struct {
		dev int64
		ino uint64
	}
	seen := map[inode]bool{}
	var total int64
	var errs []error
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		var info fs.FileInfo
		if err == nil {
			info, err = d.Info()
		}
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("%s: no block count", path)
		}
		if st.Nlink > 1 {
			id := inode{int64(st.Dev), st.Ino}
			if seen[id] {
				return nil
			}
			seen[id] = true
		}
		total += st.Blocks * 512
		return nil
	})
	return total, errors.Join(append(errs, err)...)
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
