package agentd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestTransportLoadRejectsUnsafeFilesystem(t *testing.T) {
	for _, which := range []string{"writable", "fifo", "projection-escape"} {
		t.Run(which, func(t *testing.T) {
			root := t.TempDir()
			switch which {
			case "writable":
				if err := os.WriteFile(filepath.Join(root, "config.json"), fixture(t), 0444); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := unix.Mkfifo(filepath.Join(root, "config.json"), 0444); err != nil {
					t.Fatal(err)
				}
			case "projection-escape":
				if err := os.Symlink(t.TempDir(), filepath.Join(root, "..data")); err != nil {
					t.Fatal(err)
				}
			}
			if b, err := loadTransport(root); err != ErrConfig || b != nil {
				t.Fatal("unsafe filesystem accepted")
			}
		})
	}
}

// Opt-in real read-only mount verification. The helper binary is a statically
// compiled test binary, not a production configuration override. No keys persist
// in source or container layers: fixtures live only in a temporary bind mount.
func TestTransportReadonlyContainer(t *testing.T) {
	if os.Getenv("THINKPIXEL_LOADER_HELPER") == "1" {
		b, err := loadTransport("/bootstrap")
		if err != nil {
			t.Fatal(err)
		}
		defer b.Destroy()
		if _, err = b.ClientConfig(denyFrame); err != nil {
			t.Fatal(err)
		}
		return
	}
	binary := os.Getenv("THINKPIXEL_LOADER_TEST_BINARY")
	if binary == "" {
		t.Skip("read-only container test not enabled")
	}
	image := os.Getenv("THINKPIXEL_LOADER_TEST_IMAGE")
	if image == "" {
		t.Fatal("explicit test image required")
	}
	for _, layout := range []string{"direct", "projected"} {
		t.Run(layout, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0755); err != nil {
				t.Fatal(err)
			}
			files := transportFixture(t)
			target := root
			if layout == "projected" {
				target = filepath.Join(root, "generation-one")
				if err := os.Mkdir(target, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("generation-one", filepath.Join(root, "..data")); err != nil {
					t.Fatal(err)
				}
			}
			for name, raw := range files {
				if err := os.WriteFile(filepath.Join(target, name), raw, 0444); err != nil {
					t.Fatal(err)
				}
				if layout == "projected" {
					if err := os.Symlink("..data/"+name, filepath.Join(root, name)); err != nil {
						t.Fatal(err)
					}
				}
			}
			// Public per-file symlinks can point at inconsistent data during updates;
			// the loader must read the pinned ..data directory instead.
			if layout == "projected" {
				if err := os.Remove(filepath.Join(root, "config.json")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("missing-generation/config.json", filepath.Join(root, "config.json")); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("docker", "run", "--rm", "--network=none", "--read-only", "--user=65532:65532", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--mount", "type=bind,src="+root+",dst=/bootstrap,readonly", "--mount", "type=bind,src="+binary+",dst=/loader.test,readonly", "--env", "THINKPIXEL_LOADER_HELPER=1", "--entrypoint", "/loader.test", image, "-test.run=^TestTransportReadonlyContainer$", "-test.v")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("read-only loader check: %v\n%s", err, out)
			}
		})
	}
}
