package overlay_test

import (
	"net"

	"github.com/appc/cni/pkg/types"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/overlay"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/overlay/fakes"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Overlay", func() {
	var (
		networkSandboxRepo *fakes.NetworkSandboxRepo
		namespaceRepo      *fakes.NamespaceRepo
	)

	Describe("Add", func() {
		It("should succeed", func() {
			networkSandboxRepo = &fakes.NetworkSandboxRepo{}
			namespaceRepo = &fakes.NamespaceRepo{}

			_, ip, err := net.ParseCIDR("192.168.1.3/24")
			Expect(err).NotTo(HaveOccurred())
			_, routeDst, err := net.ParseCIDR("0.0.0.0/0")
			Expect(err).NotTo(HaveOccurred())
			ipamResult := &types.Result{
				IP4: &types.IPConfig{
					IP:      *ip,
					Gateway: net.ParseIP("192.168.1.1"),
					Routes: []types.Route{
						types.Route{
							Dst: *routeDst,
						},
					},
				},
			}

			controller := overlay.Controller{
				NetworkSandboxRepo: networkSandboxRepo,
				NamespaceRepo:      namespaceRepo,
			}

			fakeNetworkSandbox := &fakes.NetworkSandbox{}
			networkSandboxRepo.CreateReturns(fakeNetworkSandbox, nil)

			err = controller.Add("", "", 0, ipamResult)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
