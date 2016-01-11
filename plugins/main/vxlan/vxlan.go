package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"runtime"
	"syscall"

	"github.com/appc/cni/pkg/ip"
	"github.com/appc/cni/pkg/ipam"
	"github.com/appc/cni/pkg/ns"
	"github.com/appc/cni/pkg/skel"
	"github.com/appc/cni/pkg/types"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
)

const (
	vxlanPort    = 4789
	vxlanVethMTU = 1450

	databaseLocation = "/vagrant/cni_runtime/peers.json"
)

type NetConf struct {
	types.NetConf
	Network     string `json:"network"`
	HostNetwork string `json:"host_network"`
}

type peer struct {
	HostAddress      string
	ContainerAddress string
	LinkAddress      string
}

type peerDB map[string]peer

func init() {
	// this ensures that main runs only on main thread (thread group leader).
	// since namespace ops (unshare, setns) are done for a single thread, we
	// must ensure that the goroutine does not jump from OS thread to thread
	runtime.LockOSThread()
}

func loadConf(bytes []byte) (*NetConf, error) {
	n := &NetConf{}
	if err := json.Unmarshal(bytes, n); err != nil {
		return nil, fmt.Errorf("failed to load netconf: %v", err)
	}
	if n.Network == "" {
		return nil, fmt.Errorf(`"network" field is required. It specifies the overlay subnet`)
	}

	return n, nil
}

func loadPeers(location string) (peerDB, error) {
	bytes, err := ioutil.ReadFile(location)
	if err != nil {
		if os.IsNotExist(err) {
			return peerDB{}, nil
		}
		return nil, err
	}

	n := peerDB{}
	if err := json.Unmarshal(bytes, &n); err != nil {
		return nil, fmt.Errorf("failed to load peer DB: %v", err)
	}

	return n, nil
}

func savePeers(location string, peers peerDB) error {
	bytes, err := json.Marshal(peers)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(location, bytes, os.FileMode(0644))
}

func findInterfaceAddress(cidr string) (string, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", err
	}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}

	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil {
			return "", err
		}

		if network.Contains(ip) {
			return ip.String(), nil
		}
	}

	return "", nil
}

func renameLink(curName, newName string) error {
	link, err := netlink.LinkByName(curName)
	if err != nil {
		return err
	}

	return netlink.LinkSetName(link, newName)
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

func setupVeth(netns string, br *netlink.Bridge, ifName string, mtu int) error {
	var hostVethName string

	err := ns.WithNetNSPath(netns, false, func(hostNS *os.File) error {
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

func cmdAdd(args *skel.CmdArgs) error {
	const vni = 1
	netConf, err := loadConf(args.StdinData)
	if err != nil {
		return err
	}

	peers, err := loadPeers(databaseLocation)
	if err != nil {
		return err
	}

	netns, err := os.Open(args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", netns, err)
	}
	defer netns.Close()

	// run the IPAM plugin and get back the config to apply
	result, err := ipam.ExecAdd(netConf.IPAM.Type, args.StdinData)
	if err != nil {
		return err
	}
	if result.IP4 == nil {
		return errors.New("IPAM plugin returned missing IPv4 config")
	}

	bridgeDevice, err := setupNetwork(vni, result.IP4.Gateway)
	if err != nil {
		return err
	}

	if err := setupVeth(args.Netns, bridgeDevice, args.IfName, vxlanVethMTU); err != nil {
		return err
	}

	err = ns.WithNetNS(netns, false, func(_ *os.File) error {
		return ipam.ConfigureIface(args.IfName, result)
	})
	if err != nil {
		return err
	}

	var containerHardwareAddr string
	err = ns.WithNetNS(netns, false, func(_ *os.File) error {
		l, err := netlink.LinkByName(args.IfName)
		if err != nil {
			return err
		}
		if veth, ok := l.(*netlink.Veth); ok {
			containerHardwareAddr = veth.Attrs().HardwareAddr.String()
		}
		return nil
	})

	hostAddress, err := findInterfaceAddress(netConf.HostNetwork)
	if err != nil {
		return err
	}

	peers[args.ContainerID] = peer{
		HostAddress:      hostAddress,
		ContainerAddress: result.IP4.IP.IP.String(),
		LinkAddress:      containerHardwareAddr,
	}

	if err := savePeers(databaseLocation, peers); err != nil {
		return err
	}

	return result.Print()
}

func cmdDel(args *skel.CmdArgs) error {
	n, err := loadConf(args.StdinData)
	if err != nil {
		return err
	}

	peers, err := loadPeers(databaseLocation)
	if err != nil {
		return err
	}

	delete(peers, args.ContainerID)

	err = ipam.ExecDel(n.IPAM.Type, args.StdinData)
	if err != nil {
		return err
	}

	// container namespace
	err = ns.WithNetNSPath(args.Netns, false, func(hostNS *os.File) error {
		return ip.DelLinkByName(args.IfName)
	})

	if serr := savePeers(databaseLocation, peers); serr != nil {
		return serr
	}

	return err
}

func main() {
	skel.PluginMain(cmdAdd, cmdDel)
}
