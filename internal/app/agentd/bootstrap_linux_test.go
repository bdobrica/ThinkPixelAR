package agentd

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRejectsWritableMountAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "config.json")
	if err := os.WriteFile(file, fixture(t), 0444); err != nil {
		t.Fatal(err)
	}
	if _, err := load(root); err != ErrConfig {
		t.Fatal("writable mount accepted")
	}
	linkroot := t.TempDir()
	if err := os.Symlink(file, filepath.Join(linkroot, "config.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := load(linkroot); err != ErrConfig {
		t.Fatal("escape accepted")
	}
}
func TestRejectKubeconfigEvenEmpty(t *testing.T) {
	t.Setenv("KUBECONFIG", "")
	if CheckCredentialExposure() != ErrConfig {
		t.Fatal("kubeconfig accepted")
	}
}

func TestLoadRejectsFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	if err := unix.Mkfifo(filepath.Join(root, "config.json"), 0444); err != nil {
		t.Fatal(err)
	}
	if _, err := load(root); err != ErrConfig {
		t.Fatal("nonregular bootstrap accepted")
	}
}
