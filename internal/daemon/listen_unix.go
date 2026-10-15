//go:build !windows

package daemon

import (
	"fmt"
	"net"
	"os"
	"syscall"
)

// privateDir makes dir readable by this user only, and refuses one that
// isn't a real directory owned by this user.
func privateDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || (ok && int(st.Uid) != os.Getuid()) {
		return fmt.Errorf("%s must be a directory owned by you", dir)
	}
	if fi.Mode().Perm() != 0o700 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}

// listenPrivate creates the socket with mode 0600 from the start: the
// umask is tightened around net.Listen, so there is no moment when another
// user could connect.
func listenPrivate(path string) (net.Listener, error) {
	old := syscall.Umask(0o177)
	ln, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil { // belt and braces
		ln.Close()
		return nil, err
	}
	return ln, nil
}
