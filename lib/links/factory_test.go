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

	Describe("CreateVethPair", func() {
		It("adds a veth link with the appropriate names and MTU", func() {
			_, _, err := factory.CreateVethPair("host", "container", 999)
			Expect(err).NotTo(HaveOccurred())

			Expect(netlinker.LinkAddCallCount()).To(Equal(1))
			veth, ok := netlinker.LinkAddArgsForCall(0).(*netlink.Veth)
			Expect(ok).To(BeTrue())

			Expect(veth.Attrs().Name).To(Equal("container"))
			Expect(veth.Attrs().MTU).To(Equal(999))
			Expect(veth.PeerName).To(Equal("host"))
		})

		It("retrieves the host link data after creating the pair", func() {
			netlinker.LinkByNameStub = func(_ string) (netlink.Link, error) {
				Expect(netlinker.LinkAddCallCount()).To(Equal(1))
				return &netlink.Veth{}, nil
			}

			_, _, err := factory.CreateVethPair("host", "container", 999)
			Expect(err).NotTo(HaveOccurred())

			Expect(netlinker.LinkByNameCallCount()).To(Equal(1))
			Expect(netlinker.LinkByNameArgsForCall(0)).To(Equal("host"))
		})

		It("returns the container link that was added", func() {
			_, container, err := factory.CreateVethPair("host", "container", 999)
			Expect(err).NotTo(HaveOccurred())

			Expect(netlinker.LinkAddCallCount()).To(Equal(1))
			addedLink := netlinker.LinkAddArgsForCall(0)
			Expect(container).To(Equal(addedLink))
		})

		It("returns the host link that was retrieved", func() {
			expectedHostLink := &netlink.Veth{
				LinkAttrs: netlink.LinkAttrs{
					Name: "host",
					MTU:  999,
				},
				PeerName: "container",
			}
			netlinker.LinkByNameReturns(expectedHostLink, nil)

			host, _, err := factory.CreateVethPair("host", "container", 999)
			Expect(err).NotTo(HaveOccurred())

			Expect(netlinker.LinkByNameCallCount()).To(Equal(1))
			Expect(host).To(Equal(expectedHostLink))
		})

		Context("when adding the veth link fails", func() {
			var linkAddError error

			BeforeEach(func() {
				linkAddError = errors.New("link add failed")
				netlinker.LinkAddReturns(linkAddError)
			})

			It("returns the error", func() {
				_, _, err := factory.CreateVethPair("host", "container", 999)
				Expect(err).To(Equal(linkAddError))
			})
		})

		Context("when retrieving the host link fails", func() {
			var linkByNameError error

			BeforeEach(func() {
				linkByNameError = errors.New("link not found")
				netlinker.LinkByNameReturns(nil, linkByNameError)
			})

			It("returns the error", func() {
				_, _, err := factory.CreateVethPair("host", "container", 999)
				Expect(err).To(Equal(linkByNameError))
			})
		})
	})

	Describe("FindLink", func() {
		Context("when a link is found", func() {
			BeforeEach(func() {
				netlinker.LinkByNameReturns(&netlink.Vxlan{VxlanId: 41}, nil)
			})

			It("should return the link", func() {
				link, err := factory.FindLink("some-device-name")
				Expect(err).NotTo(HaveOccurred())
				Expect(link).To(Equal(&netlink.Vxlan{VxlanId: 41}))
			})
		})

		Context("when the link does not exist", func() {
			BeforeEach(func() {
				netlinker.LinkByNameReturns(nil, errors.New("not found"))
			})

			It("should return nil", func() {
				_, err := factory.FindLink("some-device-name")
				Expect(err).To(Equal(errors.New("not found")))
			})
		})
	})
})
