package overlay

import (
	"fmt"
	"net"
	"os"
	"syscall"

	"github.com/appc/cni/pkg/ip"
	"github.com/appc/cni/pkg/ipam"
	"github.com/appc/cni/pkg/ns"
	"github.com/appc/cni/pkg/types"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
)

const (
	vxlanPort    = 4789
	vxlanVethMTU = 1450
)

//go:generate counterfeiter --fake-name NamespaceRepo . NamespaceRepo
type NamespaceRepo interface {
	Find(namespacePath string) (*os.File, error)
}

//go:generate counterfeiter --fake-name NetworkSandbox . NetworkSandbox
type NetworkSandbox interface {
	AddContainer(netns *os.File, ifName string, ipamResult *types.Result) error
}

//go:generate counterfeiter --fake-name NetworkSandboxRepo . NetworkSandboxRepo
type NetworkSandboxRepo interface {
	Find(vni int) (NetworkSandbox, error)
	Create(vni int, gateway net.IP) (NetworkSandbox, error)
}

type Controller struct {
	NetworkSandboxRepo NetworkSandboxRepo
	NamespaceRepo      NamespaceRepo
}

func (c *Controller) Delete(networkNSPath, interfaceName string) error {
	return ns.WithNetNSPath(networkNSPath, false, func(hostNS *os.File) error {
		return ip.DelLinkByName(interfaceName)
	})
}

func (c *Controller) Add(networkNSPath, interfaceName string, vni int, ipamResult *types.Result) error {
	netns, err := c.NamespaceRepo.Find(networkNSPath)
	if err != nil {
		panic(fmt.Errorf("failed to open netns %q: %v", netns, err))
	}
	defer netns.Close()

	networkSandbox, err := c.NetworkSandboxRepo.Find(vni)
	if err != nil {
		return err
	}
	if networkSandbox == nil {
		networkSandbox, err = c.NetworkSandboxRepo.Create(vni, ipamResult.IP4.Gateway)
		if err != nil {
			return err
		}
	}

	//bridgeDevice, err := setupNetwork(vni, ipamResult.IP4.Gateway)
	if err != nil {
		return err
	}

	//return c.addContainerToNetwork(netns, bridgeDevice, interfaceName, ipamResult)

	return networkSandbox.AddContainer(netns, interfaceName, ipamResult)
}

func (c *Controller) addContainer(netns *os.File, bridgeDevice *netlink.Bridge, interfaceName string, ipamResult *types.Result) error {
	if err := setupVeth(netns, bridgeDevice, interfaceName, vxlanVethMTU); err != nil {
		return err
	}

	err := ns.WithNetNS(netns, false, func(_ *os.File) error {
		return ipam.ConfigureIface(interfaceName, ipamResult)
	})
	if err != nil {
		return err
	}

	//var containerHardwareAddr string
	err = ns.WithNetNS(netns, false, func(_ *os.File) error {
		_, err := netlink.LinkByName(interfaceName)
		if err != nil {
			return err
		}
		//if veth, ok := l.(*netlink.Veth); ok {
		//containerHardwareAddr = veth.Attrs().HardwareAddr.String()
		//}
		return nil
	})

	return err
}

func createVxlan(vni int) (*netlink.Vxlan, error) {
	deviceName := fmt.Sprintf("vxlan%d", vni)
	if device, err := netlink.LinkByName(deviceName); err == nil {
		return device.(*netlink.Vxlan), nil
	}

	vxlan := &netlink.Vxlan{
		LinkAttrs: netlink.LinkAttrs{
			Name: deviceName,
		},
		VxlanId:  int(vni),
		Learning: true,
		Port:     int(nl.Swap16(vxlanPort)), //network endian order
		Proxy:    true,
		L3miss:   true,
		L2miss:   true,
	}

	if err := netlink.LinkAdd(vxlan); err != nil {
		return nil, fmt.Errorf("error creating vxlan interface: %v", err)
	}

	if err := netlink.LinkSetUp(vxlan); err != nil {
		return nil, fmt.Errorf("error bringing up vxlan interface: %v", err)
	}

	return vxlan, nil
}

func bridgeByName(name string) (*netlink.Bridge, error) {
	l, err := netlink.LinkByName(name)
	if err != nil {
		return nil, fmt.Errorf("could not lookup %q: %v", name, err)
	}
	br, ok := l.(*netlink.Bridge)
	if !ok {
		return nil, fmt.Errorf("%q already exists but is not a bridge", name)
	}
	return br, nil
}

func ensureBridge(brName string, mtu int, address net.IP) (*netlink.Bridge, error) {
	br := &netlink.Bridge{
		LinkAttrs: netlink.LinkAttrs{
			Name: brName,
			MTU:  mtu,
		},
	}

	if err := netlink.LinkAdd(br); err != nil {
		if err != syscall.EEXIST {
			return nil, fmt.Errorf("could not add %q: %v", brName, err)
		}

		// it's ok if the device already exists as long as config is similar
		br, err = bridgeByName(brName)
		if err != nil {
			return nil, err
		}
		return br, nil
	}

	network := net.IPNet{
		IP:   address,
		Mask: net.CIDRMask(32, 32),
	}

	addr := &netlink.Addr{IPNet: &network, Label: ""}
	if err := netlink.AddrAdd(br, addr); err != nil {
		return nil, fmt.Errorf("failed to add IP addr to %q: %v", brName, err)
	}

	if err := netlink.LinkSetUp(br); err != nil {
		return nil, err
	}

	return br, nil
}

func setupBridge(vni int, address net.IP) (*netlink.Bridge, error) {
	bridgeName := fmt.Sprintf("vxlanbr%d", vni)
	// create bridge if necessary
	br, err := ensureBridge(bridgeName, vxlanVethMTU, address)
	if err != nil {
		return nil, fmt.Errorf("failed to create bridge %q: %v", bridgeName, err)
	}

	return br, nil
}

func setupNetwork(vni int, address net.IP) (*netlink.Bridge, error) {
	vxlanDevice, err := createVxlan(vni)
	if err != nil {
		return nil, err
	}

	bridgeDevice, err := setupBridge(vni, address)
	if err != nil {
		return nil, err
	}

	if err = netlink.LinkSetMaster(vxlanDevice, bridgeDevice); err != nil {
		return nil, fmt.Errorf("failed to connect %q to bridge %v: %v", vxlanDevice, bridgeDevice.Attrs().Name, err)
	}

	return bridgeDevice, nil
}

func setupVeth(netns *os.File, br *netlink.Bridge, ifName string, mtu int) error {
	var hostVethName string

	err := ns.WithNetNS(netns, false, func(hostNS *os.File) error {
		// create the veth pair in the container and move host end into host netns
		hostVeth, _, err := ip.SetupVeth(ifName, mtu, hostNS)
		if err != nil {
			return err
		}

		hostVethName = hostVeth.Attrs().Name
		return nil
	})
	if err != nil {
		return err
	}

	// need to lookup hostVeth again as its index has changed during ns move
	hostVeth, err := netlink.LinkByName(hostVethName)
	if err != nil {
		return fmt.Errorf("failed to lookup %q: %v", hostVethName, err)
	}

	// connect host veth end to the bridge
	if err = netlink.LinkSetMaster(hostVeth, br); err != nil {
		return fmt.Errorf("failed to connect %q to bridge %v: %v", hostVethName, br.Attrs().Name, err)
	}

	return nil
}
