package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"

	"github.com/appc/cni/pkg/ipam"
	"github.com/appc/cni/pkg/skel"
	"github.com/appc/cni/pkg/types"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/overlay"
)

type NetConf struct {
	types.NetConf
	Network     string `json:"network"`
	HostNetwork string `json:"host_network"`
}

func init() {
	// this ensures that main runs only on main thread (thread group leader).
	// since namespace ops (unshare, setns) are done for a single thread, we
	// must ensure that the goroutine does not jump from OS thread to thread
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

	overlayController := &overlay.Controller{
		NetworkSandboxRepo: nil,
		NamespaceRepo:      &overlay.NamespaceRepository{},
	}
	err = overlayController.Add(args.Netns, args.IfName, vni, ipamResult)
	if err != nil {
		return fmt.Errorf("overlay controller failed: %s", err)
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
	skel.PluginMain(cmdAdd, cmdDel)
}
