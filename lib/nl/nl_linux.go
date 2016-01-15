package nl

import "github.com/vishvananda/netlink"

type nl struct {
	linkAdd     func(netlink.Link) error
	linkSetUp   func(netlink.Link) error
	linkByName  func(string) (netlink.Link, error)
	linkSetNsFd func(netlink.Link, int) error
}

var Netlink = nl{
	linkAdd:     netlink.LinkAdd,
	linkSetUp:   netlink.LinkSetUp,
	linkByName:  netlink.LinkByName,
	linkSetNsFd: netlink.LinkSetNsFd,
}

func (n nl) LinkAdd(link netlink.Link) error {
	return n.linkAdd(link)
}

func (n nl) LinkSetUp(link netlink.Link) error {
	return n.linkSetUp(link)
}

func (n nl) LinkByName(name string) (netlink.Link, error) {
	return n.linkByName(name)
}

func (n nl) LinkSetNsFd(link netlink.Link, fd int) error {
	return n.linkSetNsFd(link, fd)
}
