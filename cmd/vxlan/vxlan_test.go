package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/appc/cni/pkg/ns"
	"github.com/appc/cni/pkg/types"
	"github.com/onsi/ginkgo/config"
	"github.com/onsi/gomega/gexec"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
)

type IPAM struct {
	Type   string              `json:"type"`
	Subnet string              `json:"subnet"`
	Routes []map[string]string `json:"routes"`
}

type Config struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Network     string `json:"network"`
	HostNetwork string `json:"host_network"`
	IPAM        IPAM   `json:"ipam"`
}

var _ = Describe("vxlan", func() {
	var (
		netConfig            Config
		session              *gexec.Session
		namespacePath        string
		sandboxNamespacePath string
		sandboxNamespaceRepo string
	)

	BeforeEach(func() {
		namespacePath = newNetworkNamespace(fmt.Sprintf("test-ns-%d", config.GinkgoConfig.ParallelNode))

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

		const vni = 1
		sandboxNamespaceRepo = "/var/ducati/os-sandboxes/"
		sandboxNamespacePath = filepath.Join(sandboxNamespaceRepo, fmt.Sprintf("vni-%d", vni))
	})

	JustBeforeEach(func() {
		input, err := json.Marshal(netConfig)
		Expect(err).NotTo(HaveOccurred())

		cmd := exec.Command(pathToVxlan)
		cmd.Stdin = bytes.NewReader(input)
		cmd.Env = append(
			os.Environ(),
			fmt.Sprintf("CNI_COMMAND=ADD"),
			fmt.Sprintf("CNI_PATH=%s", cniPath),
			fmt.Sprintf("CNI_NETNS=%s", namespacePath),
			fmt.Sprintf("CNI_IFNAME=%s", "vx-eth0"),
			fmt.Sprintf("DUCATI_OS_SANDBOX_REPO=%s", sandboxNamespaceRepo),
		)

		session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		removeNetworkNamespace(filepath.Base(namespacePath))
		for _, name := range []string{"container-name", "host-name", "vxlan1", "vxlanbr1"} {
			exec.Command("ip", "link", "del", name).Run()
		}
	})

	It("creates a vxlan adapter in the sandbox namespace", func() {
		Eventually(session).Should(gexec.Exit(0))

		var link netlink.Link
		err := ns.WithNetNSPath(sandboxNamespacePath, false, func(_ *os.File) error {
			var err error
			link, err = netlink.LinkByName("vxlan1")
			return err
		})
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
	})

	It("creates a vxlan bridge in the sandbox namespace", func() {
		Eventually(session).Should(gexec.Exit(0))

		var result types.Result
		Expect(json.Unmarshal(session.Out.Contents(), &result)).To(Succeed())

		var gatewayAddr netlink.Addr
		var link netlink.Link
		err := ns.WithNetNSPath(sandboxNamespacePath, false, func(_ *os.File) error {
			var err error
			link, err = netlink.LinkByName("vxlanbr1")
			if err != nil {
				return err
			}
			addrs, err := netlink.AddrList(link, netlink.FAMILY_V4)
			if err != nil {
				return err
			}
			Expect(addrs).To(HaveLen(1))
			gatewayAddr = addrs[0]
			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		bridge, ok := link.(*netlink.Bridge)
		Expect(ok).To(BeTrue())

		Expect(bridge.LinkAttrs.MTU).To(Equal(1450))
		Expect(bridge.LinkAttrs.Flags & net.FlagUp).To(Equal(net.FlagUp))
		Expect(result.IP4.Gateway.String()).To(Equal(gatewayAddr.IPNet.IP.String()))
	})

	It("returns IPAM data", func() {
		Eventually(session).Should(gexec.Exit(0))

		var result types.Result
		Expect(json.Unmarshal(session.Out.Contents(), &result)).To(Succeed())

		var containerAddr netlink.Addr
		err := ns.WithNetNSPath(namespacePath, false, func(_ *os.File) error {
			l, err := netlink.LinkByName("vx-eth0")
			if err != nil {
				return err
			}
			addrs, err := netlink.AddrList(l, netlink.FAMILY_V4)
			if err != nil {
				return err
			}
			Expect(addrs).To(HaveLen(1))
			containerAddr = addrs[0]
			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(result.IP4.Gateway.String()).To(Equal("192.168.1.1"))
		Expect(result.IP4.Routes).To(HaveLen(1))
		Expect(result.IP4.Routes[0].Dst.String()).To(Equal("0.0.0.0/0"))
		Expect(result.IP4.IP.String()).To(Equal(containerAddr.IPNet.String()))
	})
})
