package namespace

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/appc/cni/pkg/ns"
)

type Namespace struct {
	Path string
}

func (n *Namespace) Name() string {
	return filepath.Base(n.Path)
}

func (n *Namespace) Run(callback func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	return ns.WithNetNSPath(n.Path, false, func(_ *os.File) error {
		return callback()
	})
}
