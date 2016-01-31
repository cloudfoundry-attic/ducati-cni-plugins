package executor

import (
	"net"
	"os"

	"github.com/appc/cni/pkg/types"
	"github.com/vishvananda/netlink"
)

type Executor struct {
	ContainerNS    Namespacer
	SandboxNS      Namespacer
	LinkFactory    LinkFactory
	Netlinker      Netlinker
	AddressManager AddressManager
}

//go:generate counterfeiter --fake-name Namespacer . Namespacer
type Namespacer interface {
	Open() (*os.File, error)
	Execute(func(*os.File) error) error
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

func (e *Executor) SetupContainerNS(containerID string, interfaceName string, ipamResult *types.Result) (netlink.Link, error) {
	//containerNamespaceFile, err := containerNS.Open()
	//if err != nil {
	//return fmt.Errorf("opening container namespace: %s", err)
	//}
	//defer containerNamespaceFile.Close()

	//err = containerNS.Execute(func(ns *os.File) error {
	//var (
	//containerLink netlink.Link
	//err           error
	//)

	//sandboxLink, containerLink, err = linkFactory.CreateVethPair(args.ContainerID, args.IfName, links.VxlanVethMTU)
	//if err != nil {
	//return fmt.Errorf("could not create veth pair: %s", err)
	//}

	//err = nl.Netlink.LinkSetNsFd(sandboxLink, int(sandboxNamespaceFile.Fd()))
	//if err != nil {
	//return fmt.Errorf("failed to move veth peer into sandbox: %s", err)
	//}

	//err = addressManager.AddAddress(containerLink, &ipamResult.IP4.IP)
	//if err != nil {
	//return fmt.Errorf("adding address to container veth end: %s", err)
	//}

	//err = nl.Netlink.LinkSetUp(containerLink)
	//if err != nil {
	//return fmt.Errorf("upping container veth end: %s", err)
	//}

	//for _, route := range ipamResult.IP4.Routes {
	//// TODO supporting gateway assigned to a particular route
	//nlRoute := &netlink.Route{
	//LinkIndex: containerLink.Attrs().Index,
	//Scope:     netlink.SCOPE_UNIVERSE,
	//Dst:       &route.Dst,
	//Gw:        ipamResult.IP4.Gateway,
	//}
	//err = nl.Netlink.RouteAdd(nlRoute)
	//if err != nil {
	//return fmt.Errorf("adding routes: %s", err)
	//}
	//}

	//return nil
	//})
	//if err != nil {
	//return fmt.Errorf("configuring container namespace: %s", err)
	//}

	return nil, nil
}
