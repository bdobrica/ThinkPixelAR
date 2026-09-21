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
	f, err := root.OpenFile("config.json", os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return Config{}, ErrConfig
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > MaxConfigBytes || stat.Mode().Perm()&0222 != 0 {
		return Config{}, ErrConfig
	}
	var fs unix.Statfs_t
	if unix.Fstatfs(int(f.Fd()), &fs) != nil || fs.Flags&unix.ST_RDONLY == 0 {
		return Config{}, ErrConfig
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
	if err != nil {
		return Config{}, ErrConfig
	}
	return DecodeConfig(raw)
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
