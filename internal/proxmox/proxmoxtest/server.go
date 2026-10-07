// Package proxmoxtest provides an in-process fake of the Proxmox VE API subset used by ghrm.
package proxmoxtest

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"sync"
	"testing"
)

// Guest is a fake LXC or QEMU guest.
type Guest struct {
	VMID     int
	Type     string // "lxc" or "qemu"
	Name     string
	Status   string // "running" or "stopped"
	Tags     string
	Template bool
	Config   map[string]string
}

// ThinPool mirrors /nodes/{node}/disks/lvmthin entries.
type ThinPool struct {
	LV           string `json:"lv"`
	Size         int64  `json:"lv_size"`
	Used         int64  `json:"used"`
	MetadataSize int64  `json:"metadata_size"`
	MetadataUsed int64  `json:"metadata_used"`
}

// Server is a fake Proxmox API. Tasks complete immediately.
type Server struct {
	*httptest.Server

	mu       sync.Mutex
	node     string
	auth     string
	guests   map[int]*Guest
	tasks    map[string]string // upid -> exit status
	requests []string

	// FailConfigPutOn makes PUT config fail when the form contains this key.
	FailConfigPutOn string
	FailStart       bool
	FailDelete      bool
	// PoolScoped emulates a token whose permissions come only from a resource pool:
	// only guests with a "pool" config entry are listed, and per-guest calls for
	// guests that do not exist answer 403, as real Proxmox does.
	PoolScoped bool
	// HoldTasks keeps every task "running" until it is set back to false.
	HoldTasks bool
	// NumbersAsStrings makes the LXC list encode vmid and template as JSON strings.
	NumbersAsStrings bool
	// RejectEnvOption emulates a Proxmox VE version without the LXC "env" option.
	RejectEnvOption bool
	// TransientTaskErrors makes the next n task status polls answer 596, as during a pveproxy reload.
	TransientTaskErrors int
	// StaleListStatus makes the LXC list report every guest as stopped, like the
	// pvestatd cache right after a start; status/current stays accurate.
	StaleListStatus bool
	// DenyLVMThin answers 403 on /disks/lvmthin, as for a token without Sys.Audit on "/".
	DenyLVMThin bool
	// StorageTotal and StorageUsed are reported by /nodes/{node}/storage/{storage}/status.
	StorageTotal int64
	StorageUsed  int64
	// PowerOffOnNextStop simulates a guest that powers itself off just before a stop request.
	PowerOffOnNextStop bool
	MemoryTotal     int64
	MemoryAvailable int64
	ThinPools       []ThinPool
}

// NewServer starts a fake server that accepts the given API token. It is closed when the test ends.
func NewServer(t testing.TB, node, tokenID, tokenSecret string) *Server {
	s := &Server{
		node:            node,
		auth:            "PVEAPIToken=" + tokenID + "=" + tokenSecret,
		guests:          map[int]*Guest{},
		tasks:           map[string]string{},
		MemoryTotal:     32 << 30,
		MemoryAvailable: 24 << 30,
	}
	mux := http.NewServeMux()
	p := "/api2/json"
	mux.HandleFunc("GET "+p+"/cluster/nextid", s.nextID)
	mux.HandleFunc("GET "+p+"/nodes/{node}/lxc", s.listLXC)
	mux.HandleFunc("POST "+p+"/nodes/{node}/lxc/{vmid}/clone", s.clone)
	mux.HandleFunc("GET "+p+"/nodes/{node}/lxc/{vmid}/config", s.getConfig)
	mux.HandleFunc("PUT "+p+"/nodes/{node}/lxc/{vmid}/config", s.putConfig)
	mux.HandleFunc("POST "+p+"/nodes/{node}/lxc/{vmid}/status/start", s.power("running"))
	mux.HandleFunc("POST "+p+"/nodes/{node}/lxc/{vmid}/status/stop", s.power("stopped"))
	mux.HandleFunc("GET "+p+"/nodes/{node}/lxc/{vmid}/status/current", s.currentStatus)
	mux.HandleFunc("GET "+p+"/nodes/{node}/lxc/{vmid}/interfaces", s.interfaces)
	mux.HandleFunc("DELETE "+p+"/nodes/{node}/lxc/{vmid}", s.deleteLXC)
	mux.HandleFunc("GET "+p+"/nodes/{node}/tasks/{upid}/status", s.taskStatus)
	mux.HandleFunc("GET "+p+"/nodes/{node}/tasks/{upid}/log", s.taskLog)
	mux.HandleFunc("GET "+p+"/nodes/{node}/status", s.nodeStatus)
	mux.HandleFunc("GET "+p+"/nodes/{node}/disks/lvmthin", s.lvmthin)
	mux.HandleFunc("GET "+p+"/nodes/{node}/storage/{storage}/status", s.storageStatus)
	s.Server = httptest.NewTLSServer(s.authenticate(mux))
	t.Cleanup(s.Close)
	return s
}

