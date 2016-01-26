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
		return err
	}

	if args.ContainerID == "" {
		return errors.New("CNI_CONTAINERID is required")
	}

	sandboxNS, err := getSandboxNS(fmt.Sprintf("vni-%d", vni))
	if err != nil {
		return err
	}

	// run the IPAM plugin and get back the config to apply
	ipamResult, err := ipam.ExecAdd(netConf.IPAM.Type, args.StdinData)
	if err != nil {
		return err
	}

	if ipamResult.IP4 == nil {
		return errors.New("IPAM plugin returned with missing IPv4 config")
	}

	linkFactory := &links.Factory{Netlinker: nl.Netlink}
	addressManager := &ip.AddressManager{Netlinker: nl.Netlink}

	containerNS := namespace.NewNamespace(args.Netns)

	containerNamespaceFile, err := containerNS.Open()
	if err != nil {
		return err
	}
	defer containerNamespaceFile.Close()

	sandboxNamespaceFile, err := sandboxNS.Open()
	if err != nil {
		return err //cannot be tested
	}
	defer sandboxNamespaceFile.Close()

	var sandboxLink netlink.Link
	err = containerNS.Execute(func(ns *os.File) error {
		var (
			containerLink netlink.Link
			err           error
		)

		hostLinkName := args.ContainerID
		sandboxLink, containerLink, err = linkFactory.CreateVethPair(hostLinkName, args.IfName, links.VxlanVethMTU)
		if err != nil {
			return fmt.Errorf("could not create veth pair: %s", err)
		}

		err = nl.Netlink.LinkSetNsFd(sandboxLink, int(sandboxNamespaceFile.Fd()))
		if err != nil {
			return err // cannot be tested
		}

		err = addressManager.AddAddress(containerLink, ipamResult.IP4.IP.IP)
		if err != nil {
			return err // cannot be tested
		}

		err = nl.Netlink.LinkSetUp(containerLink)
		if err != nil {
			return err // cannot be tested
		}

		return nil
	})
	if err != nil {
		return err
	}

	err = sandboxNS.Execute(func(ns *os.File) error {
		sandboxLink, err = nl.Netlink.LinkByName(sandboxLink.Attrs().Name)
		if err != nil {
			return err // cannot be tested
		}

		err = nl.Netlink.LinkSetUp(sandboxLink)
		if err != nil {
			return err // cannot be tested
		}

		vxlanName := fmt.Sprintf("vxlan%d", vni)
		vxlan, err := linkFactory.FindLink(vxlanName)
		if err != nil {
			vxlan, err = linkFactory.CreateVxlan(vxlanName, vni)
			if err != nil {
				return err // cannot be tested
			}
		}

		var bridge *netlink.Bridge
		bridgeName := fmt.Sprintf("vxlanbr%d", vni)
		link, err := linkFactory.FindLink(bridgeName)
		if err != nil {
			bridge, err = linkFactory.CreateBridge(bridgeName, ipamResult.IP4.Gateway)
			if err != nil {
				return fmt.Errorf("failed to create bridge: %s", err)
			}
		} else {
			bridge = link.(*netlink.Bridge)
		}

		err = nl.Netlink.LinkSetMaster(vxlan, bridge)
		if err != nil {
			return err // cannot be tested
		}

		err = nl.Netlink.LinkSetMaster(sandboxLink, bridge)
		if err != nil {
			return err // cannot be tested
		}

		return nil
	})
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
