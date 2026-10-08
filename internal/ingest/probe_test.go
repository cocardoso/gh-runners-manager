package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

func TestProbeAddressIsTheNextPort(t *testing.T) {
	cases := map[string]string{
		"https://10.50.0.2:8443": "10.50.0.2:8444",
		"https://ingest.lan":     "ingest.lan:444",
		"https://[fd00::2]:9000": "[fd00::2]:9001",
	}
	for in, want := range cases {
		if got, err := ProbeAddress(in); err != nil || got != want {
			t.Errorf("ProbeAddress(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	if _, err := ProbeAddress("https://h:65535"); err == nil {
		t.Error("port 65535 has no next port")
	}
}

func TestProbeListenerAcceptsAndCloses(t *testing.T) {
	ln, err := ListenProbe("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { ServeProbe(ctx, ln, slog.New(slog.NewTextHandler(io.Discard, nil))); close(done) }()
	conn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("dial the probe = %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if n, _ := conn.Read(make([]byte, 1)); n != 0 {
		t.Fatal("the probe must send nothing")
	}
	_ = conn.Close()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ServeProbe did not stop with its context")
	}
}

type failingListener struct {
	net.Listener
	accepts int
}

func (f *failingListener) Accept() (net.Conn, error) {
	f.accepts++
	return nil, errors.New("accept: too many open files")
}
func (f *failingListener) Close() error { return nil }

func TestProbeBacksOffWhenAcceptFails(t *testing.T) {
	ln := &failingListener{}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	ServeProbe(ctx, ln, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if ln.accepts > 10 {
		t.Fatalf("accepts in 100ms = %d, want a backoff", ln.accepts)
	}
}
