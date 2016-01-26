package nl

import "github.com/vishvananda/netlink"

//go:generate counterfeiter --fake-name Netlinker . Netlinker
type Netlinker interface {
	LinkAdd(link netlink.Link) error
	LinkDel(link netlink.Link) error
	LinkList() ([]netlink.Link, error)
	LinkSetUp(link netlink.Link) error
	LinkByName(name string) (netlink.Link, error)
	LinkSetNsFd(link netlink.Link, fd int) error
	AddrAdd(link netlink.Link, addr *netlink.Addr) error
	LinkSetMaster(slave netlink.Link, master *netlink.Bridge) error
	RouteAdd(*netlink.Route) error
}
