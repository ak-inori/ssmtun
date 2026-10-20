// Package probe checks that a tunnel works end to end, not just that the
// local listener exists. Each probe also counts as client traffic for the
// SSM idle timer, so it doubles as a keepalive.
//
// Only engines with a side-effect-free exchange are probed. Every connection
// to the local port makes the plugin open a TCP connection to the remote host,
// and MySQL counts each one that ends before authentication against the
// bastion's IP, blocking it after max_connect_errors. MySQL and plain TCP
// tunnels are therefore supervised by watching the plugin process alone.
package probe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/ak-inori/ssmtun/internal/config"
)

// Supported reports whether Run may be used for the engine.
func Supported(engine config.Engine) bool {
	return engine == config.EnginePostgres
}

// Run dials 127.0.0.1:port and performs the engine-specific handshake.
func Run(ctx context.Context, engine config.Engine, port int, timeout time.Duration) error {
	if !Supported(engine) {
		return fmt.Errorf("no side-effect-free probe for engine %q", engine)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)

	return postgres(conn)
}

// postgres sends a CancelRequest for a non-existent backend. The server
// processes it and closes the socket without logging anything, so a prompt
// EOF proves the RDS endpoint answered through the tunnel.
func postgres(conn net.Conn) error {
	// int32 length=16, int32 code=80877102, int32 pid=0, int32 key=0
	pkt := []byte{0, 0, 0, 16, 0x04, 0xd2, 0x16, 0x2e, 0, 0, 0, 0, 0, 0, 0, 0}
	if _, err := conn.Write(pkt); err != nil {
		return fmt.Errorf("write CancelRequest: %w", err)
	}
	buf := make([]byte, 1)
	n, err := conn.Read(buf)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("waiting for server close: %w", err)
	}
	// A real Postgres never replies to CancelRequest; bytes mean something else answered.
	return fmt.Errorf("unexpected %d byte(s) in reply to CancelRequest", n)
}

// PortInUse reports whether something already listens on 127.0.0.1:port.
func PortInUse(port int) bool {
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
	if err != nil {
		return true
	}
	_ = l.Close()
	return false
}
