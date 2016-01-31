package executor

import (
	"net"

	"github.com/appc/cni/pkg/types"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/links"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

type Executor struct {
	NetworkNamespacer Namespacer
	LinkFactory       LinkFactory
	Netlinker         Netlinker
	AddressManager    AddressManager
}

//go:generate counterfeiter --fake-name Namespacer . Namespacer
type Namespacer interface {
	GetFromPath(string) (netns.NsHandle, error)
	Set(netns.NsHandle) error
}

//go:generate counterfeiter --fake-name Netlinker . Netlinker
type Netlinker interface {
	LinkSetNsFd(link netlink.Link, fd int) error
	RouteAdd(*netlink.Route) error
	LinkSetUp(link netlink.Link) error
}

//go:generate counterfeiter --fake-name LinkFactory . LinkFactory
type LinkFactory interface {
	CreateVethPair(containerID, hostIfaceName string, mtu int) (sandboxLink netlink.Link, containerLink netlink.Link, err error)
}

//go:generate counterfeiter --fake-name AddressManager . AddressManager
type AddressManager interface {
	AddAddress(link netlink.Link, address *net.IPNet) error
}

func (e *Executor) SetupContainerNS(sandboxNsPath string,
	containerNsPath string, containerID string, interfaceName string, ipamResult *types.Result) (netlink.Link, error) {

	containerNsHandle, err := e.NetworkNamespacer.GetFromPath(containerNsPath)
	if err != nil {
		//return fmt.Errorf("could not create veth pair: %s", err)
	}

	err = e.NetworkNamespacer.Set(containerNsHandle)

	sandboxLink, containerLink, err := e.LinkFactory.CreateVethPair(containerID, interfaceName, links.VxlanVethMTU)
	if err != nil {
		//return fmt.Errorf("could not create veth pair: %s", err)
	}

	sandboxNsHandle, err := e.NetworkNamespacer.GetFromPath(sandboxNsPath)
	if err != nil {
		//return fmt.Errorf("could not create veth pair: %s", err)
	}

	err = e.Netlinker.LinkSetNsFd(sandboxLink, int(sandboxNsHandle))
	if err != nil {
		//return fmt.Errorf("failed to move veth peer into sandbox: %s", err)
	}

	err = e.AddressManager.AddAddress(containerLink, &ipamResult.IP4.IP)
	if err != nil {
		//return fmt.Errorf("adding address to container veth end: %s", err)
	}

	err = e.Netlinker.LinkSetUp(containerLink)
	if err != nil {
		//return fmt.Errorf("upping container veth end: %s", err)
	}

	for _, route := range ipamResult.IP4.Routes {
		// TODO supporting gateway assigned to a particular route
		nlRoute := &netlink.Route{
			LinkIndex: containerLink.Attrs().Index,
			Scope:     netlink.SCOPE_UNIVERSE,
			Dst:       &route.Dst,
			Gw:        ipamResult.IP4.Gateway,
		}
		err = e.Netlinker.RouteAdd(nlRoute)
		if err != nil {
			// return fmt.Errorf("adding routes: %s", err)
		}
	}

	return sandboxLink, nil
}
