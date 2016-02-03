package acceptance_test

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/namespace"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/appc/cni/pkg/types"
	"github.com/onsi/gomega/gexec"
	"github.com/onsi/gomega/ghttp"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
)

type IPAM struct {
	Type   string              `json:"type,omitempty"`
	Subnet string              `json:"subnet,omitempty"`
	Routes []map[string]string `json:"routes,omitempty"`
}

type Config struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Network     string `json:"network"`
	HostNetwork string `json:"host_network"`
	IPAM        IPAM   `json:"ipam,omitempty"`
}

const vni = 1
const DEFAULT_TIMEOUT = "3s"

var _ = Describe("vxlan", func() {
	var (
		netConfig Config
		session   *gexec.Session
		server    *ghttp.Server

		repoDir       string
		containerID   string
		serverURL     string
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

		server = ghttp.NewServer()
		serverURL = server.URL()
		server.AppendHandlers(ghttp.CombineHandlers(
			ghttp.VerifyRequest("POST", "/containers"),
			ghttp.VerifyJSON(fmt.Sprintf(`{"id":%q}`, containerID)),
			ghttp.RespondWith(http.StatusCreated, nil),
		))
	})

	AfterEach(func() {
		containerNS.Destroy()
		sandboxNS.Destroy()
		os.RemoveAll(repoDir)
		server.Close()
	})

	Describe("ADD", func() {
		It("moves a vxlan adapter into the sandbox", func() {
			var err error
			var cmd *exec.Cmd
			sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
			Expect(err).NotTo(HaveOccurred())
			session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

			sandboxNS.Execute(func(_ *os.File) error {
				link, err := netlink.LinkByName("vxlan1")
				Expect(err).NotTo(HaveOccurred())
				vxlan, ok := link.(*netlink.Vxlan)
				Expect(ok).To(BeTrue())

				Expect(vxlan.VxlanId).To(Equal(1))
				Expect(vxlan.Learning).To(BeTrue())
				Expect(vxlan.Port).To(BeEquivalentTo(nl.Swap16(4789)))
				Expect(vxlan.Proxy).To(BeTrue())
				Expect(vxlan.L2miss).To(BeTrue())
				Expect(vxlan.L3miss).To(BeTrue())
				Expect(vxlan.LinkAttrs.Flags & net.FlagUp).To(Equal(net.FlagUp))

				return nil
			})
		})

		It("creates a vxlan bridge in the sandbox", func() {
			var err error
			var cmd *exec.Cmd
			sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
			Expect(err).NotTo(HaveOccurred())

			session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

			var result types.Result
			err = json.Unmarshal(session.Out.Contents(), &result)
			Expect(err).NotTo(HaveOccurred())

			var bridge *netlink.Bridge
			var addrs []netlink.Addr

			err = sandboxNS.Execute(func(_ *os.File) error {
				link, err := netlink.LinkByName("vxlanbr1")
				if err != nil {
					return fmt.Errorf("finding link by name: %s", err)
				}

				var ok bool
				bridge, ok = link.(*netlink.Bridge)
				if !ok {
					return fmt.Errorf("unable to cast link to bridge")
				}

				addrs, err = netlink.AddrList(link, netlink.FAMILY_V4)
				if err != nil {
					return fmt.Errorf("unable to list addrs: %s", err)
				}

				return nil
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(bridge.LinkAttrs.MTU).To(Equal(1450))
			Expect(bridge.LinkAttrs.Flags & net.FlagUp).To(Equal(net.FlagUp))

			Expect(addrs).To(HaveLen(1))
			Expect(addrs[0].IPNet.IP.String()).To(Equal(result.IP4.Gateway.String()))
		})

		It("creates a veth pair in the container and sandbox namespaces", func() {
			var err error
			var cmd *exec.Cmd
			sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
			Expect(err).NotTo(HaveOccurred())
			session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

			err = containerNS.Execute(func(_ *os.File) error {
				link, err := netlink.LinkByName("vx-eth0")
				Expect(err).NotTo(HaveOccurred())

				bridge, ok := link.(*netlink.Veth)
				Expect(ok).To(BeTrue())
				Expect(bridge.LinkAttrs.MTU).To(Equal(1450))
				Expect(bridge.LinkAttrs.Flags & net.FlagUp).To(Equal(net.FlagUp))

				return nil
			})
			Expect(err).NotTo(HaveOccurred())

			err = sandboxNS.Execute(func(_ *os.File) error {
				link, err := netlink.LinkByName("guid-1")
				Expect(err).NotTo(HaveOccurred())

				bridge, ok := link.(*netlink.Veth)
				Expect(ok).To(BeTrue())
				Expect(bridge.LinkAttrs.MTU).To(Equal(1450))
				Expect(bridge.LinkAttrs.Flags & net.FlagUp).To(Equal(net.FlagUp))

				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("saves the container info in the ducati daemon", func() {
			var err error
			var cmd *exec.Cmd
			sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
			Expect(err).NotTo(HaveOccurred())
			session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

			Expect(server.ReceivedRequests()).Should(HaveLen(1))
		})

		Context("when there are routes", func() {
			BeforeEach(func() {
				netConfig.IPAM.Routes = append(netConfig.IPAM.Routes, map[string]string{"dst": "10.10.10.0/24"})
			})

			It("should contain the routes", func() {
				var err error
				var cmd *exec.Cmd
				sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
				Expect(err).NotTo(HaveOccurred())
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session).Should(gexec.Exit(0))

				var result types.Result
				err = json.Unmarshal(session.Out.Contents(), &result)
				Expect(err).NotTo(HaveOccurred())

				err = containerNS.Execute(func(_ *os.File) error {
					l, err := netlink.LinkByName("vx-eth0")
					Expect(err).NotTo(HaveOccurred())

					routes, err := netlink.RouteList(l, netlink.FAMILY_V4)
					Expect(err).NotTo(HaveOccurred())
					Expect(routes).To(HaveLen(3))

					var sanitizedRoutes []netlink.Route
					for _, route := range routes {
						sanitizedRoutes = append(sanitizedRoutes, netlink.Route{
							Gw:  route.Gw,
							Dst: route.Dst,
							Src: route.Src,
						})
					}

					Expect(sanitizedRoutes).To(ContainElement(netlink.Route{
						Gw: result.IP4.Gateway.To4(),
					}))

					_, linkLocal, err := net.ParseCIDR("192.168.1.0/24")
					Expect(err).NotTo(HaveOccurred())
					Expect(sanitizedRoutes).To(ContainElement(netlink.Route{
						Dst: linkLocal,
						Src: result.IP4.IP.IP.To4(),
					}))

					_, dest, err := net.ParseCIDR("10.10.10.0/24")
					Expect(sanitizedRoutes).To(ContainElement(netlink.Route{
						Dst: dest,
						Gw:  result.IP4.Gateway.To4(),
					}))

					return nil
				})
			})
		})

		It("returns IPAM data", func() {
			var err error
			var cmd *exec.Cmd
			sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
			Expect(err).NotTo(HaveOccurred())
			session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

			var result types.Result
			err = json.Unmarshal(session.Out.Contents(), &result)
			Expect(err).NotTo(HaveOccurred())

			Expect(result.IP4.Gateway.String()).To(Equal("192.168.1.1"))
			Expect(result.IP4.Routes).To(HaveLen(1))
			Expect(result.IP4.Routes[0].Dst.String()).To(Equal("0.0.0.0/0"))

			err = containerNS.Execute(func(_ *os.File) error {
				l, err := netlink.LinkByName("vx-eth0")
				Expect(err).NotTo(HaveOccurred())

				addrs, err := netlink.AddrList(l, netlink.FAMILY_V4)
				Expect(err).NotTo(HaveOccurred())
				Expect(addrs).To(HaveLen(1))
				Expect(addrs[0].IPNet.IP.String()).To(Equal(result.IP4.IP.IP.String()))

				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("uses the IPAM plugin to allocate an IP", func() {
			var err error
			var cmd *exec.Cmd
			sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
			Expect(err).NotTo(HaveOccurred())
			session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(0))

			var result types.Result
			err = json.Unmarshal(session.Out.Contents(), &result)
			Expect(err).NotTo(HaveOccurred())

			addressPath := filepath.Join("/var/lib/cni/networks", "test-network", result.IP4.IP.IP.String())
			addressFile, err := os.Open(addressPath)
			Expect(err).NotTo(HaveOccurred())
			addressFile.Close()
		})

		Context("when the call to the daemon fails", func() {
			It("returns an error", func() {
				server.SetHandler(0, ghttp.RespondWith(http.StatusInternalServerError, nil))

				var err error
				var cmd *exec.Cmd
				sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
				Expect(err).NotTo(HaveOccurred())
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))
				Expect(session.Out.Contents()).To(ContainSubstring("saving container data to store: expected to receive 201"))
			})
		})

		Context("when CNI_CONTAINERID is not set", func() {
			BeforeEach(func() {
				containerID = ""
			})

			It("exits with an error", func() {
				var err error
				var cmd *exec.Cmd
				sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
				Expect(err).NotTo(HaveOccurred())
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))
				Expect(session.Out.Contents()).To(ContainSubstring("CNI_CONTAINERID is required"))
			})
		})

		Context("when DUCATI_OS_SANDBOX_REPO is not set", func() {
			BeforeEach(func() {
				sandboxRepoDir = ""
			})

			It("exits with an error", func() {
				var err error
				var cmd *exec.Cmd
				sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
				Expect(err).NotTo(HaveOccurred())
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))
				Expect(session.Out.Contents()).To(ContainSubstring("DUCATI_OS_SANDBOX_REPO is required"))
			})
		})

		Context("when creating the sandbox repo fails", func() {
			BeforeEach(func() {
				f, err := ioutil.TempFile("", "sandbox-repo")
				Expect(err).NotTo(HaveOccurred())

				sandboxRepoDir = f.Name()
				f.Close()
			})

			It("exits with an error", func() {
				var err error
				var cmd *exec.Cmd
				sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
				Expect(err).NotTo(HaveOccurred())
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))
				Expect(session.Out.Contents()).To(ContainSubstring("failed to create sandbox repository"))
			})
		})

		Context("when the IPAM plugin returns an error", func() {
			BeforeEach(func() {
				netConfig.IPAM = IPAM{}
				netConfig.IPAM.Type = "not-a-plugin"
			})

			It("exits with an error", func() {
				var err error
				var cmd *exec.Cmd
				sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
				Expect(err).NotTo(HaveOccurred())
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))
				Expect(session.Out.Contents()).To(MatchRegexp("could not find.*plugin"))
			})
		})

		Context("when the container namespace cannot be opened", func() {
			BeforeEach(func() {
				tempDir, err := ioutil.TempDir("", "non-existent-namespace")
				Expect(err).NotTo(HaveOccurred())
				containerNS = namespace.NewNamespace(filepath.Join(tempDir, "non-existent-ns"))
			})

			It("exits with an error", func() {
				var err error
				var cmd *exec.Cmd
				sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
				Expect(err).NotTo(HaveOccurred())
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))
				Expect(session.Out.Contents()).To(ContainSubstring("non-existent-ns"))
			})
		})

		Context("when the pair cannot be created", func() {
			BeforeEach(func() {
				Expect(exec.Command("ip", "netns", "add", "some-namespace").Run()).To(Succeed())
				Expect(exec.Command("ip", "netns", "exec", "some-namespace", "ip", "link", "add", containerID, "type", "dummy").Run()).To(Succeed())

				containerNS = namespace.NewNamespace("/var/run/netns/some-namespace")
			})

			AfterEach(func() {
				Expect(exec.Command("ip", "netns", "del", "some-namespace").Run()).To(Succeed())
			})

			It("returns with an error", func() {
				var err error
				var cmd *exec.Cmd
				sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
				Expect(err).NotTo(HaveOccurred())
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))
				Expect(session.Out.Contents()).To(ContainSubstring("could not create veth pair"))
			})
		})

		Context("When the Bridge cannot be created inside of the sanbox", func() {
			BeforeEach(func() {
				netConfig.IPAM.Type = "fake_plugins"
			})

			It("returns with an error", func() {
				var err error
				var cmd *exec.Cmd
				sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
				Expect(err).NotTo(HaveOccurred())
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))
				Expect(session.Out.Contents()).To(ContainSubstring("failed to create bridge"))
			})
		})
	})
})