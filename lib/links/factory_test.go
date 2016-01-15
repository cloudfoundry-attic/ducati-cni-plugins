package links_test

import (
	"errors"
	"net"

	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/links"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/nl/fakes"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Factory", func() {
	var (
		factory   *links.Factory
		netlinker *fakes.Netlinker
	)

	BeforeEach(func() {
		netlinker = &fakes.Netlinker{}
		factory = &links.Factory{
			Netlinker: netlinker,
		}
	})

	Describe("CreateBridge", func() {
		var (
			expectedBridge *netlink.Bridge
			address        net.IP
		)

		BeforeEach(func() {
			expectedBridge = &netlink.Bridge{
				LinkAttrs: netlink.LinkAttrs{
					Name: "some-bridge-name",
					MTU:  links.BridgeMTU,
				},
			}

			address = net.ParseIP("192.168.1.1")
		})

		It("should return a bridge with the expected config", func() {
			bridge, err := factory.CreateBridge("some-bridge-name", address)
			Expect(err).NotTo(HaveOccurred())
			Expect(bridge).To(Equal(expectedBridge))
		})

		It("adds the bridge", func() {
			_, err := factory.CreateBridge("some-bridge-name", address)
			Expect(err).NotTo(HaveOccurred())

			Expect(netlinker.LinkAddCallCount()).To(Equal(1))
			Expect(netlinker.LinkAddArgsForCall(0)).To(Equal(expectedBridge))
		})

		Context("when adding the bridge link fails", func() {
			It("returns the error", func() {
				netlinker.LinkAddReturns(errors.New("link add failed"))

				_, err := factory.CreateBridge("some-bridge-name", address)
				Expect(err).To(Equal(errors.New("link add failed")))
			})
		})

		It("assigns the specified address", func() {
			expectedNetwork := &net.IPNet{
				IP:   address,
				Mask: net.CIDRMask(32, 32),
			}
			_, err := factory.CreateBridge("some-bridge-name", address)
			Expect(err).NotTo(HaveOccurred())

			Expect(netlinker.AddrAddCallCount()).To(Equal(1))
			br, addr := netlinker.AddrAddArgsForCall(0)
			Expect(br).To(Equal(expectedBridge))
			Expect(addr).To(Equal(&netlink.Addr{IPNet: expectedNetwork}))
		})

		Context("when assigning an address to the bridge fails", func() {
			It("returns the error", func() {
				netlinker.AddrAddReturns(errors.New("addr add failed"))

				_, err := factory.CreateBridge("some-bridge-name", address)
				Expect(err).To(Equal(errors.New("addr add failed")))
			})
		})

		It("sets the bridge link up", func() {
			_, err := factory.CreateBridge("some-bridge-name", address)
			Expect(err).NotTo(HaveOccurred())

			Expect(netlinker.LinkSetUpCallCount()).To(Equal(1))
			Expect(netlinker.LinkSetUpArgsForCall(0)).To(Equal(expectedBridge))
		})

		Context("when setting the bridge link up fails", func() {
			It("returns the error", func() {
				netlinker.LinkSetUpReturns(errors.New("bridge link up failed"))

				_, err := factory.CreateBridge("some-bridge-name", address)
				Expect(err).To(Equal(errors.New("bridge link up failed")))
			})
		})
	})

	Describe("CreateVxlan", func() {
		var expectedVxlan *netlink.Vxlan

		BeforeEach(func() {
			expectedVxlan = &netlink.Vxlan{
				LinkAttrs: netlink.LinkAttrs{
					Name: "some-device-name",
				},
				VxlanId:  int(42),
				Learning: true,
				Port:     int(nl.Swap16(links.VxlanPort)), //network endian order
				Proxy:    true,
				L3miss:   true,
				L2miss:   true,
			}
		})

		It("should return a vxlan with the expected config", func() {
			link, err := factory.CreateVxlan("some-device-name", 42)
			Expect(err).NotTo(HaveOccurred())
			Expect(link).To(Equal(expectedVxlan))
		})

		It("should add the link", func() {
			_, err := factory.CreateVxlan("some-device-name", 42)
			Expect(err).NotTo(HaveOccurred())

			Expect(netlinker.LinkAddCallCount()).To(Equal(1))
			Expect(netlinker.LinkAddArgsForCall(0)).To(Equal(expectedVxlan))
		})

		Context("when adding the link fails", func() {
			It("should return the error", func() {
				netlinker.LinkAddReturns(errors.New("some error"))

				_, err := factory.CreateVxlan("some-device-name", 42)
				Expect(err).To(Equal(errors.New("some error")))
			})
		})

		It("should set the link up", func() {
			_, err := factory.CreateVxlan("some-device-name", 42)
			Expect(err).NotTo(HaveOccurred())

			Expect(netlinker.LinkSetUpCallCount()).To(Equal(1))
			Expect(netlinker.LinkSetUpArgsForCall(0)).To(Equal(expectedVxlan))
		})

		Context("when setting the link up fails", func() {
			It("should return the error", func() {
				netlinker.LinkSetUpReturns(errors.New("some error"))

				_, err := factory.CreateVxlan("some-device-name", 42)
				Expect(err).To(Equal(errors.New("some error")))
			})
		})
	})

	Describe("FindLink", func() {
		Context("when a link is found", func() {
			It("should return the link", func() {
				netlinker.LinkByNameReturns(&netlink.Vxlan{VxlanId: 41}, nil)
				link, err := factory.FindLink("some-device-name")
				Expect(err).NotTo(HaveOccurred())
				Expect(link).To(Equal(&netlink.Vxlan{VxlanId: 41}))
			})
		})

		Context("when the link does not exist", func() {
			It("should return nil", func() {
				netlinker.LinkByNameReturns(nil, errors.New("not found"))
				_, err := factory.FindLink("some-device-name")
				Expect(err).To(Equal(errors.New("not found")))
			})
		})
	})
})
