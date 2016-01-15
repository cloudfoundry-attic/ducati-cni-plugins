package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/appc/cni/pkg/ipam"
	"github.com/appc/cni/pkg/skel"
	"github.com/appc/cni/pkg/types"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/nl" //only only on linux - ignore error
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/overlay"
	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/veth"
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

	pair, err := v.CreatePair("host-name", "container-name")
	if err != nil {
		panic(err)
	}

	containerNS := bogusNamespace{path: args.Netns}
	hostNS := bogusNamespace{path: "/proc/self/ns/net"}

	err = pair.SetupContainer(containerNS)
	if err != nil {
		panic(err)
	}

	err = pair.SetupHost(hostNS)
	if err != nil {
		panic(err)
	}

	// TODO: implement sandbox that takes netlinker as well

	//sanbox := sandbox.Sandbox{
	//NetLinker:   nl.Netlink,
	//VNI:         1,
	//HostNetwork: netConf.HostNetwork,
	//Network:     netConf.Network,
	//}

	//Sandbox, err := sandbox.Create()

	//err := sandbox.Add(pair.Host)

	return ipamResult.Print()
}

type bogusNamespace struct {
	path string
}

func (bn bogusNamespace) Execute(callback func(file *os.File) error) error {
	file, err := os.Open(bn.path)
	if err != nil {
		panic(err)
	}
	defer file.Close()

	return callback(file)
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
