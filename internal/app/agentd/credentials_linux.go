package agentd

import "os"

// LoadTransport reads only the fixed protected bootstrap mount. Kubernetes
// projected-volume symlinks are resolved once into a pinned generation directory.
// No environment or caller-selected file names are exposed by this entry point.
func LoadTransport() (*TransportBootstrap, error) { return loadTransport(BootstrapRoot) }
func loadTransport(directory string) (*TransportBootstrap, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, ErrConfig
	}
	defer root.Close()
	pinned := root
	if _, err = root.Lstat("..data"); err == nil {
		pinned, err = root.OpenRoot("..data")
		if err != nil {
			return nil, ErrConfig
		}
		defer pinned.Close()
	} else if !os.IsNotExist(err) {
		return nil, ErrConfig
	}
	files := map[string][]byte{}
	defer func() {
		for _, raw := range files {
			clear(raw)
		}
	}()
	for _, f := range bootstrapFiles {
		raw, err := readBootstrapFile(pinned, f.name, f.max)
		if err != nil {
			return nil, ErrConfig
		}
		files[f.name] = raw
	}
	return decodeTransport(files)
}
