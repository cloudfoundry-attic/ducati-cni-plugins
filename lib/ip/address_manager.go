package ip

import (
	"net"

	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/nl"
	"github.com/vishvananda/netlink"
)

type AddressManager struct {
	Netlinker nl.Netlinker
}

func (am *AddressManager) AddAddress(link netlink.Link, address net.IP) error {
	addr := &netlink.Addr{
		IPNet: &net.IPNet{
			IP:   address,
			Mask: net.CIDRMask(32, 32),
		},
	}
	return am.Netlinker.AddrAdd(link, addr)
}
