package proxmox_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/proxmox"
	"github.com/cocardoso/gh-runners-manager/internal/proxmox/proxmoxtest"
)

const (
	node    = "pve"
	tokenID = "ghrm@pve!ghrm"
	secret  = "s3cret"
)

func newClient(t *testing.T, srv *proxmoxtest.Server, tokenSecret string) *proxmox.Client {
	t.Helper()
	c, err := proxmox.New(proxmox.Config{URL: srv.URL, TokenID: tokenID, TokenSecret: tokenSecret, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	c.PollInterval = time.Millisecond
	c.RetryDelay = time.Millisecond
	return c
}

func setup(t *testing.T) (*proxmoxtest.Server, *proxmox.Client) {
	srv := proxmoxtest.NewServer(t, node, tokenID, secret)
	srv.AddGuest(proxmoxtest.Guest{VMID: 9000, Type: "lxc", Name: "ghrm-template", Template: true, Tags: "ghrm-template", Config: map[string]string{"memory": "4096"}})
	return srv, newClient(t, srv, secret)
}

func TestWrongTokenIsUnauthorized(t *testing.T) {
	srv, _ := setup(t)
	c := newClient(t, srv, "wrong")
	_, err := c.ListLXC(context.Background(), node)
	var apiErr *proxmox.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 401 {
		t.Fatalf("err = %v, want 401 APIError", err)
	}
}

func TestLXCLifecycle(t *testing.T) {
	srv, c := setup(t)
	ctx := context.Background()

	err := c.CloneLXC(ctx, node, 9000, 901, proxmox.CloneOptions{Hostname: "ghrm-a", Description: "env a", Pool: "ghrm"})
	if err != nil {
		t.Fatal(err)
	}
	g, ok := srv.Guest(901)
	if !ok || g.Name != "ghrm-a" || g.Config["pool"] != "ghrm" || g.Config["linked"] != "0" {
		t.Fatalf("clone = %+v (exists %v), want linked clone named ghrm-a in pool ghrm", g, ok)
	}

	if err := c.SetLXCConfig(ctx, node, 901, url.Values{"cores": {"2"}, "env": {"A=1\x00B=2"}}); err != nil {
		t.Fatal(err)
	}
	cfg, err := c.LXCConfig(ctx, node, 901)
	if err != nil || cfg["cores"] != "2" || cfg["env"] != "A=1\x00B=2" {
		t.Fatalf("config = %v, %v", cfg, err)
	}

	if err := c.StartLXC(ctx, node, 901); err != nil {
		t.Fatal(err)
	}
	st, err := c.LXCCurrentStatus(ctx, node, 901)
	if err != nil || st.Status != "running" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	ifaces, err := c.LXCInterfaces(ctx, node, 901)
	if err != nil || len(ifaces) != 2 || ifaces[1].Name != "eth0" {
		t.Fatalf("interfaces = %+v, %v", ifaces, err)
	}

	list, err := c.ListLXC(ctx, node)
	if err != nil || len(list) != 2 || list[0].VMID != 901 || list[1].Template != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if !list[1].HasTag("ghrm-template") || list[0].HasTag("ghrm-env") {
		t.Fatalf("tags = %q / %q", list[0].Tags, list[1].Tags)
	}

	if err := c.StopLXC(ctx, node, 901); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteLXC(ctx, node, 901); err != nil {
		t.Fatal(err)
	}
	if _, ok := srv.Guest(901); ok {
		t.Fatal("guest still exists after delete")
	}
}

func TestDeleteMissingIsNotFound(t *testing.T) {
	_, c := setup(t)
	err := c.DeleteLXC(context.Background(), node, 950)
	if !errors.Is(err, proxmox.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	_, err = c.LXCCurrentStatus(context.Background(), node, 950)
	if !errors.Is(err, proxmox.ErrNotFound) {
		t.Fatalf("status err = %v, want ErrNotFound", err)
	}
}

func TestFailedTaskReturnsLogTail(t *testing.T) {
	srv, c := setup(t)
	srv.AddGuest(proxmoxtest.Guest{VMID: 902, Type: "lxc"})
	srv.FailStart = true
	err := c.StartLXC(context.Background(), node, 902)
	if err == nil || !strings.Contains(err.Error(), "lxc-start") {
		t.Fatalf("err = %v, want task failure with log tail", err)
	}
}

func TestTaskWaitHonoursContext(t *testing.T) {
	_, c := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.WaitTask(ctx, node, "UPID:pve:1:x:1:root@pam:"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestVMIDAvailable(t *testing.T) {
	srv, c := setup(t)
	srv.AddGuest(proxmoxtest.Guest{VMID: 905, Type: "qemu"})
	ok, err := c.VMIDAvailable(context.Background(), 905)
	if err != nil || ok {
		t.Fatalf("VMIDAvailable(905) = %v, %v, want false", ok, err)
	}
	ok, err = c.VMIDAvailable(context.Background(), 906)
	if err != nil || !ok {
		t.Fatalf("VMIDAvailable(906) = %v, %v, want true", ok, err)
	}
}

func TestNodeStatusAndThinPools(t *testing.T) {
	srv, c := setup(t)
	srv.ThinPools = []proxmoxtest.ThinPool{{LV: "data", Size: 1000, Used: 400, MetadataSize: 100, MetadataUsed: 10}}
	ns, err := c.NodeStatus(context.Background(), node)
	if err != nil || ns.Memory.Total != 32<<30 || ns.Memory.Available != 24<<30 {
		t.Fatalf("node status = %+v, %v", ns, err)
	}
	pools, err := c.ThinPools(context.Background(), node)
	if err != nil || len(pools) != 1 || pools[0].LV != "data" || pools[0].Used != 400 || pools[0].MetadataUsed != 10 {
		t.Fatalf("thin pools = %+v, %v", pools, err)
	}
}

func TestNewRejectsBadURL(t *testing.T) {
	if _, err := proxmox.New(proxmox.Config{URL: "::not a url"}); err == nil {
		t.Fatal("want error for invalid URL")
	}
}

func TestListLXCAcceptsNumbersAsStrings(t *testing.T) {
	srv, c := setup(t)
	srv.NumbersAsStrings = true
	list, err := c.ListLXC(context.Background(), node)
	if err != nil || len(list) != 1 || list[0].VMID != 9000 || list[0].Template != 1 {
		t.Fatalf("list = %+v, %v; want vmid 9000 template 1 decoded from strings", list, err)
	}
}

func TestNotFoundMatchesOnlyMissingGuests(t *testing.T) {
	missing := &proxmox.APIError{StatusCode: 500, Status: "500 Configuration file 'nodes/pve/lxc/900.conf' does not exist"}
	if !errors.Is(missing, proxmox.ErrNotFound) {
		t.Error("a missing guest config must match ErrNotFound")
	}
	if !errors.Is(&proxmox.APIError{StatusCode: 404, Status: "404 Not Found"}, proxmox.ErrNotFound) {
		t.Error("404 must match ErrNotFound")
	}
	for _, msg := range []string{"500 storage 'fast' does not exist", "500 bridge 'vmbr9' does not exist"} {
		if errors.Is(&proxmox.APIError{StatusCode: 500, Status: msg}, proxmox.ErrNotFound) {
			t.Errorf("%q must not match ErrNotFound", msg)
		}
	}
}

func TestWaitTaskRetriesTransientErrors(t *testing.T) {
	srv, c := setup(t)
	srv.AddGuest(proxmoxtest.Guest{VMID: 903, Type: "lxc"})
	srv.TransientTaskErrors = 2
	if err := c.StartLXC(context.Background(), node, 903); err != nil {
		t.Fatalf("StartLXC with 2 transient poll errors = %v, want nil", err)
	}
	srv.TransientTaskErrors = 100
	if err := c.StopLXC(context.Background(), node, 903); err == nil {
		t.Fatal("StopLXC with persistent poll errors must fail")
	}
}

func TestReadsRetryTransientServerErrors(t *testing.T) {
	srv, c := setup(t)
	srv.TransientListErrors = 2
	if _, err := c.ListLXC(context.Background(), node); err != nil {
		t.Fatalf("ListLXC with 2 transient 500s = %v, want nil", err)
	}
	srv.TransientListErrors = 100
	_, err := c.ListLXC(context.Background(), node)
	var apiErr *proxmox.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 {
		t.Fatalf("ListLXC with persistent 500s = %v, want the 500", err)
	}
}

func TestWritesAreNotRetried(t *testing.T) {
	srv, c := setup(t)
	before := len(srv.Requests())
	err := c.SetLXCConfig(context.Background(), node, 4242, url.Values{"memory": {"1"}})
	if err == nil {
		t.Fatal("SetLXCConfig on a missing guest must fail")
	}
	if n := len(srv.Requests()) - before; n != 1 {
		t.Fatalf("a failed write was sent %d times, want 1", n)
	}
}

func fingerprint(srv *proxmoxtest.Server) string {
	sum := sha256.Sum256(srv.Certificate().Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

func TestTLSFingerprintPinning(t *testing.T) {
	srv, _ := setup(t)
	pinned, err := proxmox.New(proxmox.Config{URL: srv.URL, TokenID: tokenID, TokenSecret: secret, TLSFingerprint: fingerprint(srv)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pinned.ListLXC(context.Background(), node); err != nil {
		t.Fatalf("pinned client = %v, want success", err)
	}
	wrong, err := proxmox.New(proxmox.Config{URL: srv.URL, TokenID: tokenID, TokenSecret: secret, TLSFingerprint: strings.Repeat("AB:", 31) + "AB"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.ListLXC(context.Background(), node); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("wrong fingerprint = %v, want a fingerprint mismatch error", err)
	}
	if _, err := proxmox.New(proxmox.Config{URL: srv.URL, TLSFingerprint: "not-hex"}); err == nil {
		t.Fatal("want error for a malformed fingerprint")
	}
}

// A destroy that ends with warnings (e.g. a disk still in use) succeeds but can leave
// residue on the host, such as the disk or a DHCP reservation; it must not go unnoticed.
func TestTaskWarningsAreReported(t *testing.T) {
	srv, c := setup(t)
	srv.AddGuest(proxmoxtest.Guest{VMID: 903, Type: "lxc"})
	srv.DeleteWarning = "WARN: failed to delete mountpoint volume local-lvm:vm-903-disk-0: Logical volume pve/vm-903-disk-0 contains a filesystem in use."
	var got []string
	c.OnTaskWarnings = func(w proxmox.TaskWarnings) { got = append(got, w.Type, strconv.Itoa(w.VMID), w.Log) }
	if err := c.DeleteLXC(context.Background(), node, 903); err != nil {
		t.Fatalf("delete = %v, want success with warnings", err)
	}
	if len(got) != 3 || got[0] != "vzdestroy" || got[1] != "903" || !strings.Contains(got[2], "filesystem in use") {
		t.Fatalf("reported %q, want the vzdestroy warning for 903 with its log", got)
	}
}
