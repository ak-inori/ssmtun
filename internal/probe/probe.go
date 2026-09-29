// Package probe checks that a tunnel works end to end, not just that the
// local listener exists. Each probe also counts as client traffic for the
// SSM idle timer, so it doubles as a keepalive.
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

// Run dials 127.0.0.1:port and performs the engine-specific handshake.
func Run(ctx context.Context, engine config.Engine, port int, timeout time.Duration) error {
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

	switch engine {
	case config.EnginePostgres:
		return postgres(conn)
	case config.EngineMySQL:
		return mysql(conn)
	default:
		return nil
	}
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

// mysql waits for the server greeting, which MySQL sends unsolicited.
func mysql(conn net.Conn) error {
	header := make([]byte, 5)
	if _, err := io.ReadFull(conn, header); err != nil {
		return fmt.Errorf("read greeting: %w", err)
	}
	// header[4] is the protocol version (10) or 0xff for an error packet
	// such as "Host is not allowed", which still proves the server answered.
	if header[4] != 10 && header[4] != 0xff {
		return fmt.Errorf("unexpected greeting byte %#x", header[4])
	}
	return nil
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
