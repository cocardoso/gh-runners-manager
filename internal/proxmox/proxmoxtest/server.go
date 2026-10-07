// Package proxmoxtest provides an in-process fake of the Proxmox VE API subset used by ghrm.
package proxmoxtest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
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
	MemoryTotal        int64
	MemoryAvailable    int64
	ThinPools          []ThinPool
	// FailCreate makes the next container creation task fail.
	FailCreate bool
	// FailUpload makes uploads answer 500 before reading the body.
	FailUpload bool
	// RejectCreateTags emulates Proxmox checking tag permissions on /vms/<vmid>, ignoring the
	// pool a new container joins: a pool-scoped token cannot create with tags.
	RejectCreateTags bool
	// TakeVMIDOnCreate makes the next container creation find its VMID taken by a foreign
	// guest (created at that moment) and answer synchronously with an error.
	TakeVMIDOnCreate bool

	purged    map[int]bool // VMIDs deleted with purge=1 (Proxmox also drops their ACLs)
	volumes   map[string]Volume
	firewalls map[int]*Firewall
}

// Volume is an uploaded storage volume.
type Volume struct {
	VolID   string
	Content string
	Size    int64
	SHA256  string
}

// Firewall is a guest's firewall state.
type Firewall struct {
	Enabled bool
	Groups  []string
}

// Volume returns an uploaded volume.
func (s *Server) Volume(volid string) (Volume, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.volumes[volid]
	return v, ok
}

// Volumes lists uploaded volumes.
func (s *Server) Volumes() []Volume {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Volume, 0, len(s.volumes))
	for _, v := range s.volumes {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VolID < out[j].VolID })
	return out
}

// Firewall returns a guest's firewall state.
func (s *Server) Firewall(vmid int) Firewall {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.firewalls[vmid]; ok {
		return *f
	}
	return Firewall{}
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
	mux.HandleFunc("POST "+p+"/nodes/{node}/storage/{storage}/upload", s.upload)
	mux.HandleFunc("GET "+p+"/nodes/{node}/storage/{storage}/content", s.content)
	mux.HandleFunc("DELETE "+p+"/nodes/{node}/storage/{storage}/content/{volid}", s.deleteVolume)
	mux.HandleFunc("POST "+p+"/nodes/{node}/lxc", s.createLXC)
	mux.HandleFunc("POST "+p+"/nodes/{node}/lxc/{vmid}/template", s.toTemplate)
	mux.HandleFunc("PUT "+p+"/nodes/{node}/lxc/{vmid}/firewall/options", s.firewallOptions)
	mux.HandleFunc("POST "+p+"/nodes/{node}/lxc/{vmid}/firewall/rules", s.firewallRule)
	mux.HandleFunc("PUT "+p+"/nodes/{node}/lxc/{vmid}/resize", s.resize)
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
	if src.Template && r.PostForm.Get("full") != "1" {
		g.Config["rootfs"] = fmt.Sprintf("local-lvm:base-%d-disk-0/vm-%d-disk-0,size=8G", src.VMID, newID)
	}
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
	if r.URL.Query().Get("purge") == "1" {
		if s.purged == nil {
			s.purged = map[int]bool{}
		}
		s.purged[g.VMID] = true
	}
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

// upload reads the multipart body as a stream, hashing the file part.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	if s.FailUpload {
		fail(w, http.StatusInternalServerError, "upload failed")
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	fields := map[string]string{}
	var name string
	var size int64
	h := sha256.New()
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		if part.FileName() != "" {
			name = part.FileName()
			size, _ = io.Copy(h, part)
			continue
		}
		b, _ := io.ReadAll(part)
		fields[part.FormName()] = string(b)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	s.mu.Lock()
	defer s.mu.Unlock()
	volid := r.PathValue("storage") + ":" + fields["content"] + "/" + name
	exit := "OK"
	if c := fields["checksum"]; c != "" && c != sum {
		exit = "checksum mismatch: got '" + sum + "' - expected '" + c + "'"
	} else {
		if s.volumes == nil {
			s.volumes = map[string]Volume{}
		}
		s.volumes[volid] = Volume{VolID: volid, Content: fields["content"], Size: size, SHA256: sum}
	}
	data(w, s.task("imgcopy", 0, exit))
}

func (s *Server) content(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []map[string]any{}
	for _, v := range s.volumes {
		if strings.HasPrefix(v.VolID, r.PathValue("storage")+":") && (r.URL.Query().Get("content") == "" || v.Content == r.URL.Query().Get("content")) {
			out = append(out, map[string]any{"volid": v.VolID, "size": v.Size, "content": v.Content})
		}
	}
	data(w, out)
}

func (s *Server) deleteVolume(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	volid := r.PathValue("volid")
	if _, ok := s.volumes[volid]; !ok {
		fail(w, http.StatusInternalServerError, "volume '"+volid+"' does not exist")
		return
	}
	delete(s.volumes, volid)
	data(w, s.task("imgdel", 0, "OK"))
}

func (s *Server) createLXC(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = r.ParseForm()
	vmid, _ := strconv.Atoi(r.PostForm.Get("vmid"))
	if s.RejectCreateTags && r.PostForm.Get("tags") != "" {
		fail(w, http.StatusForbidden, fmt.Sprintf("Permission check failed (/vms/%d, VM.Config.Options)", vmid))
		return
	}
	if s.TakeVMIDOnCreate {
		s.TakeVMIDOnCreate = false
		s.guests[vmid] = &Guest{VMID: vmid, Type: "lxc", Status: "stopped", Name: "foreign", Config: map[string]string{}}
	}
	if _, taken := s.guests[vmid]; taken {
		fail(w, http.StatusInternalServerError, fmt.Sprintf("CT %d already exists", vmid))
		return
	}
	if s.FailCreate {
		s.FailCreate = false
		data(w, s.task("vzcreate", vmid, "unable to create CT - extracting archive failed"))
		return
	}
	cfg := map[string]string{}
	for k, v := range r.PostForm {
		cfg[k] = v[0]
	}
	s.guests[vmid] = &Guest{VMID: vmid, Type: "lxc", Status: "stopped", Name: cfg["hostname"], Tags: cfg["tags"], Config: cfg}
	data(w, s.task("vzcreate", vmid, "OK"))
}

func (s *Server) toTemplate(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.guestOr404(w, r); ok {
		g.Template = true
		data(w, nil)
	}
}

func (s *Server) firewall(vmid int) *Firewall {
	if s.firewalls == nil {
		s.firewalls = map[int]*Firewall{}
	}
	if s.firewalls[vmid] == nil {
		s.firewalls[vmid] = &Firewall{}
	}
	return s.firewalls[vmid]
}

func (s *Server) firewallOptions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.guestOr404(w, r); ok {
		_ = r.ParseForm()
		s.firewall(g.VMID).Enabled = r.PostForm.Get("enable") == "1"
		data(w, nil)
	}
}

func (s *Server) firewallRule(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.guestOr404(w, r); ok {
		_ = r.ParseForm()
		if r.PostForm.Get("type") == "group" {
			f := s.firewall(g.VMID)
			f.Groups = append(f.Groups, r.PostForm.Get("action"))
		}
		data(w, nil)
	}
}

func (s *Server) resize(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.guestOr404(w, r); ok {
		_ = r.ParseForm()
		g.Config[r.PostForm.Get("disk")+"_size"] = r.PostForm.Get("size")
		data(w, s.task("resize", g.VMID, "OK"))
	}
}

// Purged reports whether a guest was deleted with purge=1, which also removes its ACLs.
func (s *Server) Purged(vmid int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.purged[vmid]
}
