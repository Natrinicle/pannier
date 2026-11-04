package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"pannier.dev/internal/config"
	"pannier.dev/internal/engine"
	"pannier.dev/internal/pipeline"
	"pannier.dev/internal/source"
)

type Server struct {
	mu       sync.Mutex
	Cfg      config.File
	CfgPath  string
	DataDir  string
	Password string
	Eng      engine.Engine
	Web      fs.FS
	Last     pipeline.Result
	LastErr  string
	LastAt   time.Time
	Running  bool
	Bags     []pipeline.Bag
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /v1/status", s.status)
	mux.HandleFunc("GET /v1/config", s.getConfig)
	mux.HandleFunc("PATCH /v1/config", s.patchConfig)
	mux.HandleFunc("GET /v1/parcels", s.parcels)
	mux.HandleFunc("GET /v1/bags", s.listBags)
	mux.HandleFunc("GET /v1/bags/{id}", s.getBag)
	mux.HandleFunc("POST /v1/restore", s.runRestore)
	mux.HandleFunc("GET /v1/depots", s.depots)
	mux.HandleFunc("PATCH /v1/depots", s.patchConfig)
	mux.HandleFunc("GET /v1/schedule", s.schedule)
	mux.HandleFunc("PATCH /v1/schedule", s.patchConfig)
	mux.HandleFunc("GET /v1/anomalies", s.anomalies)
	mux.HandleFunc("POST /v1/backup", s.runBackup)
	mux.HandleFunc("POST /v1/packcopy", s.runPackCopy)
	mux.HandleFunc("GET /", s.static)
	return s.auth(mux)
}

func ListenAddr(addr string) error {
	if addr == "" {
		return fmt.Errorf("empty listen address")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen %q: %w", addr, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		// Compose publishes 127.0.0.1:3477:3477 on the host. Docker DNAT
		// lands on the container's eth0, so the process must bind 0.0.0.0
		// *inside that namespace*. Host publish stays loopback. Bare metal
		// without this env still refuses a WAN bind.
		if containerBindAllowed() {
			return nil
		}
		return fmt.Errorf("refusing WAN bind %s; listen on 127.0.0.1 (or set PANNIER_CONTAINER_BIND=1 behind a loopback publish)", addr)
	}
	ip := net.ParseIP(host)
	if ip != nil && !ip.IsLoopback() {
		return fmt.Errorf("refusing non-loopback bind %s", addr)
	}
	if ip == nil && host != "localhost" && host != "127.0.0.1" {
		return fmt.Errorf("refusing non-loopback bind %s", addr)
	}
	return nil
}

func containerBindAllowed() bool {
	v := os.Getenv("PANNIER_CONTAINER_BIND")
	return v == "1" || strings.EqualFold(v, "true")
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("true"))
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Console HTML/CSS/JS must load without a token so the login
		// gate can render. /v1/* stays behind Bearer/Basic.
		if publicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if s.checkAuth(r) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="pannier"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

// publicPath is the loopback console plus boolean liveness. Do not
// widen this to /metrics or OpenAPI — those would identify the service.
func publicPath(p string) bool {
	switch p {
	case "/healthz", "/", "/index.html", "/style.css", "/app.js":
		return true
	default:
		return false
	}
}

func (s *Server) checkAuth(r *http.Request) bool {
	want := s.Password
	if want == "" {
		return false
	}
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		got := strings.TrimPrefix(h, "Bearer ")
		return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
	}
	_, pass, ok := r.BasicAuth()
	if ok {
		return subtle.ConstantTimeCompare([]byte(pass), []byte(want)) == 1
	}
	if q := r.URL.Query().Get("token"); q != "" {
		return subtle.ConstantTimeCompare([]byte(q), []byte(want)) == 1
	}
	return false
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]any{
		"engine":  s.Cfg.Engine,
		"listen":  s.Cfg.Listen,
		"running": s.Running,
		"lastErr": s.LastErr,
		"copy":    "Local bag first. Off-side bag gets the unique packs.",
	}
	if !s.LastAt.IsZero() {
		out["lastAt"] = s.LastAt
	}
	writeJSON(w, out)
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, config.Redact(s.Cfg))
}

func (s *Server) patchConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var patch config.File
	if err := json.Unmarshal(body, &patch); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Cfg = mergeFile(s.Cfg, patch, body)
	if err := s.Cfg.ValidateCell(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err := config.Write(s.CfgPath, s.Cfg); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, config.Redact(s.Cfg))
}

