package acceptance_test

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/appc/cni/pkg/types"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/namespace"
	"github.com/onsi/gomega/gexec"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/vishvananda/netlink"
)

var _ = Describe("DEL", func() {
	var (
		netConfig Config
		session   *gexec.Session

		repoDir       string
		containerID   string
		containerNS   namespace.Namespace
		sandboxNS     namespace.Namespace
		namespaceRepo namespace.Repository

		sandboxRepoDir string
	)

	BeforeEach(func() {
		var err error
		repoDir, err = ioutil.TempDir("", "vxlan")
		Expect(err).NotTo(HaveOccurred())

		namespaceRepo, err = namespace.NewRepository(repoDir)
		Expect(err).NotTo(HaveOccurred())

		containerNS, err = namespaceRepo.Create("container-ns")
		Expect(err).NotTo(HaveOccurred())

		sandboxRepoDir, err = ioutil.TempDir("", "sandbox")
		Expect(err).NotTo(HaveOccurred())

		containerID = "guid-1"

		netConfig = Config{
			Name:        "test-network",
			Type:        "vxlan",
			Network:     "192.168.1.0/24",
			HostNetwork: "10.99.0.0/24",
			IPAM: IPAM{
				Type:   "host-local",
				Subnet: "192.168.1.0/24",
				Routes: []map[string]string{
					{"dst": "0.0.0.0/0"},
				},
			},
		}
	})

	AfterEach(func() {
		containerNS.Destroy()
		sandboxNS.Destroy()
		os.RemoveAll(repoDir)
	})

	Context("when delete goes smoothly", func() {
		var containerAddress string

		BeforeEach(func() {
			sandboxNS, session = execCNI("ADD", netConfig, containerNS, containerID, sandboxRepoDir)
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

			var result types.Result
			err := json.Unmarshal(session.Out.Contents(), &result)
			Expect(err).NotTo(HaveOccurred())

			containerAddress = result.IP4.IP.IP.String()
		})

		It("should release the IPAM managed address", func() {
			sandboxNS, session = execCNI("DEL", netConfig, containerNS, containerID, sandboxRepoDir)
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

			addressPath := filepath.Join("/var/lib/cni/networks", "test-network", containerAddress)
			_, err := os.Open(addressPath)
			Expect(err).To(HaveOccurred())
			Expect(os.IsNotExist(err)).To(BeTrue())
		})

		Context("when the last container leaves the network", func() {
			It("should remove the sandboxNS", func() {
				sandboxNS, session = execCNI("DEL", netConfig, containerNS, containerID, sandboxRepoDir)
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

				_, err := sandboxNS.Open()
				Expect(err).To(HaveOccurred())
				Expect(os.IsNotExist(err)).To(BeTrue())
			})
		})

		Context("when a container remains attached to the sandbox", func() {
			var containerNS2 namespace.Namespace

			BeforeEach(func() {
				var err error
				containerNS2, err = namespaceRepo.Create("container-ns-2")
				Expect(err).NotTo(HaveOccurred())

				sandboxNS, session = execCNI("ADD", netConfig, containerNS2, "guid-2", sandboxRepoDir)
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))
			})

			AfterEach(func() {
				sandboxNS, session = execCNI("DEL", netConfig, containerNS2, "guid-2", sandboxRepoDir)
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))
			})

			It("should remove the veth pair from the container and sandbox namespaces", func() {
				sandboxNS, session = execCNI("DEL", netConfig, containerNS, containerID, sandboxRepoDir)
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

				err := containerNS.Execute(func(_ *os.File) error {
					_, err := netlink.LinkByName("vx-eth0")
					Expect(err).To(HaveOccurred())
					Expect(err).To(MatchError("Link not found"))
					return nil
				})
				Expect(err).NotTo(HaveOccurred())

				err = sandboxNS.Execute(func(_ *os.File) error {
					_, err := netlink.LinkByName(containerID)
					Expect(err).To(HaveOccurred())
					Expect(err).To(MatchError("Link not found"))
					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should preserve the veth pairs for other attached containers", func() {
				sandboxNS, session = execCNI("DEL", netConfig, containerNS, containerID, sandboxRepoDir)
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

				err := containerNS2.Execute(func(_ *os.File) error {
					_, err := netlink.LinkByName("vx-eth0")
					Expect(err).NotTo(HaveOccurred())
					return nil
				})
				Expect(err).NotTo(HaveOccurred())

				err = sandboxNS.Execute(func(_ *os.File) error {
					_, err := netlink.LinkByName("guid-2")
					Expect(err).NotTo(HaveOccurred())
					return nil
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})
	})

	Context("when the container namespace is invalid", func() {
		BeforeEach(func() {
			sandboxNS, session = execCNI("ADD", netConfig, containerNS, containerID, sandboxRepoDir)
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

			var result types.Result
			err := json.Unmarshal(session.Out.Contents(), &result)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should return an error", func() {
			sandboxNS, session = execCNI("DEL", netConfig, containerNS, containerID, sandboxRepoDir)
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

			sandboxNS, session = execCNI("DEL", netConfig, namespace.NewNamespace("bad-path"), containerID, sandboxRepoDir)
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))
			Expect(session.Out.Contents()).To(ContainSubstring("failed to delete link in container namespace"))
		})
	})

	Context("when the sandbox repository cannot be acquired", func() {
		It("returns an error", func() {
			sandboxNS, session = execCNI("DEL", netConfig, containerNS, containerID, "")
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))
			Expect(session.Out.Contents()).To(ContainSubstring("failed to open sandbox repository"))
		})
	})

	Context("when the sandbox namespace no longer exists", func() {
		It("returns an error", func() {
			sandboxNS, session = execCNI("DEL", netConfig, containerNS, containerID, sandboxRepoDir)
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))
			Expect(session.Out.Contents()).To(ContainSubstring("failed to get sandbox namespace"))
		})
	})
})
