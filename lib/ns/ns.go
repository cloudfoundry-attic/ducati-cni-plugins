package ns

import "github.com/vishvananda/netns"

//go:generate counterfeiter --fake-name NetworkNamespacer . NetworkNamespacer
type NetworkNamespacer interface {
	GetFromPath(string) (netns.NsHandle, error)
	Set(netns.NsHandle) error
}
