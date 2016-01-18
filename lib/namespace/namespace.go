package namespace

import (
	"os"
	"path/filepath"

	"github.com/vishvananda/netns"
)

type Namespace struct {
	Path string
}

func (n *Namespace) Name() string {
	return filepath.Base(n.Path)
}

func (n *Namespace) FilePath() string {
	return n.Path
}

func (n *Namespace) Execute(callback func(file *os.File) error) error {
	originalHandle, err := netns.Get()
	if err != nil {
		return err
	}
	defer originalHandle.Close()

	nshandle, err := netns.GetFromPath(n.Path)
	if err != nil {
		return err
	}
	defer nshandle.Close()

	err = netns.Set(nshandle)
	if err != nil {
		return err
	}
	defer netns.Set(originalHandle)

	return callback(os.NewFile(uintptr(nshandle), ""))
}
