package du_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse-bot-sandbox/lg/internal/du"
)

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
