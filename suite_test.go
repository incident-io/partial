package partial_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestPartial(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Partial Suite")
}
