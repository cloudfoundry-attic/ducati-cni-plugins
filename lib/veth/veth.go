package veth

import (
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/vishvananda/netlink"
)

//go:generate counterfeiter --fake-name Netlinker . Netlinker
type Netlinker interface {
	LinkAdd(netlink.Link) error
	LinkSetUp(netlink.Link) error
	LinkByName(name string) (netlink.Link, error)
	LinkSetNsFd(hostVeth netlink.Link, fd int) error
}

//go:generate counterfeiter --fake-name Namespace . Namespace
type Namespace interface {
	Execute(func(*os.File) error) error
}

type Veth struct {
	Netlinker Netlinker
}

type Pair struct {
	Netlinker Netlinker
	Host      netlink.Link
	Container netlink.Link
}

func (v Veth) CreatePair(hostName, containerName string) (*Pair, error) {
	containerLink := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{
			Name:  containerName,
			Flags: net.FlagUp,
			MTU:   1450,
		},
		PeerName: hostName,
	}

	err := v.Netlinker.LinkAdd(containerLink)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("error adding link: %s", err))
	}

	hostLink, err := v.Netlinker.LinkByName(hostName)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("error finding link by name: %s", err))
	}

	pair := &Pair{
		Netlinker: v.Netlinker,
		Host:      hostLink,
		Container: containerLink,
	}

	return pair, nil
}

func (p *Pair) SetupContainer(namespace Namespace) error {
	return namespace.Execute(p.setupContainerInNamespace)
}

func (p *Pair) SetupHost(namespace Namespace) error {
	return namespace.Execute(p.setupHostInNamespace)
}

func (p *Pair) setupHostInNamespace(file *os.File) error {
	err := p.Netlinker.LinkSetNsFd(p.Host, int(file.Fd()))
	if err != nil {
		return errors.New(fmt.Sprintf("failed entering namespace: %s", err))
	}

	// TODO: refresh the link object

	if err := p.Netlinker.LinkSetUp(p.Host); err != nil {
		return errors.New(fmt.Sprintf("failed setting link UP: %s", err))
	}

	return nil
}

func (p *Pair) setupContainerInNamespace(file *os.File) error {
	if err := p.Netlinker.LinkSetUp(p.Container); err != nil {
		return errors.New(fmt.Sprintf("failed setting link UP: %s", err))
	}

	return nil
}
