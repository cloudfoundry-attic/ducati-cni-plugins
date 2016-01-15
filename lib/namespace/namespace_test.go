package namespace_test

import (
	"os/exec"
	"syscall"

	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/namespace"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Namespace", func() {
	Describe("Name", func() {
		It("returns the basename of the underlying path", func() {
			ns := &namespace.Namespace{Path: "/var/run/netns/foo"}
			Expect(ns.Name()).To(Equal("foo"))

			ns = &namespace.Namespace{Path: "/foo"}
			Expect(ns.Name()).To(Equal("foo"))

			ns = &namespace.Namespace{Path: "/foo/bar"}
			Expect(ns.Name()).To(Equal("bar"))
		})
	})

	Describe("Execute", func() {
		var nsInode uint64

		BeforeEach(func() {
			err := exec.Command("ip", "netns", "add", "ns-test-ns").Run()
			Expect(err).NotTo(HaveOccurred())

			var stat syscall.Stat_t
			err = syscall.Stat("/var/run/netns/ns-test-ns", &stat)
			Expect(err).NotTo(HaveOccurred())

			nsInode = stat.Ino
		})

		AfterEach(func() {
			err := exec.Command("ip", "netns", "delete", "ns-test-ns").Run()
			Expect(err).NotTo(HaveOccurred())
		})

		It("runs the closure in the namespace", func() {
			ns := &namespace.Namespace{Path: "/var/run/netns/ns-test-ns"}

			var stat syscall.Stat_t
			closure := func() error {
				return syscall.Stat("/proc/self/ns/net", &stat)
			}

			err := ns.Run(closure)
			Expect(err).NotTo(HaveOccurred())
			Expect(stat.Ino).To(Equal(nsInode))
		})
	})
})
