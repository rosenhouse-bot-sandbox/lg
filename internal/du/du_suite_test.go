package du_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDu(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Du Suite")
}
