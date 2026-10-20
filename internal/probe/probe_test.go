package probe

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/ak-inori/ssmtun/internal/config"
)

func serve(t *testing.T, handler func(net.Conn)) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go handler(c)
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func TestPostgresCancelRequestClosedByServer(t *testing.T) {
	port := serve(t, func(c net.Conn) {
		buf := make([]byte, 16)
		if _, err := c.Read(buf); err == nil && buf[4] == 0x04 && buf[5] == 0xd2 {
			c.Close()
			return
		}
		time.Sleep(time.Second)
		c.Close()
	})
	if err := Run(context.Background(), config.EnginePostgres, port, time.Second); err != nil {
		t.Fatalf("expected healthy, got %v", err)
	}
}

func TestPostgresNoAnswerIsUnhealthy(t *testing.T) {
	port := serve(t, func(c net.Conn) {
		time.Sleep(2 * time.Second)
		c.Close()
	})
	if err := Run(context.Background(), config.EnginePostgres, port, 200*time.Millisecond); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestMySQLAndTCPNeverConnect(t *testing.T) {
	accepted := make(chan struct{}, 1)
	port := serve(t, func(c net.Conn) {
		accepted <- struct{}{}
		c.Close()
	})
	for _, e := range []config.Engine{config.EngineMySQL, config.EngineTCP} {
		if Supported(e) {
			t.Errorf("%s must not be probed through the tunnel", e)
		}
		if err := Run(context.Background(), e, port, time.Second); err == nil {
			t.Errorf("%s: expected an error instead of a probe", e)
		}
	}
	select {
	case <-accepted:
		t.Fatal("a connection reached the listener")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestDialRefused(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	if err := Run(context.Background(), config.EnginePostgres, port, time.Second); err == nil {
		t.Fatal("expected dial error")
	}
}
