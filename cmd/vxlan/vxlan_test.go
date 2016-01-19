package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"

	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/namespace"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/appc/cni/pkg/types"
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
		netConfig Config
		session   *gexec.Session

		repoDir       string
		hostNS        namespace.Namespace
		containerNS   namespace.Namespace
		namespaceRepo namespace.Repository
	)

	BeforeEach(func() {
		var err error
		repoDir, err = ioutil.TempDir("", "vxlan")
		Expect(err).NotTo(HaveOccurred())

		namespaceRepo, err = namespace.NewRepository(repoDir)
		Expect(err).NotTo(HaveOccurred())

		containerNS, err = namespaceRepo.Create("container-ns")
		Expect(err).NotTo(HaveOccurred())

		hostNS, err = namespaceRepo.Create("host-ns")
		Expect(err).NotTo(HaveOccurred())

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

	JustBeforeEach(func() {
		input, err := json.Marshal(netConfig)
		Expect(err).NotTo(HaveOccurred())

		cmd := exec.Command(pathToVxlan)
		cmd.Stdin = bytes.NewReader(input)
		cmd.Env = append(
			os.Environ(),
			fmt.Sprintf("CNI_COMMAND=ADD"),
			fmt.Sprintf("CNI_PATH=%s", cniPath),
			fmt.Sprintf("CNI_NETNS=%s", containerNS.Path()),
			fmt.Sprintf("CNI_IFNAME=%s", "vx-eth0"),
			// fmt.Sprintf("DUCATI_OS_SANDBOX_REPO=%s", sandboxNamespaceRepo),
		)

		err = hostNS.Execute(func(_ *os.File) error {
			var err error
			session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			return err
		})
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		containerNS.Destroy()
		hostNS.Destroy()
		os.RemoveAll(repoDir)
	})

	It("creates a vxlan adapter in the host namespace", func() {
		Eventually(session).Should(gexec.Exit(0))

		hostNS.Execute(func(_ *os.File) error {
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

	It("creates a vxlan bridge in the host namespace", func() {
		Eventually(session).Should(gexec.Exit(0))

		var result types.Result
		err := json.Unmarshal(session.Out.Contents(), &result)
		Expect(err).NotTo(HaveOccurred())

		err = hostNS.Execute(func(_ *os.File) error {
			link, err := netlink.LinkByName("vxlanbr1")
			Expect(err).NotTo(HaveOccurred())

			bridge, ok := link.(*netlink.Bridge)
			Expect(ok).To(BeTrue())
			Expect(bridge.LinkAttrs.MTU).To(Equal(1450))
			Expect(bridge.LinkAttrs.Flags & net.FlagUp).To(Equal(net.FlagUp))

			addrs, err := netlink.AddrList(link, netlink.FAMILY_V4)
			Expect(err).NotTo(HaveOccurred())
			Expect(addrs).To(HaveLen(1))
			Expect(addrs[0].IPNet.IP.String()).To(Equal(result.IP4.Gateway.String()))

			return nil
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("returns IPAM data", func() {
		Eventually(session).Should(gexec.Exit(0))

		var result types.Result
		err := json.Unmarshal(session.Out.Contents(), &result)
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
})
