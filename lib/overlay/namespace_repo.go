package overlay

import "os"

type NamespaceRepository struct{}

func (n NamespaceRepository) Find(path string) (*os.File, error) {
	return os.Open(path)
}
