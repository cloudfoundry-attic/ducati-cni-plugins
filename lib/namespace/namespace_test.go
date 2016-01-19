package namespace_test

import (
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/namespace"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Namespace", func() {
	Describe("Path", func() {
		It("returns the path used on the constructor", func() {
			ns := namespace.NewNamespace("/some/path/name")
			Expect(ns.Path()).To(Equal("/some/path/name"))
		})
	})

	Describe("Name", func() {
		It("returns the basename of the underlying path", func() {
			ns := namespace.NewNamespace("/var/run/netns/foo")
			Expect(ns.Name()).To(Equal("foo"))

			ns = namespace.NewNamespace("/foo")
			Expect(ns.Name()).To(Equal("foo"))

			ns = namespace.NewNamespace("/foo/bar")
			Expect(ns.Name()).To(Equal("bar"))
		})
	})

	Describe("Open", func() {
		var tempDir string
		var ns namespace.Namespace

		BeforeEach(func() {
			var err error
			tempDir, err = ioutil.TempDir("", "ns")
			Expect(err).NotTo(HaveOccurred())

			nsPath := filepath.Join(tempDir, "namespace")
			nsFile, err := os.Create(nsPath)
			Expect(err).NotTo(HaveOccurred())
			nsFile.Close()

			ns = namespace.NewNamespace(nsPath)
		})

		AfterEach(func() {
			os.RemoveAll(tempDir)
		})

		It("returns an open file representing the namesapce", func() {
			f, err := ns.Open()
			Expect(err).NotTo(HaveOccurred())
			Expect(f.Name()).To(Equal(ns.Path()))
			f.Close()
		})

		Context("when open fails", func() {
			BeforeEach(func() {
				err := os.Remove(ns.Path())
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns the error from open", func() {
				_, err := ns.Open()
				Expect(err).To(HaveOccurred())
			})
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
			ns := namespace.NewNamespace("/var/run/netns/ns-test-ns")

			var stat syscall.Stat_t
			closure := func(f *os.File) error {
				return syscall.Stat("/proc/self/ns/net", &stat)
			}

			err := ns.Execute(closure)
			Expect(err).NotTo(HaveOccurred())
			Expect(stat.Ino).To(Equal(nsInode))
		})
	})

	Describe("Destroy", func() {
		It("removes the namespace bind mount and file", func() {
			err := exec.Command("ip", "netns", "add", "destroy-ns-test").Run()
			Expect(err).NotTo(HaveOccurred())

			ns := namespace.NewNamespace("/var/run/netns/destroy-ns-test")
			err = ns.Destroy()
			Expect(err).NotTo(HaveOccurred())

			var stat syscall.Stat_t
			err = syscall.Stat(ns.Path(), &stat)
			Expect(err).To(HaveOccurred())
			Expect(os.IsNotExist(err)).To(BeTrue())
		})

		Context("when the naemspace file does not exist", func() {
			It("returns an error", func() {
				ns := namespace.NewNamespace("/var/run/netns/non-existent")
				err := ns.Destroy()
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when the namespace file is not a bind mount", func() {
			var nsPath string

			BeforeEach(func() {
				nsPath = filepath.Join("/var/run/netns", "simple-file")
				_, err := os.Create(nsPath)
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns an error", func() {
				ns := namespace.NewNamespace(nsPath)
				err := ns.Destroy()
				Expect(err).To(HaveOccurred())
			})

			It("does not remove the file", func() {
				f, err := os.Open(nsPath)
				Expect(err).NotTo(HaveOccurred())
				f.Close()
			})
		})
	})
})
