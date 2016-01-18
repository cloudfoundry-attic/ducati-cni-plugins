package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/appc/cni/pkg/ipam"
	"github.com/appc/cni/pkg/skel"
	"github.com/appc/cni/pkg/types"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/links"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/namespace"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/nl" //only only on linux - ignore error
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/overlay"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/veth"
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

	v := veth.Veth{Netlinker: nl.Netlink}

	containerIPNet := ipamResult.IP4.IP

	// TODO: implement sandbox that takes netlinker as well
	osSandboxRepoRoot := os.Getenv("DUCATI_OS_SANDBOX_REPO")
	if osSandboxRepoRoot == "" {
		panic("missing required env var DUCATI_OS_SANDBOX_REPO")
	}
	sandboxRepo, err := namespace.NewRepository(osSandboxRepoRoot)
	if err != nil {
		panic(err)
	}

	sandboxNS, err := sandboxRepo.Get(fmt.Sprintf("vni-%d", vni))
	if err != nil {
		sandboxNS, err = sandboxRepo.Create(fmt.Sprintf("vni-%d", vni))
		if err != nil {
			panic(err)
		}
	}

	//sanbox := sandbox.Sandbox{
	//NetLinker:   nl.Netlink,
	//VNI:         1,
	//HostNetwork: netConf.HostNetwork,
	//Network:     netConf.Network,
	//}

	//Sandbox, err := sandbox.Create()

	//err := sandbox.Add(pair.Host)

	pair, err := v.CreatePair("host-name", args.IfName, containerIPNet)
	if err != nil {
		panic(err)
	}

	containerNS := &namespace.Namespace{Path: args.Netns}
	//hostNS := &namespace.Namespace{Path: "/proc/self/ns/net"}

	err = pair.SetupContainer(containerNS)
	if err != nil {
		panic(err)
	}

	err = pair.SetupHost(sandboxNS)
	if err != nil {
		panic(err)
	}

	err = sandboxNS.Execute(func(*os.File) error {
		vxlanName := fmt.Sprintf("vxlan%d", vni)
		linkFactory := &links.Factory{Netlinker: nl.Netlink}
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

		err = nl.Netlink.LinkSetMaster(pair.Host, bridge)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		panic(err)
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

	overlayController := &overlay.Controller{}
	return overlayController.Delete(args.Netns, args.IfName)
}

func main() {
	runtime.LockOSThread()
	skel.PluginMain(cmdAdd, cmdDel)
}
