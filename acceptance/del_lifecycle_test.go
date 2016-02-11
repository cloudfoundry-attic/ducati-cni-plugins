package acceptance_test

import (
	"encoding/json"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"github.com/appc/cni/pkg/types"
	"github.com/cloudfoundry-incubator/ducati-daemon/lib/namespace"
	"github.com/onsi/gomega/gexec"
	"github.com/onsi/gomega/ghttp"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/vishvananda/netlink"
)

var _ = Describe("VXLAN DEL", func() {
	var (
		netConfig Config
		session   *gexec.Session
		server    *ghttp.Server
		serverURL string

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
			Name:      "test-network",
			Type:      "vxlan",
			NetworkID: "some-network-id",
		}

		server = ghttp.NewServer()
		serverURL = server.URL()

		server.RouteToHandler("POST", "/ipam/some-network-id/guid-1", ghttp.CombineHandlers(
			ghttp.RespondWithJSONEncoded(
				http.StatusCreated,
				types.Result{
					IP4: &types.IPConfig{
						IP: net.IPNet{
							IP:   net.ParseIP("192.168.1.2"),
							Mask: net.CIDRMask(24, 32),
						},
						Gateway: net.ParseIP("192.168.1.1"),
						Routes: []types.Route{{
							Dst: net.IPNet{
								IP:   net.ParseIP("192.168.0.0"),
								Mask: net.CIDRMask(16, 32),
							},
							GW: net.ParseIP("192.168.1.1"),
						}},
					},
				},
			),
		))

		server.RouteToHandler("POST", "/ipam/some-network-id/guid-2", ghttp.CombineHandlers(
			ghttp.RespondWithJSONEncoded(
				http.StatusCreated,
				types.Result{
					IP4: &types.IPConfig{
						IP: net.IPNet{
							IP:   net.ParseIP("192.168.1.3"),
							Mask: net.CIDRMask(24, 32),
						},
						Gateway: net.ParseIP("192.168.1.1"),
						Routes: []types.Route{{
							Dst: net.IPNet{
								IP:   net.ParseIP("192.168.0.0"),
								Mask: net.CIDRMask(16, 32),
							},
							GW: net.ParseIP("192.168.1.1"),
						}},
					},
				},
			),
		))

		server.RouteToHandler("POST", "/containers", func(resp http.ResponseWriter, req *http.Request) {
			resp.WriteHeader(http.StatusCreated)
		})

		server.RouteToHandler("DELETE", regexp.MustCompile("/containers/.*"), func(resp http.ResponseWriter, req *http.Request) {
			resp.WriteHeader(http.StatusNoContent)
		})
	})

	AfterEach(func() {
		containerNS.Destroy()
		sandboxNS.Destroy()
		os.RemoveAll(repoDir)
		server.Close()
	})

	Context("when delete goes smoothly", func() {
		var containerAddress string

		BeforeEach(func() {
			var err error
			var cmd *exec.Cmd
			sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
			session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

			var result types.Result
			err = json.Unmarshal(session.Out.Contents(), &result)
			Expect(err).NotTo(HaveOccurred())

			containerAddress = result.IP4.IP.IP.String()
		})

		PIt("should release the IPAM managed address", func() {
			var err error
			var cmd *exec.Cmd
			sandboxNS, cmd, err = buildCNICmd("DEL", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
			session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

			addressPath := filepath.Join("/var/lib/cni/networks", "test-network", containerAddress)
			_, err = os.Open(addressPath)
			Expect(err).To(HaveOccurred())
			Expect(os.IsNotExist(err)).To(BeTrue())
		})

		It("removes the container info from the ducati daemon", func() {
			var err error
			var cmd *exec.Cmd
			sandboxNS, cmd, err = buildCNICmd("DEL", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
			Expect(err).NotTo(HaveOccurred())
			session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

			Expect(server.ReceivedRequests()).Should(HaveLen(3))
		})

		Context("when the last container leaves the network", func() {
			It("should remove the sandboxNS", func() {
				var err error
				var cmd *exec.Cmd
				sandboxNS, cmd, err = buildCNICmd("DEL", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

				_, err = sandboxNS.Open()
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

				var cmd *exec.Cmd
				sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS2, "guid-2", sandboxRepoDir, serverURL)
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))
			})

			AfterEach(func() {
				var err error
				var cmd *exec.Cmd
				sandboxNS, cmd, err = buildCNICmd("DEL", netConfig, containerNS2, "guid-2", sandboxRepoDir, serverURL)
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))
			})

			It("should remove the veth pair from the container and sandbox namespaces", func() {
				var err error
				var cmd *exec.Cmd
				sandboxNS, cmd, err = buildCNICmd("DEL", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

				err = containerNS.Execute(func(_ *os.File) error {
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
				var err error
				var cmd *exec.Cmd
				sandboxNS, cmd, err = buildCNICmd("DEL", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

				err = containerNS2.Execute(func(_ *os.File) error {
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
			var err error
			var cmd *exec.Cmd
			sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
			session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

			var result types.Result
			err = json.Unmarshal(session.Out.Contents(), &result)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should return an error", func() {
			var err error
			var cmd *exec.Cmd
			sandboxNS, cmd, err = buildCNICmd("DEL", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
			session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

			sandboxNS, cmd, err = buildCNICmd("DEL", netConfig, namespace.NewNamespace("bad-path"), containerID, sandboxRepoDir, serverURL)
			session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))
			Expect(session.Out.Contents()).To(ContainSubstring("failed to delete link in container namespace"))
		})
	})

	Context("when the sandbox repository cannot be acquired", func() {
		BeforeEach(func() {
			sandboxRepoDir = ""
		})

		It("returns an error", func() {
			var err error
			var cmd *exec.Cmd
			sandboxNS, cmd, err = buildCNICmd("DEL", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
			session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))
			Expect(session.Out.Contents()).To(ContainSubstring("failed to open sandbox repository"))
		})
	})

	Context("when the sandbox namespace no longer exists", func() {
		It("returns an error", func() {
			var err error
			var cmd *exec.Cmd
			sandboxNS, cmd, err = buildCNICmd("DEL", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
			session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))
			Expect(session.Out.Contents()).To(ContainSubstring("failed to get sandbox namespace"))
		})
	})
})
