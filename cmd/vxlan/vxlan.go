package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"

	"github.com/appc/cni/pkg/ipam"
	"github.com/appc/cni/pkg/skel"
	"github.com/appc/cni/pkg/types"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/links"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/namespace"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/nl" //only only on linux - ignore error
	"github.com/vishvananda/netlink"
)

type NetConf struct {
	types.NetConf
	Network     string `json:"network"` // IPNet
	HostNetwork string `json:"host_network"`
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

func cmdAdd(args *skel.CmdArgs) error {
	const vni = 1
	const vxlanMTU = 1450

	netConf, err := loadConf(args.StdinData)
	if err != nil {
		return err
	}

	// run the IPAM plugin and get back the config to apply
	ipamResult, err := ipam.ExecAdd(netConf.IPAM.Type, args.StdinData)
	if err != nil {
		return err
	}

	if ipamResult.IP4 == nil {
		return errors.New("IPAM plugin returned missing IPv4 config")
	}

	linkFactory := &links.Factory{Netlinker: nl.Netlink}

	hostLink, containerLink, err := linkFactory.CreateVethPair("host-name", args.IfName, vxlanMTU)
	if err != nil {
		panic(err)
	}

	containerNS := namespace.NewNamespace(args.Netns)

	f, err := os.Open(containerNS.Path())
	if err != nil {
		panic(err)
	}

	err = nl.Netlink.LinkSetNsFd(containerLink, int(f.Fd()))
	if err != nil {
		panic(err)
	}

	err = containerNS.Execute(func(ns *os.File) error {
		link, err := nl.Netlink.LinkByName(args.IfName)
		if err != nil {
			return err
		}

		addr := &netlink.Addr{
			IPNet: &net.IPNet{
				IP:   ipamResult.IP4.IP.IP,
				Mask: net.CIDRMask(32, 32),
			},
		}
		err = nl.Netlink.AddrAdd(link, addr)
		if err != nil {
			return err
		}

		err = nl.Netlink.LinkSetUp(link)
		if err != nil {
			panic(err)
		}

		return nil
	})
	if err != nil {
		panic(err)
	}

	err = nl.Netlink.LinkSetUp(hostLink)
	if err != nil {
		panic(err)
	}

	vxlanName := fmt.Sprintf("vxlan%d", vni)
	vxlan, err := linkFactory.FindLink(vxlanName)
	if err != nil {
		vxlan, err = linkFactory.CreateVxlan(vxlanName, vni)
		if err != nil {
			panic(err)
		}
	}

	var bridge *netlink.Bridge
	bridgeName := fmt.Sprintf("vxlanbr%d", vni)
	link, err := linkFactory.FindLink(bridgeName)
	if err != nil {
		bridge, err = linkFactory.CreateBridge(bridgeName, ipamResult.IP4.Gateway)
		if err != nil {
			panic(err)
		}
	} else {
		bridge = link.(*netlink.Bridge)
	}

	err = nl.Netlink.LinkSetMaster(vxlan, bridge)
	if err != nil {
		return err
	}

	err = nl.Netlink.LinkSetMaster(hostLink, bridge)
	if err != nil {
		return err
	}

	return ipamResult.Print()
}

func cmdDel(args *skel.CmdArgs) error {
	n, err := loadConf(args.StdinData)
	if err != nil {
		return err
	}

	err = ipam.ExecDel(n.IPAM.Type, args.StdinData)
	if err != nil {
		return err
	}

	return nil
}

func main() {
	runtime.LockOSThread()
	skel.PluginMain(cmdAdd, cmdDel)
}
