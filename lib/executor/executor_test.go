package executor_test

import (
	"net"

	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/executor"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/executor/fakes"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"

	"github.com/appc/cni/pkg/types"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

type TestLink struct {
	Attributes netlink.LinkAttrs
}

func (t TestLink) Attrs() *netlink.LinkAttrs {
	return &t.Attributes
}

func (t TestLink) Type() string {
	return "NOT IMPLEMENTED"
}

var _ = Describe("SetupContainerNS", func() {
	var (
		ex                executor.Executor
		networkNamespacer *fakes.Namespacer
		linkFactory       *fakes.LinkFactory
		netlinker         *fakes.Netlinker
		addressManager    *fakes.AddressManager
	)

	BeforeEach(func() {
		networkNamespacer = &fakes.Namespacer{}
		linkFactory = &fakes.LinkFactory{}
		netlinker = &fakes.Netlinker{}
		addressManager = &fakes.AddressManager{}

		ex = executor.Executor{
			NetworkNamespacer: networkNamespacer,
			LinkFactory:       linkFactory,
			Netlinker:         netlinker,
			AddressManager:    addressManager,
		}
	})

	Context("when setup succeeds", func() {
		BeforeEach(func() {
		})

		It("should construct the network inside the container namespace", func() {
			networkNamespacer.GetFromPathStub = func(ns string) (netns.NsHandle, error) {
				if ns == "/var/some/sandbox/namespace" {
					return netns.NsHandle(6), nil
				} else {
					return netns.NsHandle(5), nil
				}

				return netns.NsHandle(-1), nil
			}

			returnedSandboxLink := TestLink{Attributes: netlink.LinkAttrs{Name: "sandbox-link"}}
			returnedContainerLink := TestLink{Attributes: netlink.LinkAttrs{
				Index: 1555,
				Name:  "container-link",
			},
			}
			linkFactory.CreateVethPairReturns(returnedSandboxLink, returnedContainerLink, nil)

			result := &types.Result{
				IP4: &types.IPConfig{
					IP: net.IPNet{
						IP:   net.ParseIP("192.168.100.1"),
						Mask: net.ParseIP("192.168.100.1").DefaultMask(),
					},
					Gateway: net.ParseIP("0.0.0.0"),
					Routes: []types.Route{
						{
							Dst: net.IPNet{
								IP:   net.ParseIP("192.168.100.1"),
								Mask: net.ParseIP("192.168.100.1").DefaultMask(),
							},
							GW: net.ParseIP("192.168.100.1"),
						},
					},
				},
			}

			sandboxLink, err := ex.SetupContainerNS("/var/some/sandbox/namespace", "/var/some/container/namespace", "some-container-id", "some-eth0", result)
			Expect(err).NotTo(HaveOccurred())
			Expect(sandboxLink.Attrs().Name).To(Equal("sandbox-link"))

			By("asking for the container namespace handle")
			Expect(networkNamespacer.GetFromPathCallCount()).To(Equal(2))
			Expect(networkNamespacer.GetFromPathArgsForCall(0)).To(Equal("/var/some/container/namespace"))

			By("switch to the container namespace via the handle")
			Expect(networkNamespacer.SetCallCount()).To(Equal(1))
			Expect(networkNamespacer.SetArgsForCall(0)).To(Equal(netns.NsHandle(5)))

			By("creating a veth pair when the container namespace")
			Expect(linkFactory.CreateVethPairCallCount()).To(Equal(1))
			containerID, interfaceName, vxlanVethMTU := linkFactory.CreateVethPairArgsForCall(0)
			Expect(containerID).To(Equal("some-container-id"))
			Expect(interfaceName).To(Equal("some-eth0"))
			Expect(vxlanVethMTU).To(Equal(1450))

			By("getting the sandbox namespace")
			Expect(networkNamespacer.GetFromPathArgsForCall(1)).To(Equal("/var/some/sandbox/namespace"))

			By("moving the sandboxlink into the sandbox namespace")
			Expect(netlinker.LinkSetNsFdCallCount()).To(Equal(1))
			sandboxLink, fd := netlinker.LinkSetNsFdArgsForCall(0)
			Expect(sandboxLink).To(Equal(returnedSandboxLink))
			Expect(fd).To(Equal(int(netns.NsHandle(6))))

			By("adding an address to the container link")
			Expect(addressManager.AddAddressCallCount()).To(Equal(1))
			cLink, returnedResult := addressManager.AddAddressArgsForCall(0)
			Expect(cLink).To(Equal(returnedContainerLink))
			Expect(returnedResult).To(Equal(&result.IP4.IP))

			By("setting the container link to UP")
			Expect(netlinker.LinkSetUpCallCount()).To(Equal(1))
			Expect(netlinker.LinkSetUpArgsForCall(0)).To(Equal(returnedContainerLink))

			By("adding a route")
			Expect(netlinker.RouteAddCallCount()).To(Equal(1))
			route := netlinker.RouteAddArgsForCall(0)
			Expect(route.LinkIndex).To(Equal(1555))
			Expect(route.Scope).To(Equal(netlink.SCOPE_UNIVERSE))
			Expect(route.Dst).To(Equal(&result.IP4.Routes[0].Dst))
			Expect(route.Gw).To(Equal(result.IP4.Gateway))
		})
	})
})