func (s *Server) parcels(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	cfg := s.Cfg
	sites := 1
	if len(cfg.Remotes) > 0 {
		sites = 2
	}
	var last *time.Time
	if !s.LastAt.IsZero() {
		t := s.LastAt
		last = &t
	}
	s.mu.Unlock()
	found := source.Pick(r.Context(), cfg.Selection)
	type parcel struct {
		ID, Kind, Name string
		Sites          int
		Last           *time.Time `json:",omitempty"`
	}
	cat := pipeline.CatalogWith(cfg, found)
	out := make([]parcel, 0, len(cat))
	for _, it := range cat {
		out = append(out, parcel{ID: it.ID, Kind: it.Kind, Name: it.Name, Sites: sites, Last: last})
	}
	writeJSON(w, out)
}

func (s *Server) listBags(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.Bags
	if out == nil {
		out = []pipeline.Bag{}
	}
	writeJSON(w, out)
}

func (s *Server) getBag(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.Bags {
		if b.ID == id {
			writeJSON(w, b)
			return
		}
	}
	http.Error(w, "bag not found", http.StatusNotFound)
}

type restoreReq struct {
	BagID  string   `json:"bagId"`
	Items  []string `json:"items"`
	Target string   `json:"target"`
}

type restoreOut struct {
	OK       bool     `json:"ok"`
	Target   string   `json:"target"`
	Restored []string `json:"restored"`
	Skipped  []string `json:"skipped"`
	Sequence []string `json:"sequence,omitempty"`
}

func (s *Server) runRestore(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var req restoreReq
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if req.BagID == "" {
		http.Error(w, "bagId required", 400)
		return
	}

	s.mu.Lock()
	var bag pipeline.Bag
	found := false
	for _, b := range s.Bags {
		if b.ID == req.BagID {
			bag = b
			found = true
			break
		}
	}
	eng := s.Eng
	repo := s.Cfg.RepoFor(bag.Satchel)
	dataDir := s.DataDir
	s.mu.Unlock()
	if !found {
		http.Error(w, "bag not found", http.StatusNotFound)
		return
	}
	if eng == nil {
		http.Error(w, "no engine", 500)
		return
	}

	wantAll := len(req.Items) == 0 || (len(req.Items) == 1 && req.Items[0] == "all")
	want := map[string]bool{}
	for _, id := range req.Items {
		want[id] = true
	}

	target := req.Target
	if target == "" {
		target = filepath.Join(dataDir, "restore", bag.ID)
	}
	if !pathInside(dataDir, target) {
		http.Error(w, "restore target must stay under the data dir", 400)
		return
	}

	out := restoreOut{Target: target, Restored: []string{}, Skipped: []string{}}
	needSequence := false
	for _, it := range bag.Contents {
		if !wantAll && !want[it.ID] {
			continue
		}
		if it.Sequence {
			needSequence = true
			out.Skipped = append(out.Skipped, it.ID+": control-plane sequence")
			continue
		}
		if !it.Restorable || it.SnapshotID == "" {
			out.Skipped = append(out.Skipped, it.ID+": not snapshotted")
			continue
		}
		dest := filepath.Join(target, it.ID)
		if err := eng.Restore(r.Context(), engine.RestoreIn{
			Repo: repo, ID: it.SnapshotID, Target: dest,
		}); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		out.Restored = append(out.Restored, it.ID)
	}
	if needSequence {
		out.Sequence = append([]string(nil), pipeline.ControlPlaneSteps...)
	}
	if len(out.Restored) == 0 && !needSequence {
		http.Error(w, "nothing to restore in that selection", 400)
		return
	}
	out.OK = true
	writeJSON(w, out)
}

// pathInside keeps restore-beside on this cell. Restore must not be able
// to pick /var/lib/etcd or another live data-dir via the console.
func pathInside(root, target string) bool {
	if root == "" {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absT, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absT)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *Server) depots(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, config.Redact(s.Cfg).Remotes)
}

func (s *Server) schedule(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, s.Cfg.Schedule)
}

func (s *Server) anomalies(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	type flag struct {
		ID, Title, Detail string
	}
	flags := make([]flag, 0)
	if s.LastErr != "" {
		flags = append(flags, flag{ID: "last-run", Title: "Last run complained", Detail: s.LastErr})
	}
	if len(s.Cfg.Remotes) == 0 {
		flags = append(flags, flag{ID: "no-offside", Title: "No off-side bag", Detail: "3-2-1 wants a depot that is not this disk."})
	}
	writeJSON(w, flags)
}

