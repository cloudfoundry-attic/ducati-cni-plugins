package veth

import (
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/cloudfoundry-incubator/ducati-cni-plugins/lib/nl"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

//go:generate counterfeiter --fake-name Namespace . Namespace
type Namespace interface {
	Execute(func(*os.File) error) error
	FilePath() string
}

type Veth struct {
	Netlinker nl.Netlinker
}

type Pair struct {
	Netlinker      nl.Netlinker
	Host           netlink.Link
	Container      netlink.Link
	ContainerIPNet net.IPNet
}

func (v Veth) CreatePair(hostName, containerName string, containerIPNet net.IPNet) (*Pair, error) {
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
		Netlinker:      v.Netlinker,
		Host:           hostLink,
		Container:      containerLink,
		ContainerIPNet: containerIPNet,
	}

	return pair, nil
}

func (p *Pair) SetupContainer(namespace Namespace) error {
	return p.setupContainerInNamespace(namespace)
}

func (p *Pair) SetupHost(namespace Namespace) error {
	return p.setupHostInNamespace(namespace)
}

func (p *Pair) setupHostInNamespace(namespace Namespace) error {
	file, err := netns.GetFromPath(namespace.FilePath())
	if err != nil {
		panic(err)
	}

	linkName := p.Host.Attrs().Name

	err = p.Netlinker.LinkSetNsFd(p.Host, int(file))
	if err != nil {
		return errors.New(fmt.Sprintf("failed entering namespace: %s", err))
	}

	return namespace.Execute(func(*os.File) error {
		p.Host, err = p.Netlinker.LinkByName(linkName)
		if err != nil {
			return fmt.Errorf("failed finding host link after Ns set: %s", err)
		}

		if err := p.Netlinker.LinkSetUp(p.Host); err != nil {
			return errors.New(fmt.Sprintf("failed setting link UP: %s", err))
		}
		return nil
	})

	return nil
}

func (p *Pair) setupContainerInNamespace(namespace Namespace) error {
	file, err := netns.GetFromPath(namespace.FilePath())
	if err != nil {
		panic(err)
	}

	linkName := p.Container.Attrs().Name

	err = p.Netlinker.LinkSetNsFd(p.Container, int(file))
	if err != nil {
		return errors.New(fmt.Sprintf("failed entering namespace: %s", err))
	}

	return namespace.Execute(func(*os.File) error {
		p.Container, err = p.Netlinker.LinkByName(linkName)
		if err != nil {
			return fmt.Errorf("failed finding host link after Ns set: %s", err)
		}

		err = p.Netlinker.AddrAdd(p.Container, &netlink.Addr{
			IPNet: &p.ContainerIPNet,
		})
		if err != nil {
			return fmt.Errorf("failed adding address to container veth: %s", err)
		}

		if err = p.Netlinker.LinkSetUp(p.Container); err != nil {
			return errors.New(fmt.Sprintf("failed setting link UP: %s", err))
		}

		return nil
	})
}
