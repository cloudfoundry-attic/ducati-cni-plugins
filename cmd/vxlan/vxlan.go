package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/appc/cni/pkg/skel"
	"github.com/cloudfoundry-incubator/ducati-daemon/client"
)

func newServerClient(stdinBytes []byte) (*client.DaemonClient, error) {
	var stdinStruct struct {
		ServerURL string `json:"daemon_base_url"`
	}
	err := json.Unmarshal(stdinBytes, &stdinStruct)
	if err != nil {
		return nil, fmt.Errorf("failed to parse stdin as JSON: %s", err)
	}

	if stdinStruct.ServerURL == "" {
		return nil, errors.New(`"daemon_base_url" field required.`)
	}

	return client.New(stdinStruct.ServerURL, http.DefaultClient), nil
}

func cmdAdd(args *skel.CmdArgs) error {
	daemonClient, err := newServerClient(args.StdinData)
	if err != nil {
		return err
	}

	ipamResult, err := daemonClient.CNIAdd(args)
	if err != nil {
		return err
	}

	return ipamResult.Print()
}

func cmdDel(args *skel.CmdArgs) error {
	daemonClient, err := newServerClient(args.StdinData)
	if err != nil {
		return err
	}

	err = daemonClient.CNIDel(args)
	if err != nil {
		return err
	}

	return nil
}

func main() {
	skel.PluginMain(cmdAdd, cmdDel)
}
