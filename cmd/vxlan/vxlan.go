package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime"

	"github.com/appc/cni/pkg/skel"
	"github.com/appc/cni/pkg/types"
	"github.com/cloudfoundry-incubator/ducati-daemon/client"
	"github.com/cloudfoundry-incubator/ducati-daemon/lib/nl"
	"github.com/cloudfoundry-incubator/ducati-daemon/models"
)

const vni = 1

type NetConf struct {
	types.NetConf
	NetworkID     string `json:"network_id"`
	DaemonBaseURL string `json:"daemon_base_url"`
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
	if args.ContainerID == "" {
		return errors.New("CNI_CONTAINERID is required")
	}

	netConf, err := loadConf(args.StdinData)
	if err != nil {
		return fmt.Errorf("loading config: %s", err)
	}

	if netConf.DaemonBaseURL == "" {
		return fmt.Errorf(`"daemon_base_url" field required.`)
	}

	daemonClient := client.New(netConf.DaemonBaseURL, http.DefaultClient)

	ipamResult, err := daemonClient.AllocateIP(netConf.NetworkID, args.ContainerID)
	if err != nil {
		return err
	}

	hostip, err := getHostIP()
	if err != nil {
		return fmt.Errorf("getting host IP:", err)
	}

	err = daemonClient.ContainerUp(netConf.NetworkID, args.ContainerID, models.NetworksSetupContainerPayload{
		Args:               args.Args,
		ContainerNamespace: args.Netns,
		InterfaceName:      args.IfName,
		VNI:                vni,
		HostIP:             hostip,
		IPAM:               ipamResult,
	})
	if err != nil {
		return err
	}

	return ipamResult.Print()
}

func cmdDel(args *skel.CmdArgs) error {
	netConf, err := loadConf(args.StdinData)
	if err != nil {
		return fmt.Errorf("loading config: %s", err)
	}

	if netConf.DaemonBaseURL == "" {
		return fmt.Errorf(`"daemon_base_url" field required.`)
	}

	daemonClient := client.New(netConf.DaemonBaseURL, http.DefaultClient)

	err = daemonClient.RemoveContainer(args.ContainerID)
	if err != nil {
		return fmt.Errorf("removing container data to store: %s", err)
	}

	err = daemonClient.ContainerDown(netConf.NetworkID, args.ContainerID, models.NetworksDeleteContainerPayload{
		ContainerNamespace: args.Netns,
		InterfaceName:      args.IfName,
		VNI:                vni,
	})
	if err != nil {
		return err
	}

	return nil
}

func main() {
	skel.PluginMain(cmdAdd, cmdDel)
}
