package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVolumesRoot(t *testing.T) {
	data := t.TempDir()
	docker := filepath.Join(t.TempDir(), "docker-volumes")
	legacy := filepath.Join(data, "volumes")
	// Linux reports a missing bind by naming Docker's folder; elsewhere
	// (Docker Desktop) there is no folder to name.
	fallback := ""
	if runtime.GOOS == "linux" {
		fallback = docker
	}

	if got := volumesRoot("", data, docker, dirExists); got != fallback {
		t.Errorf("nothing exists: %q, want %q", got, fallback)
	}
	if err := os.Mkdir(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := volumesRoot("", data, docker, dirExists); got != legacy {
		t.Errorf("only <data>/volumes exists: %q, want it kept for older installs", got)
	}
	if err := os.Mkdir(docker, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := volumesRoot("", data, docker, dirExists); got != docker {
		t.Errorf("Docker's folder exists: %q, want it", got)
	}
	if got := volumesRoot("/srv/volumes", data, docker, dirExists); got != "/srv/volumes" {
		t.Errorf("a set path: %q, want it to win", got)
	}
}

func TestVolumesPathFlagAndEnv(t *testing.T) {
	clearEnv(t)
	if c := parseTestArgs(t, []string{"--volumes-path", "/mnt/vols"}, "v"); c.VolumesRoot() != "/mnt/vols" {
		t.Errorf("--volumes-path: %q", c.VolumesRoot())
	}
	t.Setenv("EIGHTLY_VOLUMES_PATH", "/env/vols")
	if c := parseTestArgs(t, nil, "v"); c.VolumesRoot() != "/env/vols" {
		t.Errorf("EIGHTLY_VOLUMES_PATH: %q", c.VolumesRoot())
	}
}

// On a host Docker's folder is root-only. An agent running as another user
// must still pick it, and be told it cannot read it, rather than fall back to
// <data>/volumes, which is not where the volumes are.
func TestVolumesRoot_UnreadableDockerFolderStillWins(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs unix permissions and a non-root user")
	}
	locked := filepath.Join(t.TempDir(), "docker")
	if err := os.MkdirAll(filepath.Join(locked, "volumes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	data := t.TempDir()
	if err := os.Mkdir(filepath.Join(data, "volumes"), 0o700); err != nil {
		t.Fatal(err)
	}
	docker := filepath.Join(locked, "volumes")
	if got := volumesRoot("", data, docker, dirExists); got != docker {
		t.Errorf("got %q, want Docker's folder %q even though it cannot be read", got, docker)
	}
}
