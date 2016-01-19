package ip

import (
	"net"

	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/nl"
	"github.com/vishvananda/netlink"
)

type RouteManager struct {
	Netlinker nl.Netlinker
}

func (rm *RouteManager) AddRoute(link netlink.Link, network *net.IPNet, gateway net.IP) error {
	return rm.Netlinker.RouteAdd(&netlink.Route{
		LinkIndex: link.Attrs().Index,
		Scope:     netlink.SCOPE_UNIVERSE,
		Dst:       network,
		Gw:        gateway,
	})
}
