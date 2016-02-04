package executor

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"

	"github.com/appc/cni/pkg/types"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/links"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/namespace"
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
	FindLink(name string) (netlink.Link, error)
	CreateVxlan(name string, vni int) (netlink.Link, error)
}

//go:generate counterfeiter --fake-name AddressManager . AddressManager
type AddressManager interface {
	AddAddress(link netlink.Link, address *net.IPNet) error
}

const selfPath = "/proc/self/ns/net"

func (e *Executor) EnsureVxlanDeviceExists(vni int, sandboxNS namespace.Namespace) (string, error) {
	vxlanName := fmt.Sprintf("vxlan%d", vni)

	var foundVxlanDevice bool
	err := sandboxNS.Execute(func(ns *os.File) error {
		if _, err := e.LinkFactory.FindLink(vxlanName); err == nil {
			foundVxlanDevice = true
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed attempting to find vxlan device in sandbox: %s", err)
	}

	// create vxlan device within host namespace
	if !foundVxlanDevice {

		vxlan, err := e.LinkFactory.CreateVxlan(vxlanName, vni)
		if err != nil {
			return "", fmt.Errorf("creating vxlan device on host namespace: %s", err)
		}

		sandboxNamespaceFile, err := sandboxNS.Open()
		if err != nil {
			return "", fmt.Errorf("opening sandbox namespace: %s", err)
		}
		defer sandboxNamespaceFile.Close()

		// move vxlan device to sandbox namespace
		err = e.Netlinker.LinkSetNsFd(vxlan, int(sandboxNamespaceFile.Fd()))
		if err != nil {
			return "", fmt.Errorf("moving vxlan device into sandbox: %s", err)
		}
	}

	return vxlanName, nil
}

func (e *Executor) SetupContainerNS(
	sandboxNsPath string,
	containerNsPath string,
	containerID string,
	interfaceName string,
	ipamResult *types.Result,
) (netlink.Link, error) {
	hostNsHandle, err := e.NetworkNamespacer.GetFromPath(selfPath)
	if err != nil {
		panic(err)
	}
	defer e.restoreAndCloseNamespace(hostNsHandle)

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
		if err != nil && err != unix.EEXIST {
			return nil, fmt.Errorf("adding route to %s via %s failed: %s", nlRoute.Dst, nlRoute.Gw, err)
		}
	}

	return sandboxLink, nil
}

func (e *Executor) restoreAndCloseNamespace(handle ns.Handle) {
	err := e.NetworkNamespacer.Set(handle)
	if err != nil {
		panic(err)
	}

	err = handle.Close()
	if err != nil {
		panic(err)
	}
}
