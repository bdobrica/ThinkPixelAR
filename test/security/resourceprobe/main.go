// resourceprobe performs bounded hostile checks in disposable qualification Pods.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		panic("expected cpu, pids, memory, or disk")
	}
	switch os.Args[1] {
	case "pids":
		var limit unix.Rlimit
		if err := unix.Getrlimit(unix.RLIMIT_NPROC, &limit); err != nil {
			panic(err)
		}
		raised := unix.Setrlimit(unix.RLIMIT_NPROC, &unix.Rlimit{Cur: 256, Max: 256})
		children := []*exec.Cmd{}
		defer func() {
			for _, child := range children {
				_ = child.Process.Kill()
				_ = child.Wait()
			}
		}()
		rejected := false
		for range 150 {
			child := exec.Command("/bin/sleep", "30")
			if err := child.Start(); err != nil {
				rejected = errors.Is(err, unix.EAGAIN)
				break
			}
			children = append(children, child)
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"soft": limit.Cur, "hard": limit.Max, "raise_denied": errors.Is(raised, unix.EPERM), "children_started": len(children), "fork_rejected": rejected})
		if limit.Max != 128 || raised == nil || !rejected {
			panic("process bound failed")
		}
	case "cpu":
		before, err := os.ReadFile("/sys/fs/cgroup/cpu.stat")
		if err != nil {
			panic(err)
		}
		var workers sync.WaitGroup
		for range 3 {
			workers.Go(func() {
				end := time.Now().Add(8 * time.Second)
				for time.Now().Before(end) {
				}
			})
		}
		workers.Wait()
		after, err := os.ReadFile("/sys/fs/cgroup/cpu.stat")
		if err != nil {
			panic(err)
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"before": string(before), "after": string(after)})
	case "memory":
		// Touch at most 640 MiB; the 512 MiB container must be OOM-killed first.
		blocks := [][]byte{}
		for range 640 {
			b := make([]byte, 1<<20)
			for i := range b {
				b[i] = 0x5a
			}
			blocks = append(blocks, b)
		}
		runtime.KeepAlive(blocks)
		panic("memory ceiling did not kill the probe")
	case "disk", "disk-bound":
		file, err := os.Create("/tmp/probe-fill")
		if err != nil {
			panic(err)
		}
		defer file.Close()
		block := make([]byte, 1<<20)
		for i := range block {
			block[i] = 0x5a
		}
		var written int
		for range 48 {
			n, err := file.Write(block)
			written += n
			if err != nil {
				fmt.Printf("bytes=%d error=%v\n", written, err)
				if os.Args[1] == "disk-bound" {
					if !errors.Is(err, unix.ENOSPC) || written > 32<<20 {
						panic("scratch ceiling failed")
					}
					other, e := os.Create("/tmp/probe-second")
					if e != nil {
						panic(e)
					}
					// Consume a possible final partial block, then require aggregate exhaustion.
					_, e = other.Write(make([]byte, 8192))
					_ = other.Close()
					if !errors.Is(e, unix.ENOSPC) {
						panic("second file bypassed aggregate bound")
					}
					if e = file.Close(); e != nil {
						panic(e)
					}
					if e = os.Remove("/tmp/probe-fill"); e != nil {
						panic(e)
					}
					if e = os.WriteFile("/tmp/probe-recovered", []byte("space reclaimed"), 0600); e != nil {
						panic(e)
					}
					fmt.Println("aggregate ENOSPC and capacity recovery passed")
				}
				return
			}
		}
		if err := file.Sync(); err != nil {
			panic(err)
		}
		if os.Args[1] == "disk-bound" {
			panic("scratch allowed writes above hard capacity")
		}
		fmt.Printf("bytes=%d awaiting kubelet eviction\n", written)
	default:
		panic("unsupported probe")
	}
}
