package executor

import (
	"fmt"
	"net"

	"github.com/appc/cni/pkg/types"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/links"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/nl"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/ns"
	"github.com/vishvananda/netlink"
)

type Executor struct {
	NetworkNamespacer ns.Namespacer
	LinkFactory       LinkFactory
	Netlinker         nl.Netlinker
	AddressManager    AddressManager
}

//go:generate counterfeiter --fake-name LinkFactory . LinkFactory
type LinkFactory interface {
	CreateVethPair(containerID, hostIfaceName string, mtu int) (sandboxLink netlink.Link, containerLink netlink.Link, err error)
}

//go:generate counterfeiter --fake-name AddressManager . AddressManager
type AddressManager interface {
	AddAddress(link netlink.Link, address *net.IPNet) error
}

func (e *Executor) SetupContainerNS(
	sandboxNsPath string,
	containerNsPath string,
	containerID string,
	interfaceName string,
	ipamResult *types.Result,
) (netlink.Link, error) {

	containerNsHandle, err := e.NetworkNamespacer.GetFromPath(containerNsPath)
	if err != nil {
		return nil, fmt.Errorf("could not open container namespace %q: %s", containerNsPath, err)
	}
	defer containerNsHandle.Close()

	err = e.NetworkNamespacer.Set(containerNsHandle)
	if err != nil {
		return nil, fmt.Errorf("set container namespace %q failed: %s", containerNsPath, err)
	}

	sandboxLink, containerLink, err := e.LinkFactory.CreateVethPair(containerID, interfaceName, links.VxlanVethMTU)
	if err != nil {
		return nil, fmt.Errorf("could not create veth pair: %s", err)
	}

	sandboxNsHandle, err := e.NetworkNamespacer.GetFromPath(sandboxNsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox namespace handle: %s", err)
	}
	defer sandboxNsHandle.Close()

	err = e.Netlinker.LinkSetNsFd(sandboxLink, int(sandboxNsHandle.Fd()))
	if err != nil {
		return nil, fmt.Errorf("failed to move sandbox link into sandbox: %s", err)
	}

	err = e.AddressManager.AddAddress(containerLink, &ipamResult.IP4.IP)
	if err != nil {
		return nil, fmt.Errorf("setting container address failed: %s", err)
	}

	err = e.Netlinker.LinkSetUp(containerLink)
	if err != nil {
		return nil, fmt.Errorf("failed to up container link: %s", err)
	}

	for _, r := range ipamResult.IP4.Routes {
		route := r
		nlRoute := &netlink.Route{
			LinkIndex: containerLink.Attrs().Index,
			Scope:     netlink.SCOPE_UNIVERSE,
			Dst:       &route.Dst,
			Gw:        route.GW,
		}

		if nlRoute.Gw == nil {
			nlRoute.Gw = ipamResult.IP4.Gateway
		}

		err = e.Netlinker.RouteAdd(nlRoute)
		if err != nil {
			return nil, fmt.Errorf("adding route to %s via %s failed: %s", nlRoute.Dst, nlRoute.Gw, err)
		}
	}

	return sandboxLink, nil
}
