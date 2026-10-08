package proxmox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"
)

type dialTimeout struct{}

func (dialTimeout) Error() string   { return "i/o timeout" }
func (dialTimeout) Timeout() bool   { return true }
func (dialTimeout) Temporary() bool { return true }

func TestRetryableRead(t *testing.T) {
	api := func(code int, body string) error {
		return &APIError{Method: "GET", Path: "/x", StatusCode: code, Body: body}
	}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"command socket reset", api(500, "failed to read from command socket: Connection reset by peer"), true},
		{"proxy reload", api(596, "Connection timed out"), true},
		{"bad gateway", api(502, ""), true},
		{"missing guest", api(500, "Configuration file 'nodes/pve/lxc/900.conf' does not exist"), false},
		{"forbidden", api(403, ""), false},
		{"not implemented", api(501, ""), false},
		{"connection reset", fmt.Errorf("proxmox GET /x: %w", &net.OpError{Op: "read", Err: syscall.ECONNRESET}), true},
		{"server closed the connection", fmt.Errorf("proxmox GET /x: %w", io.ErrUnexpectedEOF), true},
		{"dial timeout: the host drops packets", fmt.Errorf("proxmox GET /x: %w", &net.OpError{Op: "dial", Err: dialTimeout{}}), false},
		{"client timeout", fmt.Errorf("proxmox GET /x: %w", context.DeadlineExceeded), false},
		{"canceled", context.Canceled, false},
		{"decode after a 2xx", errors.New("proxmox GET /x: decode: invalid character"), false},
	}
	for _, c := range cases {
		if got := retryableRead(c.err); got != c.want {
			t.Errorf("%s: retryableRead = %v, want %v", c.name, got, c.want)
		}
	}
}
