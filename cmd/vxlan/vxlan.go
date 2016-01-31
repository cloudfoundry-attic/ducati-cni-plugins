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
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/executor"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/ip"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/links"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/namespace"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/nl" //only only on linux - ignore error
	"github.com/vishvananda/netlink"
)

const vni = 1

type NetConf struct {
	types.NetConf
	Network     string `json:"network"` // IPNet
	HostNetwork string `json:"host_network"`
}

func init() {
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

func getSandboxRepo() (namespace.Repository, error) {
	sandboxRepoDir := os.Getenv("DUCATI_OS_SANDBOX_REPO")
	if sandboxRepoDir == "" {
		return nil, errors.New("DUCATI_OS_SANDBOX_REPO is required")
	}

	sandboxRepo, err := namespace.NewRepository(sandboxRepoDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox repository: %s", err)
	}

	return sandboxRepo, nil
}

func getSandboxNS(name string) (namespace.Namespace, error) {
	sandboxRepo, err := getSandboxRepo()
	if err != nil {
		return nil, err
	}

	sandboxNS, err := sandboxRepo.Get(name)
	if err != nil {
		sandboxNS, err = sandboxRepo.Create(name)
		if err != nil {
			panic(err)
		}
	}

	return sandboxNS, nil
}

func cmdAdd(args *skel.CmdArgs) error {
	netConf, err := loadConf(args.StdinData)
	if err != nil {
		return fmt.Errorf("loading config: %s", err)
	}

	if args.ContainerID == "" {
		return errors.New("CNI_CONTAINERID is required")
	}

	sandboxNS, err := getSandboxNS(fmt.Sprintf("vni-%d", vni))
	if err != nil {
		return fmt.Errorf("getting vxlan sandbox: %s", err)
	}

	// run the IPAM plugin and get back the config to apply
	ipamResult, err := ipam.ExecAdd(netConf.IPAM.Type, args.StdinData)
	if err != nil {
		return fmt.Errorf("executing IPAM plugin: %s", err)
	}

	if ipamResult.IP4 == nil {
		return errors.New("IPAM plugin returned with missing IPv4 config")
	}

	linkFactory := &links.Factory{Netlinker: nl.Netlink}
	addressManager := &ip.AddressManager{Netlinker: nl.Netlink}

	sandboxNamespaceFile, err := sandboxNS.Open()
	if err != nil {
		return fmt.Errorf("opening sandbox namespace: %s", err)
	}
	defer sandboxNamespaceFile.Close()

	executor := executor.Executor{
		ContainerNS:    namespace.NewNamespace(args.Netns),
		SandboxNS:      sandboxNS,
		LinkFactory:    linkFactory,
		Netlinker:      nl.Netlink,
		AddressManager: addressManager,
	}

	sandboxLink, err = executor.SetupContainerNS(args.ContainerID, args.IfName, ipamResult)
	if err != nil {
		panic(err)
	}

	vxlanName := fmt.Sprintf("vxlan%d", vni)

	var foundVxlanDevice bool
	err = sandboxNS.Execute(func(ns *os.File) error {
		if _, err := linkFactory.FindLink(vxlanName); err == nil {
			foundVxlanDevice = true
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed attempting to find vxlan device in sandbox: %s", err)
	}

	// create vxlan device within host namespace
	if foundVxlanDevice == false {
		vxlan, err := linkFactory.CreateVxlan(vxlanName, vni)
		if err != nil {
			return fmt.Errorf("creating vxlan device on host namespace: %s", err)
		}

		// move vxlan device to sandbox namespace
		err = nl.Netlink.LinkSetNsFd(vxlan, int(sandboxNamespaceFile.Fd()))
		if err != nil {
			return fmt.Errorf("moving vxland device into sandbox: %s", err)
		}
	}

	err = sandboxNS.Execute(func(ns *os.File) error {
		vxlan, err := linkFactory.FindLink(vxlanName)
		if err != nil {
			return fmt.Errorf("finding vxlan device within sandbox: %s", err)
		}

		err = nl.Netlink.LinkSetUp(vxlan)
		if err != nil {
			return fmt.Errorf("upping sandbox veth end: %s", err)
		}

		vxlan, err = linkFactory.FindLink(vxlanName)
		if err != nil {
			return fmt.Errorf("finding vxlan device within sandbox after upping: %s", err)
		}

		sandboxLink, err = nl.Netlink.LinkByName(sandboxLink.Attrs().Name)
		if err != nil {
			return fmt.Errorf("find sandbox veth end by name: %s", err)
		}

		err = nl.Netlink.LinkSetUp(sandboxLink)
		if err != nil {
			return fmt.Errorf("upping sandbox veth end: %s", err)
		}

		var bridge *netlink.Bridge
		bridgeName := fmt.Sprintf("vxlanbr%d", vni)
		link, err := linkFactory.FindLink(bridgeName)
		if err != nil {
			bridge, err = linkFactory.CreateBridge(bridgeName, &net.IPNet{
				IP:   ipamResult.IP4.Gateway,
				Mask: ipamResult.IP4.IP.Mask,
			})
			if err != nil {
				return fmt.Errorf("failed to create bridge: %s", err)
			}
		} else {
			bridge = link.(*netlink.Bridge)
		}

		err = nl.Netlink.LinkSetMaster(vxlan, bridge)
		if err != nil {
			return fmt.Errorf("slaving vxlan to bridge: %s", err)
		}

		err = nl.Netlink.LinkSetMaster(sandboxLink, bridge)
		if err != nil {
			return fmt.Errorf("slaving veth end to bridge: %s", err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("configuring sandbox namespace: %s", err)
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

	linkFactory := &links.Factory{Netlinker: nl.Netlink}
	containerNS := namespace.NewNamespace(args.Netns)

	err = containerNS.Execute(func(ns *os.File) error {
		linkFactory.DeleteLinkByName(args.IfName)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to delete link in container namespace: %s", err)
	}

	sandboxRepo, err := getSandboxRepo()
	if err != nil {
		return fmt.Errorf("failed to open sandbox repository: %s", err)
	}

	sandboxNS, err := sandboxRepo.Get(fmt.Sprintf("vni-%d", vni))
	if err != nil {
		return fmt.Errorf("failed to get sandbox namespace: %s", err)
	}

	var sandboxLinks []netlink.Link
	err = sandboxNS.Execute(func(ns *os.File) error {
		var err error
		sandboxLinks, err = linkFactory.ListLinks()
		return err
	})
	if err != nil {
		return fmt.Errorf("failed to get sandbox links: %s", err)
	}

	for _, link := range sandboxLinks {
		if _, ok := link.(*netlink.Veth); ok {
			return nil // we still have a container attached
		}
	}

	err = sandboxNS.Destroy()
	if err != nil {
		return fmt.Errorf("failed to destroy sandbox namespace: %s", err)
	}

	return nil
}

func main() {
	skel.PluginMain(cmdAdd, cmdDel)
}
