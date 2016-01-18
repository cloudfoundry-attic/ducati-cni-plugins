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

var _ = Describe("NamespaceRepo", func() {
	var repoDir string

	BeforeEach(func() {
		var err error
		repoDir, err = ioutil.TempDir("", "ns-repo")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		err := os.RemoveAll(repoDir)
		Expect(err).NotTo(HaveOccurred())
	})

	Describe("NewRepository", func() {
		It("returns a repository", func() {
			repo, err := namespace.NewRepository(repoDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(repo).NotTo(BeNil())
		})

		Context("when the target directory does not exist", func() {
			BeforeEach(func() {
				err := os.RemoveAll(repoDir)
				Expect(err).NotTo(HaveOccurred())
			})

			It("creates the directory", func() {
				_, err := namespace.NewRepository(repoDir)
				Expect(err).NotTo(HaveOccurred())

				info, err := os.Stat(repoDir)
				Expect(err).NotTo(HaveOccurred())
				Expect(info.IsDir()).To(BeTrue())
			})
		})
	})

	Describe("Get", func() {
		var repo namespace.Repository

		BeforeEach(func() {
			var err error
			repo, err = namespace.NewRepository(repoDir)
			Expect(err).NotTo(HaveOccurred())
		})

		Context("when the namespace file does not exist", func() {
			It("returns ErrNotExist", func() {
				_, err := repo.Get("test-ns")
				Expect(err).To(HaveOccurred())
				Expect(os.IsNotExist(err)).To(BeTrue())
			})
		})

		Context("when the namespace file exists", func() {
			BeforeEach(func() {
				f, err := os.Create(filepath.Join(repoDir, "test-ns"))
				Expect(err).NotTo(HaveOccurred())
				Expect(f.Close()).To(Succeed())
			})

			It("returns the namespace", func() {
				ns, err := repo.Get("test-ns")
				Expect(err).NotTo(HaveOccurred())
				Expect(ns.Name()).To(Equal("test-ns"))
			})
		})
	})

	Describe("Create", func() {
		var repo namespace.Repository

		BeforeEach(func() {
			var err error
			repo, err = namespace.NewRepository(repoDir)
			Expect(err).NotTo(HaveOccurred())
		})

		It("creates a namespace in the repository", func() {
			ns, err := repo.Create("test-ns")
			Expect(err).NotTo(HaveOccurred())
			Expect(ns.Name()).To(Equal("test-ns"))

			nsPath := filepath.Join(repoDir, "test-ns")
			defer syscall.Unmount(nsPath, syscall.MNT_DETACH)

			var repoStat syscall.Stat_t
			err = syscall.Stat(nsPath, &repoStat)
			Expect(err).NotTo(HaveOccurred())

			var nsSelfStat syscall.Stat_t
			callback := func() error {
				return syscall.Stat("/proc/self/ns/net", &nsSelfStat)
			}
			err = ns.Execute(callback)
			Expect(err).NotTo(HaveOccurred())

			Expect(repoStat.Ino).To(Equal(nsSelfStat.Ino))
		})

		It("should not show up in ip netns list", func() {
			_, err := repo.Create("test-ns")
			Expect(err).NotTo(HaveOccurred())

			nsPath := filepath.Join(repoDir, "test-ns")
			defer syscall.Unmount(nsPath, syscall.MNT_DETACH)

			output, err := exec.Command("ip", "netns", "list").CombinedOutput()
			Expect(err).NotTo(HaveOccurred())
			Expect(output).NotTo(ContainSubstring("test-ns"))
		})

		Context("when the namespace file already exists", func() {
			BeforeEach(func() {
				f, err := os.Create(filepath.Join(repoDir, "test-ns"))
				Expect(err).NotTo(HaveOccurred())
				f.Close()
			})

			AfterEach(func() {
				os.RemoveAll(filepath.Join(repoDir, "test-ns"))
			})

			It("returns ErrExist", func() {
				_, err := repo.Create("test-ns")
				Expect(err).To(HaveOccurred())
				Expect(os.IsExist(err)).To(BeTrue())
			})
		})

		Context("when ip netns add fails", func() {
			BeforeEach(func() {
				err := exec.Command("ip", "netns", "add", "test-ns").Run()
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				err := exec.Command("ip", "netns", "delete", "test-ns").Run()
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns an error", func() {
				_, err := repo.Create("test-ns")
				Expect(err).To(HaveOccurred())
			})

			It("deletes the namspace file in the target repository", func() {
				_, err := repo.Create("test-ns")
				Expect(err).To(HaveOccurred())

				path := filepath.Join(repoDir, "test-ns")
				_, err = os.Open(path)
				Expect(err).To(HaveOccurred())
				Expect(os.IsNotExist(err)).To(BeTrue())
			})
		})
	})
})
