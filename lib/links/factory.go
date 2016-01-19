package links

import (
	"net"

	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/nl"
	"github.com/vishvananda/netlink"
	vnl "github.com/vishvananda/netlink/nl"
)

const (
	BridgeMTU    = 1500
	VxlanPort    = 4789
	VxlanVethMTU = 1450
)

type Factory struct {
	Netlinker nl.Netlinker
}

func (f *Factory) CreateBridge(name string, addr net.IP) (*netlink.Bridge, error) {
	bridge := &netlink.Bridge{
		LinkAttrs: netlink.LinkAttrs{
			Name: name,
			MTU:  BridgeMTU,
		},
	}

	err := f.Netlinker.LinkAdd(bridge)
	if err != nil {
		return nil, err
	}

	err = f.Netlinker.AddrAdd(bridge, &netlink.Addr{
		IPNet: &net.IPNet{
			IP:   addr,
			Mask: net.CIDRMask(32, 32),
		},
	})
	if err != nil {
		return nil, err
	}

	err = f.Netlinker.LinkSetUp(bridge)
	if err != nil {
		return nil, err
	}

	return bridge, nil
}

func (f *Factory) CreateVethPair(hostName, containerName string) (netlink.Link, netlink.Link, error) {
	containerLink := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{
			Name: containerName,
			//Flags: net.FlagUp,
			//MTU:   1450,
		},
		//PeerName: hostName,
	}
	hostLink := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{
			Name: hostName,
		},
	}
	err := f.Netlinker.LinkAdd(containerLink)
	if err != nil {
		panic(err)
	}
	return hostLink, containerLink, nil
}

func (f *Factory) CreateVxlan(name string, vni int) (netlink.Link, error) {
	vxlan := &netlink.Vxlan{
		LinkAttrs: netlink.LinkAttrs{
			Name: name,
		},
		VxlanId:  vni,
		Learning: true,
		Port:     int(vnl.Swap16(VxlanPort)), //network endian order
		Proxy:    true,
		L3miss:   true,
		L2miss:   true,
	}

	err := f.Netlinker.LinkAdd(vxlan)
	if err != nil {
		return nil, err
	}

	err = f.Netlinker.LinkSetUp(vxlan)
	if err != nil {
		return nil, err
	}

	return vxlan, nil
}

func (f *Factory) FindLink(name string) (netlink.Link, error) {
	return f.Netlinker.LinkByName(name)
}
