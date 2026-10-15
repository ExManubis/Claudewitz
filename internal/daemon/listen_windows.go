package daemon

import (
	"net"
	"os"
)

// privateDir: Windows has no Unix permission bits; the folder sits in the
// user's profile, which other users can't open by default.
func privateDir(dir string) error {
	_, err := os.Stat(dir)
	return err
}

func listenPrivate(path string) (net.Listener, error) {
	return net.Listen("unix", path)
}
