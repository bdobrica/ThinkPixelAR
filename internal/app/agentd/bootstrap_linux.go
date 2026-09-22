package agentd

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// Load reads only the fixed bootstrap root. OpenRoot permits Kubernetes projected
// Secret symlinks inside that root but rejects escapes. Check the opened file's
// mount, not a pathname that can be replaced between stat and read.
func Load() (Config, error) { return load(BootstrapRoot) }
func load(directory string) (Config, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return Config{}, ErrConfig
	}
	defer root.Close()
	raw, err := readBootstrapFile(root, "config.json", MaxConfigBytes)
	if err != nil {
		return Config{}, ErrConfig
	}
	return DecodeConfig(raw)
}

func readBootstrapFile(root *os.Root, name string, max int) ([]byte, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrConfig
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > int64(max) || stat.Mode().Perm()&0222 != 0 {
		return nil, ErrConfig
	}
	var fs unix.Statfs_t
	if unix.Fstatfs(int(f.Fd()), &fs) != nil || fs.Flags&unix.ST_RDONLY == 0 {
		return nil, ErrConfig
	}
	raw, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil || len(raw) > max {
		clear(raw)
		return nil, ErrConfig
	}
	return raw, nil
}

// CheckCredentialExposure is a startup guard, not proof against arbitrary
// credential injection. Effective Pod verification remains an external boundary.
func CheckCredentialExposure() error {
	if _, present := os.LookupEnv("KUBECONFIG"); present {
		return ErrConfig
	}
	paths := []string{"/var/run/secrets/kubernetes.io/serviceaccount", "/home/nonroot/.kube/config"}
	if os.Geteuid() == 0 {
		paths = append(paths, "/root/.kube/config")
	}
	for _, p := range paths {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			return ErrConfig
		}
	}
	return nil
}