// AddGuest registers a guest.
func (s *Server) AddGuest(g Guest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g.Config == nil {
		g.Config = map[string]string{}
	}
	if g.Status == "" {
		g.Status = "stopped"
	}
	s.guests[g.VMID] = &g
}

// Guest returns a copy of a guest.
func (s *Server) Guest(vmid int) (Guest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guests[vmid]
	if !ok {
		return Guest{}, false
	}
	c := *g
	c.Config = maps.Clone(g.Config)
	return c, true
}

// SetHoldTasks sets HoldTasks under the server lock (safe while requests are in flight).
func (s *Server) SetHoldTasks(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.HoldTasks = v
}

// Requests returns "METHOD path" for every authenticated request, in order.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != s.auth {
			fail(w, http.StatusUnauthorized, "authentication failure")
			return
		}
		s.mu.Lock()
		s.requests = append(s.requests, r.Method+" "+r.URL.Path)
		s.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func data(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": nil, "message": msg})
}

// failParams answers like Proxmox's parameter validation: the reason is in the body only.
func failParams(w http.ResponseWriter, errs map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": nil, "errors": errs, "message": "Parameter verification failed."})
}

func (s *Server) guestOr404(w http.ResponseWriter, r *http.Request) (*Guest, bool) {
	vmid, _ := strconv.Atoi(r.PathValue("vmid"))
	g, ok := s.guests[vmid]
	switch {
	case !ok && s.PoolScoped:
		fail(w, http.StatusForbidden, fmt.Sprintf("Permission check failed (/vms/%d, VM.Audit)", vmid))
	case !ok:
		fail(w, http.StatusInternalServerError, fmt.Sprintf("Configuration file 'nodes/%s/lxc/%d.conf' does not exist", s.node, vmid))
	}
	return g, ok
}

func (s *Server) task(kind string, vmid int, exit string) string {
	upid := fmt.Sprintf("UPID:%s:%08d:%s:%d:root@pam:", s.node, len(s.tasks)+1, kind, vmid)
	s.tasks[upid] = exit
	return upid
}

func (s *Server) nextID(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vmid, _ := strconv.Atoi(r.URL.Query().Get("vmid"))
	if _, taken := s.guests[vmid]; taken {
		failParams(w, map[string]string{"vmid": fmt.Sprintf("VM %d already exists", vmid)})
		return
	}
	data(w, strconv.Itoa(vmid))
}

func (s *Server) listLXC(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []map[string]any{}
	ids := make([]int, 0, len(s.guests))
	for id := range s.guests {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		g := s.guests[id]
		if g.Type != "lxc" || (s.PoolScoped && g.Config["pool"] == "") {
			continue
		}
		status := g.Status
		if s.StaleListStatus {
			status = "stopped"
		}
		e := map[string]any{"vmid": g.VMID, "name": g.Name, "status": status, "tags": g.Tags}
		if g.Template {
			e["template"] = 1
		}
		if s.NumbersAsStrings {
			e["vmid"] = strconv.Itoa(g.VMID)
			if g.Template {
				e["template"] = "1"
			}
		}
		out = append(out, e)
	}
	data(w, out)
}

