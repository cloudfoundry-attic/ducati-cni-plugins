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
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/executor"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/ip"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/links"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/namespace"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/nl" //only only on linux - ignore error
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/ns" //only only on linux - ignore error
	"github.com/cloudfoundry-incubator/ducati-daemon/client"
	"github.com/cloudfoundry-incubator/ducati-daemon/models"
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
	daemonBaseURL := os.Getenv("DAEMON_BASE_URL")
	if daemonBaseURL == "" {
		return fmt.Errorf("missing required env var 'DAEMON_BASE_URL'")
	}

	if args.ContainerID == "" {
		return errors.New("CNI_CONTAINERID is required")
	}

	netConf, err := loadConf(args.StdinData)
	if err != nil {
		return fmt.Errorf("loading config: %s", err)
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

	containerNS := namespace.NewNamespace(args.Netns)

	executor := executor.Executor{
		NetworkNamespacer: ns.LinuxNamespacer,
		LinkFactory:       linkFactory,
		Netlinker:         nl.Netlink,
		AddressManager:    addressManager,
	}

	sandboxLink, err := executor.SetupContainerNS(sandboxNS.Path(), containerNS.Path(), args.ContainerID, args.IfName, ipamResult)
	if err != nil {
		return err
	}

	bridgeName := fmt.Sprintf("vxlanbr%d", vni)
	vxlanName, err := executor.EnsureVxlanDeviceExists(vni, sandboxNS)
	if err != nil {
		return err
	}

	err = executor.SetupSandboxNS(
		vxlanName, bridgeName,
		sandboxNS,
		sandboxLink,
		ipamResult)
	if err != nil {
		return fmt.Errorf("configuring sandbox namespace: %s", err)
	}

	daemonClient := client.New(daemonBaseURL)

	container := models.Container{
		ID: args.ContainerID,
	}

	err = daemonClient.SaveContainer(container)
	if err != nil {
		return fmt.Errorf("saving container data to store: %s", err)
	}

	return ipamResult.Print()
}

func cmdDel(args *skel.CmdArgs) error {
	daemonBaseURL := os.Getenv("DAEMON_BASE_URL")
	if daemonBaseURL == "" {
		return fmt.Errorf("missing required env var 'DAEMON_BASE_URL'")
	}

	n, err := loadConf(args.StdinData)
	if err != nil {
		return err
	}

	daemonClient := client.New(daemonBaseURL)

	err = daemonClient.RemoveContainer(args.ContainerID)
	if err != nil {
		return fmt.Errorf("removing container data to store: %s", err)
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
