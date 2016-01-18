package veth_test

import (
	"errors"
	"io/ioutil"
	"net"
	"os"

	nlfakes "github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/nl/fakes"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/veth"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/veth/fakes"
	"github.com/vishvananda/netlink"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = PDescribe("Veth", func() {
	var (
		netlinker      *nlfakes.Netlinker
		namespace      *fakes.Namespace
		v              veth.Veth
		containerIPNet net.IPNet
	)

	BeforeEach(func() {
		netlinker = &nlfakes.Netlinker{}
		namespace = &fakes.Namespace{}
		v = veth.Veth{
			Netlinker: netlinker,
		}
		containerIPNet = net.IPNet{}
	})

	Describe("CreatePair", func() {
		It("should use netlink to add a veth pair", func() {
			expectedHostVeth := &netlink.Veth{
				LinkAttrs: netlink.LinkAttrs{
					Name: "host-eth-name",
				},
			}

			expectedContainerVeth := &netlink.Veth{
				LinkAttrs: netlink.LinkAttrs{
					Name:  "some-container-name",
					Flags: net.FlagUp,
					MTU:   1450,
				},
				PeerName: "some-host-name",
			}

			netlinker.LinkByNameReturns(expectedHostVeth, nil)

			vethPair, err := v.CreatePair("some-host-name", "some-container-name", containerIPNet)
			Expect(err).NotTo(HaveOccurred())
			Expect(vethPair.Host).To(Equal(expectedHostVeth))
			Expect(vethPair.Container).To(Equal(expectedContainerVeth))

			Expect(netlinker.LinkAddCallCount()).To(Equal(1))

			netlinkVeth := netlinker.LinkAddArgsForCall(0)
			Expect(netlinkVeth).To(Equal(expectedContainerVeth))

			Expect(netlinker.LinkByNameCallCount()).To(Equal(1))
			Expect(netlinker.LinkByNameArgsForCall(0)).To(Equal("some-host-name"))
		})

		Context("when there is an error during adding a link", func() {
			It("returns the error", func() {
				netlinker.LinkAddReturns(errors.New("some-link-add-error"))

				_, err := v.CreatePair("some-host-name", "some-container-name", containerIPNet)
				Expect(err).To(MatchError("error adding link: some-link-add-error"))
			})
		})

		Context("when there is an error retrieving a link by name", func() {
			It("returns the error", func() {
				netlinker.LinkByNameReturns(nil, errors.New("some-link-by-name-error"))

				_, err := v.CreatePair("some-host-name", "some-host-name", containerIPNet)
				Expect(err).To(MatchError("error finding link by name: some-link-by-name-error"))
			})
		})
	})

	Describe("SetupContainer", func() {
		var (
			nsFile *os.File
			pair   *veth.Pair
		)

		BeforeEach(func() {
			var err error

			pair = &veth.Pair{
				Netlinker: netlinker,
				Host:      &netlink.Veth{},
				Container: &netlink.Veth{},
			}

			nsFile, err = ioutil.TempFile("", "testing")
			Expect(err).NotTo(HaveOccurred())

			namespace.ExecuteStub = func(callback func(*os.File) error) error {
				return callback(nsFile)
			}
		})

		AfterEach(func() {
			defer nsFile.Close()
		})

		It("should set the container link to UP in the container namespace", func() {
			err := pair.SetupContainer(namespace)
			Expect(err).NotTo(HaveOccurred())

			Expect(netlinker.LinkSetUpCallCount()).To(Equal(1))
			netlinkVeth := netlinker.LinkSetUpArgsForCall(0)
			Expect(netlinkVeth).To(Equal(pair.Container))
		})

		Context("when an error occurs during setting the link to UP", func() {
			It("return an error", func() {
				netlinker.LinkSetUpReturns(errors.New("some-link-up-error"))

				err := pair.SetupContainer(namespace)
				Expect(err).To(MatchError("failed setting link UP: some-link-up-error"))
			})
		})
	})

	Describe("SetupHost", func() {
		var (
			nsFile *os.File
			pair   *veth.Pair
		)

		BeforeEach(func() {
			var err error

			pair = &veth.Pair{
				Netlinker: netlinker,
				Host:      &netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: "host"}},
				Container: &netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: "container"}},
			}

			nsFile, err = ioutil.TempFile("", "testing")
			Expect(err).NotTo(HaveOccurred())

			namespace.ExecuteStub = func(callback func(*os.File) error) error {
				return callback(nsFile)
			}
		})

		AfterEach(func() {
			defer nsFile.Close()
		})

		It("should move into the host namespace and set UP within it", func() {
			err := pair.SetupHost(namespace)
			Expect(err).NotTo(HaveOccurred())

			Expect(namespace.ExecuteCallCount()).To(Equal(1))

			Expect(netlinker.LinkSetNsFdCallCount()).To(Equal(1))
			hostLink, fd := netlinker.LinkSetNsFdArgsForCall(0)
			Expect(hostLink).To(Equal(pair.Host))
			Expect(fd).To(Equal(int(nsFile.Fd())))

			Expect(netlinker.LinkSetUpCallCount()).To(Equal(1))
			hostLink = netlinker.LinkSetUpArgsForCall(0)
			Expect(hostLink).To(Equal(pair.Host))
		})

		Context("when an error occurs entering the namespace", func() {
			It("returns the error", func() {
				netlinker.LinkSetNsFdReturns(errors.New("some-error"))

				err := pair.SetupHost(namespace)
				Expect(err).To(MatchError("failed entering namespace: some-error"))
			})
		})

		Context("when an error occurs setting the link to UP", func() {
			It("returns the error", func() {
				netlinker.LinkSetUpReturns(errors.New("some-error"))

				err := pair.SetupHost(namespace)
				Expect(err).To(MatchError("failed setting link UP: some-error"))
			})
		})
	})
})