func (s *Server) runBackup(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.Running {
		s.mu.Unlock()
		http.Error(w, "backup already running", http.StatusConflict)
		return
	}
	s.Running = true
	cfg := s.Cfg
	eng := s.Eng
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			s.Running = false
			s.mu.Unlock()
		}()
		pipe := pipeline.Pipeline{Cfg: cfg, Eng: eng, Logf: log.Printf}
		// Request context dies when this handler returns; the run is a job.
		results, err := pipe.BackupAll(context.Background())
		s.mu.Lock()
		s.LastAt = time.Now().UTC()
		if len(results) > 0 {
			s.Last = results[0]
		}
		if err != nil {
			s.LastErr = err.Error()
		} else {
			s.LastErr = ""
		}
		for i := len(results) - 1; i >= 0; i-- {
			res := results[i]
			overlay, ok := cfg.SatchelNamed(res.Satchel)
			if !ok {
				overlay = cfg.House()
			}
			sites := 1
			if len(overlay.Remotes) > 0 {
				sites = 1 + len(overlay.Remotes)
			}
			bag := res.Bag(cfg.Engine, sites)
			s.Bags = append([]pipeline.Bag{bag}, s.Bags...)
		}
		if len(s.Bags) > 50 {
			s.Bags = s.Bags[:50]
		}
		saveBags(s.DataDir, s.Bags)
		s.mu.Unlock()
		_ = pipeline.Stamp(s.DataDir, "last-backup")
	}()
	writeJSON(w, map[string]any{"started": true})
}

func (s *Server) runPackCopy(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	cfg := s.Cfg
	eng := s.Eng
	s.mu.Unlock()
	var last engine.PackCopyResult
	for _, sat := range cfg.AllSatchels() {
		sub := sat.Apply(cfg)
		repo := sub.Repo()
		for _, rem := range sub.Remotes {
			pc, err := eng.PackCopy(r.Context(), engine.PackCopyIn{
				Repo: repo,
				Dest: engine.ObjectDest{
					Endpoint: rem.Endpoint, Bucket: rem.Bucket, Prefix: rem.Prefix,
					Region: rem.Region, DisableTLS: rem.DisableTLS,
					ObjectLock: rem.ObjectLock, LiveRepo: rem.LiveRepo,
					Role: engine.RoleIngest,
				},
			})
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			last = pc
		}
	}
	writeJSON(w, last)
}

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if s.Web == nil {
		http.NotFound(w, r)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		p = "index.html"
	}
	b, err := fs.ReadFile(s.Web, p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(p, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(p, ".js"):
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	default:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	_, _ = w.Write(b)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func mergeFile(base, patch config.File, raw []byte) config.File {
	var probe map[string]json.RawMessage
	_ = json.Unmarshal(raw, &probe)
	if _, ok := probe["engine"]; ok && patch.Engine != "" {
		base.Engine = patch.Engine
	}
	if _, ok := probe["listen"]; ok && patch.Listen != "" {
		base.Listen = patch.Listen
	}
	if _, ok := probe["selfInclude"]; ok {
		base.SelfInclude = patch.SelfInclude
	}
	if _, ok := probe["selection"]; ok {
		base.Selection = patch.Selection
	}
	if _, ok := probe["schedule"]; ok {
		base.Schedule = patch.Schedule
	}
	if _, ok := probe["localRepo"]; ok {
		base.LocalRepo = patch.LocalRepo
	}
	if _, ok := probe["remotes"]; ok {
		config.NormalizeRemotes(patch.Remotes)
		base.Remotes = patch.Remotes
	}
	if _, ok := probe["dumps"]; ok {
		base.Dumps = patch.Dumps
	}
	if _, ok := probe["retention"]; ok {
		base.Retention = patch.Retention
	}
	if _, ok := probe["satchels"]; ok {
		base.Satchels = patch.Satchels
	}
	return base
}

func saveBags(dir string, bags []pipeline.Bag) {
	if dir == "" {
		return
	}
	b, err := json.MarshalIndent(bags, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "bags.json"), b, 0o600)
}

func LoadBags(dir string) []pipeline.Bag {
	b, err := os.ReadFile(filepath.Join(dir, "bags.json"))
	if err != nil {
		return nil
	}
	var bags []pipeline.Bag
	if json.Unmarshal(b, &bags) != nil {
		return nil
	}
	return bags
}

func LoadOrCreatePassword(dataDir, envVal string) (string, bool, error) {
	if envVal != "" {
		return envVal, false, nil
	}
	path := filepath.Join(dataDir, "console.password")
	if b, err := os.ReadFile(path); err == nil {
		return strings.TrimSpace(string(b)), false, nil
	}
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", false, err
	}
	pw := hex.EncodeToString(buf[:])
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", false, err
	}
	if err := os.WriteFile(path, []byte(pw+"\n"), 0o600); err != nil {
		return "", false, err
	}
	return pw, true, nil
}
