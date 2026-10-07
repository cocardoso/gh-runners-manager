package proxmox_test

import (
	"context"
	"errors"
	"net/url"
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
