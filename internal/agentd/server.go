package agentd

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
)

type LocalServer struct {
	Server     *http.Server
	Listener   net.Listener
	socketPath string
}

func ListenUnix(ctx context.Context, socketPath string, handler http.Handler) (*LocalServer, error) {
	if err := prepareUnixSocketPath(socketPath); err != nil {
		return nil, err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(socketPath)
		return nil, err
	}
	server := &http.Server{Handler: handler}
	local := &LocalServer{Server: server, Listener: listener, socketPath: socketPath}
	go func() {
		<-ctx.Done()
		_ = local.Close()
	}()
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			_ = local.Close()
		}
	}()
	return local, nil
}

func prepareUnixSocketPath(socketPath string) error {
	parent := filepath.Dir(socketPath)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	info, err := os.Stat(parent)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("unix socket parent directory must not be accessible by group or others")
	}
	info, err = os.Lstat(socketPath)
	if err == nil {
		if info.Mode().Type() != os.ModeSocket {
			return errors.New("refusing to remove existing non-socket path")
		}
		return os.Remove(socketPath)
	}
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *LocalServer) Close() error {
	if s == nil {
		return nil
	}
	var err error
	if s.Server != nil {
		err = s.Server.Close()
	}
	if s.Listener != nil {
		if closeErr := s.Listener.Close(); err == nil && closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			err = closeErr
		}
	}
	if s.socketPath != "" {
		if removeErr := os.Remove(s.socketPath); err == nil && removeErr != nil && !os.IsNotExist(removeErr) {
			err = removeErr
		}
	}
	return err
}
