package ns

import "github.com/vishvananda/netns"

type ns struct{}

var Namespacer = &ns{}

func (*ns) GetFromPath(path string) (netns.NsHandle, error) {
	return netns.GetFromPath(path)
}

func (*ns) Set(handle netns.NsHandle) error {
	return netns.Set(handle)
}
