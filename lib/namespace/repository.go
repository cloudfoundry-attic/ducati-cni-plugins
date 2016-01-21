package namespace

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

//go:generate counterfeiter --fake-name Repository . Repository
type Repository interface {
	Get(name string) (Namespace, error)
	Create(name string) (Namespace, error)
}

type repository struct {
	root string
}

func NewRepository(root string) (Repository, error) {
	err := os.MkdirAll(root, 0755)
	if err != nil {
		return nil, err
	}
	return &repository{
		root: root,
	}, nil
}

func (r *repository) Get(name string) (Namespace, error) {
	file, err := r.open(name)
	if err != nil {
		return nil, err
	}
	file.Close()

	return NewNamespace(file.Name()), nil
}

func (r *repository) Create(name string) (Namespace, error) {
	file, err := r.create(name)
	if err != nil {
		return nil, err
	}
	file.Close()

	err = exec.Command("ip", "netns", "add", name).Run()
	if err != nil {
		os.Remove(file.Name())
		return nil, err
	}

	netnsPath := filepath.Join("/var/run/netns", name)
	err = bindMountFile(netnsPath, file.Name())
	if err != nil {
		panic(err)
	}

	err = unlinkNetworkNamespace(netnsPath)
	if err != nil {
		panic(err)
	}

	return NewNamespace(file.Name()), nil
}

func (r *repository) open(name string) (*os.File, error) {
	return os.Open(filepath.Join(r.root, name))
}

func (r *repository) create(name string) (*os.File, error) {
	return os.OpenFile(filepath.Join(r.root, name), os.O_CREATE|os.O_EXCL, 0644)
}

func unlinkNetworkNamespace(path string) error {
	if err := syscall.Unmount(path, syscall.MNT_DETACH); err != nil {
		return err
	}
	return os.Remove(path)
}

func bindMountFile(src, dst string) error {
	// mount point has to be an existing file
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	f.Close()

	return syscall.Mount(src, dst, "none", syscall.MS_BIND, "")
}
