package executor_test

import (
	"net"

	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/executor"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/executor/fakes"

	"github.com/appc/cni/pkg/types"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("SetupContainerNS", func() {
	var (
		ex             executor.Executor
		containerNS    *fakes.Namespacer
		sandboxNS      *fakes.Namespacer
		linkFactory    *fakes.LinkFactory
		netlinker      *fakes.Netlinker
		addressManager *fakes.AddressManager
	)

	BeforeEach(func() {
		containerNS = &fakes.Namespacer{}
		sandboxNS = &fakes.Namespacer{}
		linkFactory = &fakes.LinkFactory{}
		netlinker = &fakes.Netlinker{}
		addressManager = &fakes.AddressManager{}

		ex = executor.Executor{
			ContainerNS:    containerNS,
			SandboxNS:      sandboxNS,
			LinkFactory:    linkFactory,
			Netlinker:      netlinker,
			AddressManager: addressManager,
		}
	})

	It("should construct the network inside the container namespace", func() {
		ipamResults := &types.Result{
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

		sandboxLink, err := ex.SetupContainerNS("some-container-id", "some-eth0", ipamResults)
		Expect(err).NotTo(HaveOccurred())

		Expect(sandboxLink.Attrs().Name).To(Equal("some-sandbox-link"))
	})
})
