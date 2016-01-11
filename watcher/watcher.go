package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
)

type peer struct {
	HostAddress      string
	ContainerAddress string
	LinkAddress      string
}

type peerDB map[string]peer

func main() {
	filename := os.Args[1]
	peers, err := loadPeers(filename)
	if err != nil {
		os.Exit(1)
	}

	for _, p := range peers {
		if err := addNeighbor(p.LinkAddress, p.ContainerAddress, "vxlan1"); err != nil {
			log.Fatal(err)
		}
		if err := addVTEP(p.LinkAddress, p.HostAddress, "vxlan1"); err != nil {
			log.Fatal(err)
		}
	}
}

func loadPeers(location string) (peerDB, error) {
	bytes, err := ioutil.ReadFile(location)
	if err != nil {
		if os.IsNotExist(err) {
			return peerDB{}, nil
		}
		return nil, err
	}

	n := peerDB{}
	if err := json.Unmarshal(bytes, &n); err != nil {
		return nil, fmt.Errorf("failed to load peer DB: %v", err)
	}

	return n, nil
}

func addNeighbor(mac, ip, dev string) error {
	return exec.Command(
		"ip", "neigh",
		"add", ip,
		"lladdr", mac,
		"nud", "permanent",
		"dev", dev,
	).Run()
}

func addVTEP(mac, ip, dev string) error {
	return exec.Command(
		"bridge",
		"fdb",
		"add", mac,
		"dev", dev,
		"dst", ip,
	).Run()
}