func (s *Server) clone(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.guestOr404(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	newID, _ := strconv.Atoi(r.PostForm.Get("newid"))
	if _, taken := s.guests[newID]; taken {
		fail(w, http.StatusInternalServerError, fmt.Sprintf("CT %d already exists", newID))
		return
	}
	g := &Guest{VMID: newID, Type: "lxc", Status: "stopped", Tags: src.Tags, Name: r.PostForm.Get("hostname"), Config: maps.Clone(src.Config)}
	for _, k := range []string{"hostname", "description", "pool"} {
		if v := r.PostForm.Get(k); v != "" {
			g.Config[k] = v
		}
	}
	g.Config["linked"] = r.PostForm.Get("full")
	s.guests[newID] = g
	data(w, s.task("vzclone", newID, "OK"))
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.guestOr404(w, r); ok {
		data(w, g.Config)
	}
}

func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guestOr404(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	if s.RejectEnvOption && r.PostForm.Has("env") {
		failParams(w, map[string]string{"env": "property is not defined in schema and the schema does not allow additional properties"})
		return
	}
	if s.FailConfigPutOn != "" && r.PostForm.Has(s.FailConfigPutOn) {
		fail(w, http.StatusInternalServerError, "config update failed")
		return
	}
	for k, v := range r.PostForm {
		g.Config[k] = v[0]
		if k == "tags" {
			g.Tags = v[0]
		}
	}
	data(w, nil)
}

func (s *Server) power(state string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		g, ok := s.guestOr404(w, r)
		if !ok {
			return
		}
		if state == "running" && s.FailStart {
			data(w, s.task("vzstart", g.VMID, "command 'lxc-start' failed: exit code 1"))
			return
		}
		if state == "stopped" && s.PowerOffOnNextStop {
			s.PowerOffOnNextStop = false
			g.Status = "stopped"
			fail(w, http.StatusInternalServerError, fmt.Sprintf("CT %d not running", g.VMID))
			return
		}
		if state == "stopped" && g.Status == "stopped" {
			fail(w, http.StatusInternalServerError, fmt.Sprintf("CT %d not running", g.VMID))
			return
		}
		g.Status = state
		data(w, s.task("vz"+state, g.VMID, "OK"))
	}
}

func (s *Server) currentStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.guestOr404(w, r); ok {
		mem, _ := strconv.ParseInt(g.Config["memory"], 10, 64)
		data(w, map[string]any{"status": g.Status, "mem": 256 << 20, "maxmem": mem << 20})
	}
}

func (s *Server) interfaces(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guestOr404(w, r)
	if !ok {
		return
	}
	if g.Status != "running" {
		data(w, []any{})
		return
	}
	data(w, []map[string]string{
		{"name": "lo", "inet": "127.0.0.1/8"},
		{"name": "eth0", "inet": fmt.Sprintf("10.50.0.%d/24", 100+g.VMID%100)},
	})
}

func (s *Server) deleteLXC(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guestOr404(w, r)
	if !ok {
		return
	}
	if g.Status == "running" {
		fail(w, http.StatusInternalServerError, fmt.Sprintf("CT %d is running - destroy failed", g.VMID))
		return
	}
	if s.FailDelete {
		fail(w, http.StatusInternalServerError, "can't lock file '/run/lock/lxc/pve-config-"+strconv.Itoa(g.VMID)+".lock' - got timeout")
		return
	}
	delete(s.guests, g.VMID)
	data(w, s.task("vzdestroy", g.VMID, "OK"))
}

func (s *Server) taskStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	exit, ok := s.tasks[r.PathValue("upid")]
	if !ok {
		fail(w, http.StatusInternalServerError, "no such task")
		return
	}
	if s.TransientTaskErrors > 0 {
		s.TransientTaskErrors--
		fail(w, 596, "Connection timed out")
		return
	}
	if s.HoldTasks {
		data(w, map[string]string{"status": "running"})
		return
	}
	data(w, map[string]string{"status": "stopped", "exitstatus": exit})
}

func (s *Server) taskLog(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	exit := s.tasks[r.PathValue("upid")]
	data(w, []map[string]any{{"n": 1, "t": "starting task"}, {"n": 2, "t": exit}})
}

func (s *Server) nodeStatus(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data(w, map[string]any{"memory": map[string]int64{
		"total": s.MemoryTotal, "available": s.MemoryAvailable, "free": s.MemoryAvailable / 2,
	}})
}

func (s *Server) lvmthin(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.DenyLVMThin {
		fail(w, http.StatusForbidden, "Permission check failed (/, Sys.Audit)")
		return
	}
	data(w, s.ThinPools)
}

func (s *Server) storageStatus(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data(w, map[string]any{"total": s.StorageTotal, "used": s.StorageUsed, "active": 1})
}
