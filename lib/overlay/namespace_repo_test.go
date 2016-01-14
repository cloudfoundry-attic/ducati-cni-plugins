package overlay_test

import (
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/overlay"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("NamespaceRepo", func() {
	Describe("Find", func() {
		It("should open the path on the filesystem", func() {
			repo := overlay.NamespaceRepository{}

			file, err := repo.Find("/")
			Expect(err).NotTo(HaveOccurred())

			info, err := file.Stat()
			Expect(err).NotTo(HaveOccurred())
			Expect(info.IsDir()).To(BeTrue())
		})
	})
})
