package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"

	"github.com/appc/cni/pkg/skel"
	"github.com/appc/cni/pkg/types"
	"github.com/cloudfoundry-incubator/ducati-daemon/client"
	"github.com/cloudfoundry-incubator/ducati-daemon/container"
	"github.com/cloudfoundry-incubator/ducati-daemon/executor"
	"github.com/cloudfoundry-incubator/ducati-daemon/lib/ip"
	"github.com/cloudfoundry-incubator/ducati-daemon/lib/links"
	"github.com/cloudfoundry-incubator/ducati-daemon/lib/namespace"
	"github.com/cloudfoundry-incubator/ducati-daemon/lib/nl"
	"github.com/vishvananda/netlink"
)

const vni = 1

type NetConf struct {
	types.NetConf
	NetworkID string `json:"network_id"`
}

func init() {
	runtime.LockOSThread()
}

func loadConf(bytes []byte) (*NetConf, error) {
	n := &NetConf{}

	if err := json.Unmarshal(bytes, n); err != nil {
		return nil, fmt.Errorf("failed to load netconf: %v", err)
	}

	if n.NetworkID == "" {
		return nil, fmt.Errorf(`"network_id" field is required. It identifies the network.`)
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

func getHostIP() (string, error) {
	routes, err := nl.Netlink.RouteList(nil, nl.FAMILY_V4)
	if err != nil {
		return "", fmt.Errorf("route list failed: %s", err)
	}

	var ifaceName string
	for _, r := range routes {
		link, err := nl.Netlink.LinkByIndex(r.LinkIndex)
		if err != nil {
			return "", fmt.Errorf("link by index failed: %s", err)
		}
		if r.Dst == nil && r.Src == nil {
			ifaceName = link.Attrs().Name
		}
	}

	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return "", fmt.Errorf("error getting interface: %s", err)
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return "", fmt.Errorf("error getting addrs: %s", err)
	}

	if len(addrs) == 0 {
		return "", fmt.Errorf("no addrs found for interface: %s", ifaceName)
	}

	return addrs[0].String(), nil
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

	addressManager := &ip.AddressManager{Netlinker: nl.Netlink}
	routeManager := &ip.RouteManager{Netlinker: nl.Netlink}
	linkFactory := &links.Factory{Netlinker: nl.Netlink}
	daemonClient := client.New(daemonBaseURL, http.DefaultClient)
	executor := executor.New(addressManager, routeManager, linkFactory)

	creator := container.Creator{
		LinkFinder: linkFactory,
		Executor:   executor,
	}

	ipamResult, err := daemonClient.AllocateIP(netConf.NetworkID, args.ContainerID)
	if err != nil {
		return err
	}

	container, err := creator.Setup(container.CreatorConfig{
		BridgeName:      fmt.Sprintf("vxlanbr%d", vni),
		SandboxNsPath:   sandboxNS.Path(),
		ContainerNsPath: args.Netns,
		ContainerID:     args.ContainerID,
		InterfaceName:   args.IfName,
		VNI:             vni,
		IPAMResult:      ipamResult,
	})
	if err != nil {
		return err
	}

	container.HostIP, err = getHostIP()
	if err != nil {
		return fmt.Errorf("getting host IP:", err)
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

	daemonClient := client.New(daemonBaseURL, http.DefaultClient)

	err := daemonClient.RemoveContainer(args.ContainerID)
	if err != nil {
		return fmt.Errorf("removing container data to store: %s", err)
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
