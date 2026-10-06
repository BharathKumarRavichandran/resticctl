package securefile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMakePrivateDirProtectsExistingParents(t *testing.T) {
	root := filepath.Join(t.TempDir(), "status")
	directory := filepath.Join(root, "profiles", "home", "history")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := MakePrivateDir(root, directory); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	for path := directory; ; path = filepath.Dir(path) {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode=%o", path, info.Mode().Perm())
		}
		if path == root {
			break
		}
	}
}

func TestMakePrivateDirRejectsEscapeAndSymlinks(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "state")
	outside := filepath.Join(parent, "outside")
	if err := MakePrivateDir(root, outside); err == nil {
		t.Fatal("accepted outside directory")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "profiles")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := MakePrivateDir(root, filepath.Join(link, "home")); err == nil {
		t.Fatal("followed symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "home")); !os.IsNotExist(err) {
		t.Fatalf("modified outside directory: %v", err)
	}
}
