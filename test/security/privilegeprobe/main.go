// privilegeprobe qualifies the running agentd image, never production readiness.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func restrictedStatus(raw string) bool {
	fields := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok {
			fields[k] = strings.Join(strings.Fields(v), " ")
		}
	}
	for _, key := range []string{"Uid", "Gid"} {
		if fields[key] != "65532 65532 65532 65532" {
			return false
		}
	}
	for _, key := range []string{"CapInh", "CapPrm", "CapEff", "CapBnd", "CapAmb"} {
		if fields[key] != "0000000000000000" {
			return false
		}
	}
	return fields["NoNewPrivs"] == "1" && fields["Seccomp"] == "2"
}

func run() error {
	fail := errors.New("agentd privilege boundary failed")
	// PID 1 is the real supervisor; the exec probe must inherit the same restrictions.
	for _, path := range []string{"/proc/1/status", "/proc/self/status"} {
		raw, err := os.ReadFile(path)
		if err != nil || !restrictedStatus(string(raw)) {
			return fail
		}
	}
	argv, err := os.ReadFile("/proc/1/cmdline")
	if err != nil || string(argv) != "/usr/local/bin/thinkpixel-agentd\x00" {
		return fail
	}
	if _, present := os.LookupEnv("KUBECONFIG"); present {
		return fail
	}
	for _, path := range []string{
		"/var/run/secrets/kubernetes.io/serviceaccount", "/home/nonroot/.kube/config", "/root/.kube/config",
		"/var/run/docker.sock", "/run/containerd/containerd.sock", "/run/k3s/containerd/containerd.sock",
		"/dev/kvm", "/dev/mem", "/dev/sda", "/dev/vda",
	} {
		// Inaccessible root home is acceptable; accessible credentials/devices are not.
		if _, err := os.Lstat(path); !os.IsNotExist(err) && !errors.Is(err, unix.EACCES) {
			return fail
		}
	}
	for _, path := range []string{"/", "/run/thinkpixel/bootstrap"} {
		var st unix.Statfs_t
		if unix.Statfs(path, &st) != nil || st.Flags&unix.ST_RDONLY == 0 {
			return fail
		}
	}
	if err := unix.Setresuid(0, 0, 0); !errors.Is(err, unix.EPERM) {
		return fail
	}
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_RAW, unix.IPPROTO_ICMP)
	if err == nil {
		_ = unix.Close(fd)
		return fail
	}
	if !errors.Is(err, unix.EPERM) && !errors.Is(err, unix.EACCES) {
		return fail
	}
	err = unix.Mount("none", "/tmp", "tmpfs", 0, "size=4096")
	if err == nil {
		_ = unix.Unmount("/tmp", 0)
		return fail
	}
	if !errors.Is(err, unix.EPERM) && !errors.Is(err, unix.EACCES) {
		return fail
	}
	return nil
}
func main() {
	if run() != nil {
		fmt.Fprintln(os.Stderr, "agentd privilege probe: failed")
		os.Exit(1)
	}
	fmt.Println("agentd privilege probe: process identity, capabilities, seccomp, no-new-privileges, read-only mounts, credential/device absence and syscall denials passed")
}
