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
	"github.com/cloudfoundry-incubator/ducati-daemon/models"
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
	Name      string `json:"name"`
	Type      string `json:"type"`
	NetworkID string `json:"network_id"`
}

const vni = 1
const DEFAULT_TIMEOUT = "3s"

var _ = Describe("VXLAN ADD", func() {
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
		reqBytes      []byte

		ipamResult types.Result

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

		ipamResult = types.Result{
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
		}

		ipamStatusCode := http.StatusCreated
		ipamPostHandler := ghttp.CombineHandlers(
			ghttp.VerifyRequest("POST", "/ipam/some-network-id"),
			ghttp.VerifyHeaderKV("Content-Type", "application/json"),
			ghttp.RespondWithJSONEncodedPtr(&ipamStatusCode, &ipamResult),
		)

		server.RouteToHandler("POST", "/ipam/some-network-id", ipamPostHandler)
		server.RouteToHandler("POST", "/containers", func(resp http.ResponseWriter, req *http.Request) {
			var err error
			reqBytes, err = ioutil.ReadAll(req.Body)
			Expect(err).NotTo(HaveOccurred())
			resp.WriteHeader(http.StatusCreated)
		})
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

			Expect(server.ReceivedRequests()).To(HaveLen(2))

			var output *models.Container
			err = json.Unmarshal(reqBytes, &output)
			Expect(err).NotTo(HaveOccurred())

			hostIP, _, err := net.ParseCIDR(output.HostIP)
			Expect(err).NotTo(HaveOccurred())

			Expect(output.ID).To(Equal(containerID))
			Expect(output.MAC).To(MatchRegexp("[[:xdigit:]]{2}:[[:xdigit:]]{2}:[[:xdigit:]]{2}:[[:xdigit:]]{2}:[[:xdigit:]]{2}:[[:xdigit:]]{2}"))
			Expect(hostIP).NotTo(BeNil())
		})

		Context("when there are routes", func() {
			BeforeEach(func() {
				_, destination, err := net.ParseCIDR("10.10.10.0/24")
				Expect(err).NotTo(HaveOccurred())

				ipamResult.IP4.Routes = append(ipamResult.IP4.Routes, types.Route{Dst: *destination})
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

					_, vxlanNet, err := net.ParseCIDR("192.168.0.0/16")
					Expect(sanitizedRoutes).To(ContainElement(netlink.Route{
						Dst: vxlanNet,
						Gw:  result.IP4.Gateway.To4(),
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
			Expect(result.IP4.Routes[0].Dst.String()).To(Equal("192.168.0.0/16"))

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

		Context("when the call to allocate an IP fails", func() {
			BeforeEach(func() {
				server.RouteToHandler("POST", "/ipam/some-network-id", ghttp.RespondWith(http.StatusInternalServerError, nil))
			})

			It("returns an error", func() {
				var err error
				var cmd *exec.Cmd
				sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
				Expect(err).NotTo(HaveOccurred())
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))

				Expect(server.ReceivedRequests()).To(HaveLen(1))
				Expect(session.Out.Contents()).To(ContainSubstring("unexpected status code on AllocateIP"))
			})
		})

		Context("when the call to the daemon to register the container fails", func() {
			BeforeEach(func() {
				server.RouteToHandler("POST", "/containers", ghttp.RespondWith(http.StatusInternalServerError, nil))
			})

			It("returns an error", func() {
				var err error
				var cmd *exec.Cmd
				sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
				Expect(err).NotTo(HaveOccurred())
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))

				Expect(server.ReceivedRequests()).To(HaveLen(2))
				Expect(session.Out.Contents()).To(ContainSubstring("saving container data to store"))
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
				ipamResult.IP4.Gateway = net.ParseIP("192.168.1.2")
			})

			PIt("returns with an error", func() {
				var err error
				var cmd *exec.Cmd
				sandboxNS, cmd, err = buildCNICmd("ADD", netConfig, containerNS, containerID, sandboxRepoDir, serverURL)
				Expect(err).NotTo(HaveOccurred())
				session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
				Expect(err).NotTo(HaveOccurred())
				Eventually(session, DEFAULT_TIMEOUT).Should(gexec.Exit(1))

				Expect(session.Out.Contents()).To(ContainSubstring("configuring sandbox namespace"))
			})
		})
	})
})
