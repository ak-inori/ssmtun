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

func TestMySQLGreeting(t *testing.T) {
	port := serve(t, func(c net.Conn) {
		c.Write([]byte{0x4a, 0, 0, 0, 10, '8', '.', '0', 0})
		time.Sleep(100 * time.Millisecond)
		c.Close()
	})
	if err := Run(context.Background(), config.EngineMySQL, port, time.Second); err != nil {
		t.Fatalf("expected healthy, got %v", err)
	}
}

func TestDialRefused(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	if err := Run(context.Background(), config.EngineTCP, port, time.Second); err == nil {
		t.Fatal("expected dial error")
	}
}
