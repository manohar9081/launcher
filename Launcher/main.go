// Project Launcher — one page to start/stop/open every local web app.
//
// Go port of server.py (stdlib only). Serves the dashboard and a small JSON API:
//
//	GET  /                  dashboard (index.html)
//	GET  /api/apps          status of every app
//	POST /api/start   {id}  start one app
//	POST /api/stop    {id}  stop one app (also stops instances started elsewhere)
//	POST /api/all/start     start everything that is stopped
//	POST /api/all/stop      stop everything that is running
//	GET  /api/logs?id=…     tail of an app's log
//
// Run:  go run .            (or ./launcher.exe)   →  http://127.0.0.1:9090
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	serverVersion = "ProjectLauncher/1.0"
	busyMessage   = "an action is already running for this app"

	// Probe defaults (Python: port_open(port, timeout=0.3) and 0.2 call sites).
	defaultProbeTimeout = 300 * time.Millisecond
	startProbeTimeout   = 200 * time.Millisecond

	// Startup wait (Python: 50 rounds of probe + 0.3 s sleep, ~15 s budget).
	startWaitRounds = 50
	startWaitSleep  = 300 * time.Millisecond

	// Stop wait (Python: 20 rounds of check + 0.25 s sleep).
	stopWaitRounds = 20
	stopWaitSleep  = 250 * time.Millisecond

	// tail_log default.
	logTailLines = 150
)

// ------------------------------------------------------------------ config ----

// Config carries the same knobs server.py derived at import time.
type Config struct {
	// ProjectRoot is the parent of Root (Python PROJECT_ROOT). Kept for parity
	// with server.py, which defines it but never uses it.
	ProjectRoot string
	// Root is the directory holding apps.json, index.html, state.json and logs/
	// (Python ROOT — the directory containing server.py). In single-file builds
	// it is the data directory instead.
	Root      string
	AppsFile  string
	StateFile string
	LogDir    string
	IndexFile string
	Host      string
	Port      int
	// DataDir is where the single-file build unpacks app binaries, assets,
	// logs and state (empty in folder mode).
	DataDir string
}

// DefaultConfig resolves paths relative to the executable or working
// directory. LAUNCHER_ROOT overrides it for packaged deployments. In
// single-file builds everything lives under a per-user data directory
// (LAUNCHER_DATA_DIR overrides) so nothing is written next to the exe.
func DefaultConfig() Config {
	if embeddedBuild {
		dataDir := defaultDataDir()
		return Config{
			Root:      dataDir,
			DataDir:   dataDir,
			AppsFile:  "", // packed inside the executable
			StateFile: filepath.Join(dataDir, "state.json"),
			LogDir:    filepath.Join(dataDir, "logs"),
			IndexFile: "", // served from the embedded dashboard
			Host:      envOr("HOST", "0.0.0.0"),
			Port:      envInt("PORT", 9090),
		}
	}
	root := sourceDir()
	if v := os.Getenv("LAUNCHER_ROOT"); v != "" {
		root = v
	}
	return Config{
		ProjectRoot: filepath.Dir(root),
		Root:        root,
		AppsFile:    filepath.Join(root, "apps.json"),
		StateFile:   filepath.Join(root, "state.json"),
		LogDir:      filepath.Join(root, "logs"),
		IndexFile:   filepath.Join(root, "index.html"),
		Host:        envOr("HOST", "0.0.0.0"),
		Port:        envInt("PORT", 9090),
	}
}

// sourceDir returns the runtime directory, so moving a complete app directory
// does not leave the launcher pointing at the build machine's source tree.
func sourceDir() string {
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		exeDir := filepath.Dir(exe)
		if fileExists(filepath.Join(exeDir, "apps.json")) {
			return exeDir
		}
	}
	wd, _ := os.Getwd()
	return wd
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// -------------------------------------------------------------------- apps ----

// App mirrors one entry of the "apps" list in apps.json.
type App struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Icon     string            `json:"icon"`
	Desc     string            `json:"desc"`
	Category string            `json:"category"`
	Port     int               `json:"port"`
	URLPath  string            `json:"urlPath"`
	Cmd      string            `json:"cmd"`
	Binary   string            `json:"binary,omitempty"`
	Args     []string          `json:"args,omitempty"`
	Network  bool              `json:"network"`
	Env      map[string]string `json:"env,omitempty"`
	ServedBy string            `json:"servedBy,omitempty"` // "launcher" = HTML app hosted in-process (single-file build)
	RawDir   string            `json:"dir"`
	Dir      string            `json:"-"` // resolved absolute dir (Python "_dir")
}

// stateEntry is one value in state.json (Python: {"pid": …, "startedAt": …}).
type stateEntry struct {
	PID       int     `json:"pid"`
	StartedAt float64 `json:"startedAt"`
}

type stateMap map[string]stateEntry

// Server implements http.Handler. One instance owns the app registry, the
// in-flight guard and the state-file lock (Python: APPS / INFLIGHT / MUTEX).
type Server struct {
	cfg      Config
	apps     []*App
	byID     map[string]*App
	mu       sync.Mutex
	inflight map[string]struct{}
	// hostedListeners holds the in-process HTTP listeners of HTML apps hosted
	// inside the launcher itself (from the embed in single-file builds, from
	// the app's disk folder in dev).
	hostedListeners map[string]net.Listener
}

// NewServer loads apps.json and returns a handler serving the launcher API.
func NewServer(cfg Config) (*Server, error) {
	apps, byID, err := loadApps(cfg)
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:               cfg,
		apps:              apps,
		byID:              byID,
		inflight:          make(map[string]struct{}),
		hostedListeners: make(map[string]net.Listener),
	}, nil
}

func loadApps(cfg Config) ([]*App, map[string]*App, error) {
	var parsed struct {
		Apps []*App `json:"apps"`
	}
	if embeddedBuild {
		data, err := embeddedConfigJSON()
		if err != nil {
			return nil, nil, err
		}
		if err := json.Unmarshal(data, &parsed); err != nil {
			return nil, nil, fmt.Errorf("embedded apps.json: %w", err)
		}
	} else if data, err := os.ReadFile(cfg.AppsFile); err == nil {
		if err := json.Unmarshal(data, &parsed); err != nil {
			return nil, nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, nil, err
	}
	if parsed.Apps == nil {
		parsed.Apps = []*App{}
	}
	byID := make(map[string]*App, len(parsed.Apps))
	kept := make([]*App, 0, len(parsed.Apps))
	for _, a := range parsed.Apps {
		if a == nil || a.ID == "" {
			return nil, nil, fmt.Errorf("%s: app entry missing \"id\"", cfg.AppsFile)
		}
		if _, dup := byID[a.ID]; dup {
			return nil, nil, fmt.Errorf("duplicate app id in apps.json: %s", a.ID)
		}
		byID[a.ID] = a

		// Apps hosted inside the launcher: in the single-file build they have
		// no folder (assets come from the exe); in folder mode they serve
		// straight from their disk folder.
		if a.ServedBy == "launcher" && embeddedBuild {
			a.Dir = ""
			kept = append(kept, a)
			continue
		}

		// Python: a["_dir"] = (ROOT / a["dir"]).resolve()
		base := cfg.Root
		if embeddedBuild {
			base = cfg.DataDir // apps are unpacked into the data directory
		}
		dir := filepath.Join(base, a.RawDir)
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			dir = resolved
		}
		a.Dir = dir
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			// Skip rather than fail: one missing folder should not take down
			// the whole launcher (matters in trimmed release bundles too).
			fmt.Fprintf(os.Stderr, "launcher: warning: skipping app %q — folder missing: %s\n", a.ID, dir)
			delete(byID, a.ID)
			continue
		}
		kept = append(kept, a)
	}
	if embeddedBuild {
		// The exe is self-contained; never scan the user's folders.
		return kept, byID, nil
	}
	discovered, err := discoverApps(cfg, byID, kept)
	if err != nil {
		return nil, nil, err
	}
	return append(kept, discovered...), byID, nil
}

// discoverApps adds simple static web projects that are present beside the
// launcher but have not yet been added to apps.json. The registry remains the
// place for custom commands, ports, labels, and non-static applications.
func discoverApps(cfg Config, byID map[string]*App, configured []*App) ([]*App, error) {
	projectRoot := filepath.Dir(cfg.Root)
	launcherRoot, _ := filepath.Abs(cfg.Root)
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("scan project folders: %w", err)
	}

	usedPorts := make(map[int]bool, len(configured))
	configuredDirs := make(map[string]bool, len(configured))
	for _, a := range configured {
		usedPorts[a.Port] = true
		if absolute, err := filepath.Abs(filepath.Join(cfg.Root, a.RawDir)); err == nil {
			configuredDirs[absolute] = true
		}
	}
	nextPort := 9000
	var discovered []*App
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "Launcher" ||
			strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dir := filepath.Join(projectRoot, entry.Name())
		if absolute, _ := filepath.Abs(dir); absolute == launcherRoot {
			continue
		}
		if absolute, _ := filepath.Abs(dir); configuredDirs[absolute] {
			continue
		}
		kind, entrypoint := discoverableProject(dir)
		if kind == "" {
			continue
		}
		id := appID(entry.Name())
		if _, exists := byID[id]; exists {
			continue
		}
		for usedPorts[nextPort] {
			nextPort++
		}
		rawDir := "../" + entry.Name()
		app := &App{
			ID: id, Name: entry.Name(), Icon: "📦",
			Desc:     "Discovered static web app",
			Category: "Discovered", Port: nextPort, URLPath: "/",
			RawDir: rawDir, Network: true, Dir: dir,
		}
		if kind == "go" {
			app.Binary = rawDir + "/" + id + "-launcher"
			app.Args = []string{}
			app.Desc = "Discovered Go app"
		} else {
			app.ServedBy = "launcher" // hosted in-process, from its folder
			if entrypoint != "index.html" {
				app.URLPath = "/" + entrypoint
			}
		}
		byID[id] = app
		usedPorts[nextPort] = true
		nextPort++
		discovered = append(discovered, app)
	}

	sort.Slice(discovered, func(i, j int) bool { return discovered[i].ID < discovered[j].ID })
	return discovered, nil
}

// discoverableProject identifies folders that contain a runnable app. HTML
// projects use the static server; Go modules use the launcher build logic.
func discoverableProject(dir string) (kind, entrypoint string) {
	if fileExists(filepath.Join(dir, "go.mod")) {
		if goHTTPEntrypoint(dir) {
			return "go", ""
		}
		return "", ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", ""
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".html") {
			if entry.Name() == "index.html" {
				return "static", "index.html"
			}
			if entrypoint == "" {
				entrypoint = entry.Name()
			}
		}
	}
	if entrypoint != "" {
		return "static", entrypoint
	}
	return "", ""
}

func goHTTPEntrypoint(dir string) bool {
	candidates := goSourceFiles(dir)
	if entries, err := os.ReadDir(filepath.Join(dir, "cmd")); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				candidates = append(candidates, filepath.Join(dir, "cmd", entry.Name(), "main.go"))
			}
		}
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		source := string(data)
		if strings.Contains(source, "net/http") ||
			strings.Contains(source, "ListenAndServe") ||
			strings.Contains(source, "http.Server") {
			return true
		}
	}
	return false
}

func goSourceFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".go" {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	return files
}

func appID(name string) string {
	fields := strings.Fields(strings.ToLower(name))
	return strings.Join(fields, "-")
}

func loadState(path string) stateMap {
	data, err := os.ReadFile(path)
	if err != nil {
		return stateMap{}
	}
	st := stateMap{}
	if err := json.Unmarshal(data, &st); err != nil || st == nil {
		return stateMap{}
	}
	return st
}

// saveState writes state.json atomically (Python: tmp file + replace).
func saveState(path string, st stateMap) {
	tmp := strings.TrimSuffix(path, filepath.Ext(path)) + ".tmp"
	data, err := json.MarshalIndent(st, "", " ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "launcher: save_state: %v\n", err)
		return
	}
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "launcher: save_state: %v\n", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		fmt.Fprintf(os.Stderr, "launcher: save_state: %v\n", err)
	}
}

// ------------------------------------------------------------------ probes ----

var lanCache struct {
	sync.Mutex
	ip string
	at time.Time
}

// lanIP returns this machine's Wi-Fi/LAN address ("" when offline / no
// interface). Cached for 30 s like the Python version.
func lanIP() string {
	lanCache.Lock()
	defer lanCache.Unlock()
	if time.Since(lanCache.at) < 30*time.Second {
		return lanCache.ip
	}
	ip := ""
	if runtime.GOOS == "windows" {
		out, err := runCommand(5*time.Second, "powershell", "-NoProfile", "-Command",
			"Get-NetIPAddress -AddressFamily IPv4 | Where-Object {$_.IPAddress -notlike '127.*'} | Select-Object -First 1 -ExpandProperty IPAddress")
		if err == nil {
			if trimmed := strings.TrimSpace(out); trimmed != "" {
				ip = strings.TrimSpace(strings.Split(trimmed, "\n")[0])
			}
		}
	}
	if ip == "" {
		for _, ifc := range []string{"en0", "en1"} { // macOS: primary Wi-Fi / ethernet
			out, err := runCommand(3*time.Second, "ipconfig", "getifaddr", ifc)
			if err != nil {
				continue
			}
			if trimmed := strings.TrimSpace(out); trimmed != "" {
				ip = trimmed
				break
			}
		}
	}
	if ip == "" {
		// UDP "connect" picks a source address without sending packets —
		// same trick as the Python socket fallback.
		if conn, err := net.DialTimeout("udp", "8.8.8.8:80", time.Second); err == nil {
			if host, _, splitErr := net.SplitHostPort(conn.LocalAddr().String()); splitErr == nil && !strings.HasPrefix(host, "127.") {
				ip = host
			}
			conn.Close()
		}
	}
	lanCache.ip, lanCache.at = ip, time.Now()
	return ip
}

// portOpen reports whether something accepts TCP connections on the port.
func portOpen(port int, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// runCommand runs a helper command with a timeout and captures stdout.
// Mirrors subprocess.run(..., capture_output=True, text=True, timeout=N):
// on timeout or spawn failure the output is discarded.
func runCommand(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	hideWindow(cmd) // no console flash for tasklist/netstat/powershell helpers
	out, err := cmd.Output()
	return string(out), err
}

// -------------------------------------------------------- app status types ----

// appStatus is the JSON object of GET /api/apps. Pointer fields are emitted as
// null when unset, matching the Python dict's explicit nulls.
type appStatus struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Icon      string   `json:"icon"`
	Desc      string   `json:"desc"`
	Category  string   `json:"category"`
	Port      int      `json:"port"`
	URL       string   `json:"url"`
	Cmd       string   `json:"cmd"`
	Network   bool     `json:"network"`
	LanURL    *string  `json:"lanUrl"`
	Running   bool     `json:"running"`
	Managed   bool     `json:"managed"`
	External  bool     `json:"external"`
	PID       *int     `json:"pid"`
	Proc      *string  `json:"proc"`
	StartedAt *float64 `json:"startedAt"`
}

func (s *Server) appStatus(a *App, st stateMap) appStatus {
	entry := st[a.ID]
	pid := entry.PID
	ours := pid != 0 && pidAlive(pid)
	running := portOpen(a.Port, defaultProbeTimeout)

	var proc *string
	if running {
		if pids := listenerPIDs(a.Port); len(pids) > 0 {
			if label := procCmdline(pids[0]); label != "" {
				proc = &label
			}
		}
	}

	var lanURL *string
	if a.Network {
		if lan := lanIP(); lan != "" {
			u := fmt.Sprintf("http://%s:%d%s", lan, a.Port, a.URLPath)
			lanURL = &u
		}
	}

	out := appStatus{
		ID:       a.ID,
		Name:     a.Name,
		Icon:     a.Icon,
		Desc:     a.Desc,
		Category: a.Category,
		Port:     a.Port,
		URL:      fmt.Sprintf("http://127.0.0.1:%d%s", a.Port, a.URLPath),
		Cmd:      a.Cmd,
		Network:  a.Network,
		LanURL:   lanURL,
		Running:  running,
		Managed:  ours && running,
		External: running && !ours,
		Proc:     proc,
	}
	if ours {
		out.PID = &pid
		startedAt := entry.StartedAt
		out.StartedAt = &startedAt
	}
	return out
}

// ----------------------------------------------------------- start / stop ----

// actionResp is the JSON body of /api/start and /api/stop. Pointer/omitempty
// fields are present in the output exactly when the Python version includes
// them (e.g. "killed": [] stays visible, absent keys stay absent).
type actionResp struct {
	OK             bool       `json:"ok"`
	Busy           bool       `json:"busy,omitempty"`
	AlreadyRunning bool       `json:"alreadyRunning,omitempty"`
	Error          string     `json:"error,omitempty"`
	PID            *int       `json:"pid,omitempty"`
	Slow           bool       `json:"slow,omitempty"`
	Note           string     `json:"note,omitempty"`
	Killed         *[]int     `json:"killed,omitempty"`
	StillRunning   *bool      `json:"stillRunning,omitempty"`
	Status         *appStatus `json:"status,omitempty"`
	ID             string     `json:"id,omitempty"` // set on /api/all/* results
}

type allResp struct {
	OK      bool          `json:"ok"`
	Results []*actionResp `json:"results"`
}

type healthResp struct {
	OK      bool    `json:"ok"`
	Lan     *string `json:"lan"`
	LanURL  *string `json:"lanUrl"`
	Mode    string  `json:"mode,omitempty"`    // "single-file" in embed builds
	DataDir string  `json:"dataDir,omitempty"` // where extracted apps live (embed builds)
	Window  string  `json:"window,omitempty"`  // "native" | "app" | "browser" | "none"
	OS      string  `json:"os,omitempty"`      // runtime.GOOS, e.g. "windows"
}

type logsResp struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Lines []string `json:"lines"`
}

func (s *Server) logPath(a *App) string {
	return filepath.Join(s.cfg.LogDir, a.ID+".log")
}

// startApp runs one start with the in-flight guard (Python start_app).
func (s *Server) startApp(a *App) *actionResp {
	s.mu.Lock()
	if _, busy := s.inflight[a.ID]; busy {
		s.mu.Unlock()
		return &actionResp{OK: false, Busy: true, Error: busyMessage}
	}
	s.inflight[a.ID] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inflight, a.ID)
		s.mu.Unlock()
	}()
	return s.startAppLocked(a)
}

func (s *Server) startAppLocked(a *App) *actionResp {
	if a.ServedBy == "launcher" {
		return s.startHostedStatic(a)
	}
	if portOpen(a.Port, defaultProbeTimeout) {
		return &actionResp{OK: true, AlreadyRunning: true}
	}
	if err := os.MkdirAll(s.cfg.LogDir, 0o755); err != nil {
		return &actionResp{OK: false, Error: err.Error()}
	}
	logFile, err := os.OpenFile(s.logPath(a), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return &actionResp{OK: false, Error: err.Error()}
	}
	defer logFile.Close()
	fmt.Fprintf(logFile, "\n===== started %s =====\n", time.Now().Format("2006-01-02 15:04:05"))

	env := childEnv(a)
	cmdStr := normalizeAppCommand(strings.ReplaceAll(a.Cmd, "{port}", strconv.Itoa(a.Port)))
	var cmd *exec.Cmd
	if a.Binary != "" {
		binary, err := resolveBinary(s.cfg.Root, a.Binary)
		if err != nil {
			return &actionResp{OK: false, Error: err.Error()}
		}
		args := make([]string, 0, len(a.Args))
		for _, arg := range a.Args {
			args = append(args, strings.ReplaceAll(arg, "{port}", strconv.Itoa(a.Port)))
		}
		cmd = exec.Command(binary, args...)
	} else {
		parts := shellCommand(cmdStr)
		cmd = exec.Command(parts[0], parts[1:]...)
	}
	cmd.Dir = a.Dir
	cmd.Env = env
	cmd.Stdout = logFile
	cmd.Stderr = logFile // Python: stderr=subprocess.STDOUT
	cmd.SysProcAttr = processSysProcAttr()
	if err := cmd.Start(); err != nil {
		return &actionResp{OK: false, Error: err.Error()}
	}
	pid := cmd.Process.Pid

	s.mu.Lock()
	st := loadState(s.cfg.StateFile)
	st[a.ID] = stateEntry{PID: pid, StartedAt: float64(time.Now().UnixNano()) / 1e9}
	saveState(s.cfg.StateFile, st)
	s.mu.Unlock()

	// Wait for the port to answer (max ~15 s, e.g. streamlit is slow).
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	for i := 0; i < startWaitRounds; i++ {
		if portOpen(a.Port, startProbeTimeout) {
			return &actionResp{OK: true, PID: &pid}
		}
		select {
		case <-exited:
			return &actionResp{OK: false, Error: "process exited during startup", PID: &pid}
		default:
		}
		time.Sleep(startWaitSleep)
	}
	note := "started, but port did not answer within 15 s — check logs"
	return &actionResp{OK: true, PID: &pid, Slow: true, Note: note}
}

// startHostedStatic hosts an HTML app inside the launcher process: no child
// process, the port is served from the embedded assets (single-file build)
// or straight from the app's disk folder (dev build).
func (s *Server) startHostedStatic(a *App) *actionResp {
	if portOpen(a.Port, defaultProbeTimeout) {
		return &actionResp{OK: true, AlreadyRunning: true}
	}
	handler, err := staticHandlerFor(a)
	if err != nil {
		return &actionResp{OK: false, Error: err.Error()}
	}
	if err := os.MkdirAll(s.cfg.LogDir, 0o755); err != nil {
		return &actionResp{OK: false, Error: err.Error()}
	}
	if logFile, err := os.OpenFile(s.logPath(a), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644); err == nil {
		fmt.Fprintf(logFile, "\n===== started %s (hosted inside launcher.exe) =====\n",
			time.Now().Format("2006-01-02 15:04:05"))
		logFile.Close()
	}
	host := "127.0.0.1"
	if a.Network {
		host = "0.0.0.0"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(a.Port)))
	if err != nil {
		return &actionResp{OK: false, Error: err.Error()}
	}
	pid := os.Getpid()
	s.mu.Lock()
	s.hostedListeners[a.ID] = ln
	st := loadState(s.cfg.StateFile)
	st[a.ID] = stateEntry{PID: pid, StartedAt: float64(time.Now().UnixNano()) / 1e9}
	saveState(s.cfg.StateFile, st)
	s.mu.Unlock()
	go func() {
		srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
		_ = srv.Serve(ln) // returns when the app is stopped or the launcher exits
	}()
	return &actionResp{OK: true, PID: &pid}
}

// stopHostedStatic closes the in-process listener of a hosted HTML app.
func (s *Server) stopHostedStatic(a *App) *actionResp {
	s.mu.Lock()
	ln := s.hostedListeners[a.ID]
	delete(s.hostedListeners, a.ID)
	st := loadState(s.cfg.StateFile)
	delete(st, a.ID)
	saveState(s.cfg.StateFile, st)
	s.mu.Unlock()
	if ln != nil {
		ln.Close()
	}
	for i := 0; i < 10; i++ {
		if !portOpen(a.Port, startProbeTimeout) {
			still := false
			killed := []int{}
			return &actionResp{OK: true, Killed: &killed, StillRunning: &still}
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Port still held after our listener closed → someone else's process; use
	// the ordinary external-kill path so Stop stays trustworthy.
	killed := []int{}
	for _, p := range listenerPIDs(a.Port) {
		if killProc(p) == nil {
			killed = append(killed, p)
		}
	}
	for i := 0; i < stopWaitRounds; i++ {
		if !portOpen(a.Port, startProbeTimeout) {
			break
		}
		time.Sleep(stopWaitSleep)
	}
	still := portOpen(a.Port, startProbeTimeout)
	return &actionResp{OK: !still, Killed: &killed, StillRunning: &still}
}

func resolveBinary(root, configured string) (string, error) {
	if embeddedBuild {
		// Packaged binaries are unpacked into <data>/bin; the packaged name
		// carries no extension, Windows needs .exe.
		path := filepath.Join(root, "bin", embeddedBinaryName(configured))
		if !fileExists(path) {
			return "", fmt.Errorf("packaged binary missing: %s", path)
		}
		if err := ensureExecutable(path); err != nil {
			return "", fmt.Errorf("make binary executable: %s: %v", path, err)
		}
		return path, nil
	}
	path := configured
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, _ = filepath.Abs(path)
	platformPath := platformBinaryPath(path)
	candidates := []string{platformPath}
	for _, candidate := range candidates {
		if fileExists(candidate) {
			if err := ensureExecutable(candidate); err != nil {
				return "", fmt.Errorf("make binary executable: %s: %v", candidate, err)
			}
			return candidate, nil
		}
	}
	if built, err := buildGoBinary(platformPath); err == nil {
		return built, nil
	} else {
		return "", fmt.Errorf("Go app binary not found: %s (looked in %s; automatic build failed: %v)", configured, strings.Join(candidates, ", "), err)
	}
}

func platformBinaryPath(path string) string {
	suffixed := path + "-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		suffixed += ".exe" // Windows needs the extension to execute reliably
	}
	return suffixed
}

// buildGoBinary builds an app on first use when its platform binary is absent.
// Most apps have main.go at the module root; Monitor uses the conventional
// cmd/monitor entrypoint, which is discovered automatically.
func buildGoBinary(output string) (string, error) {
	moduleDir := filepath.Dir(output)
	if !fileExists(filepath.Join(moduleDir, "go.mod")) {
		return "", fmt.Errorf("no go.mod in %s", moduleDir)
	}

	buildTarget := "."
	if !hasMainPackage(moduleDir) {
		entries, err := os.ReadDir(filepath.Join(moduleDir, "cmd"))
		if err != nil {
			return "", fmt.Errorf("no Go entrypoint in %s", moduleDir)
		}
		var entrypoints []string
		for _, entry := range entries {
			if entry.IsDir() && fileExists(filepath.Join(moduleDir, "cmd", entry.Name(), "main.go")) {
				entrypoints = append(entrypoints, "./cmd/"+entry.Name())
			}
		}
		if len(entrypoints) != 1 {
			return "", fmt.Errorf("expected one Go entrypoint in %s/cmd, found %d", moduleDir, len(entrypoints))
		}
		buildTarget = entrypoints[0]
	}

	cmd := exec.Command("go", "build", "-o", output, buildTarget)
	hideWindow(cmd)
	cmd.Dir = moduleDir
	if outputText, err := cmd.CombinedOutput(); err != nil {
		message := strings.TrimSpace(string(outputText))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("go build in %s: %s", moduleDir, message)
	}
	if !fileExists(output) {
		return "", fmt.Errorf("go build completed without creating %s", output)
	}
	if err := ensureExecutable(output); err != nil {
		return "", fmt.Errorf("make built binary executable: %s: %v", output, err)
	}
	return output, nil
}

func hasMainPackage(dir string) bool {
	for _, path := range goSourceFiles(dir) {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), "package main") {
			return true
		}
	}
	return false
}

func ensureExecutable(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o111 == 0 {
		return os.Chmod(path, info.Mode().Perm()|0o755)
	}
	return nil
}

// childEnv builds the child environment: inherited env, PORT, then the app's
// env entries with "{port}" substituted (Python _start_app_locked).
func childEnv(a *App) []string {
	env := make(map[string]string)
	for _, kv := range os.Environ() {
		if i := strings.Index(kv, "="); i > 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}
	env["PORT"] = strconv.Itoa(a.Port)
	for k, v := range a.Env {
		env[k] = strings.ReplaceAll(v, "{port}", strconv.Itoa(a.Port))
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// stopApp runs one stop with the in-flight guard (Python stop_app).
func (s *Server) stopApp(a *App) *actionResp {
	s.mu.Lock()
	if _, busy := s.inflight[a.ID]; busy {
		s.mu.Unlock()
		return &actionResp{OK: false, Busy: true, Error: busyMessage}
	}
	s.inflight[a.ID] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inflight, a.ID)
		s.mu.Unlock()
	}()
	return s.stopAppLocked(a)
}

func (s *Server) stopAppLocked(a *App) *actionResp {
	if a.ServedBy == "launcher" {
		return s.stopHostedStatic(a)
	}
	st := loadState(s.cfg.StateFile)
	pid := st[a.ID].PID
	killed := []int{}

	// 1) the instance this launcher started (whole process group)
	if pid != 0 && pidAlive(pid) {
		killTree(pid)
		killed = append(killed, pid)
		for i := 0; i < stopWaitRounds; i++ {
			if !pidAlive(pid) {
				break
			}
			time.Sleep(stopWaitSleep)
		}
		if pidAlive(pid) {
			killTreeForce(pid)
		}
	}

	// 2) anything else of ours still listening on the port (external start)
	if portOpen(a.Port, defaultProbeTimeout) {
		for _, p := range listenerPIDs(a.Port) {
			if killProc(p) == nil {
				killed = append(killed, p)
			}
		}
		for i := 0; i < stopWaitRounds; i++ {
			if !portOpen(a.Port, startProbeTimeout) {
				break
			}
			time.Sleep(stopWaitSleep)
		}
		if portOpen(a.Port, startProbeTimeout) {
			for _, p := range listenerPIDs(a.Port) {
				killProcForce(p)
			}
		}
	}

	s.mu.Lock()
	st = loadState(s.cfg.StateFile)
	delete(st, a.ID)
	saveState(s.cfg.StateFile, st)
	s.mu.Unlock()

	still := portOpen(a.Port, startProbeTimeout)
	return &actionResp{OK: !still, Killed: &killed, StillRunning: &still}
}

// killAllApps stops every app this launcher started (state-tracked instances).
// It backs --kill-apps-on-exit: on Ctrl+C or closing the console the panel
// takes the apps it started down with it instead of leaving them detached.
// Apps running without a state entry (started outside the launcher) survive.
func (s *Server) killAllApps() {
	st := loadState(s.cfg.StateFile)
	for _, a := range s.apps {
		if _, ours := st[a.ID]; !ours {
			continue
		}
		if res := s.stopApp(a); !res.OK {
			fmt.Printf("  ! could not stop %s: %s\n", a.Name, res.Error)
		}
	}
}

// tailLog returns the last `lines` lines of the app's log ([] when missing).
func (s *Server) tailLog(a *App, lines int) []string {
	data, err := os.ReadFile(s.logPath(a))
	if err != nil {
		return []string{}
	}
	all := splitLines(strings.ToValidUTF8(string(data), "\uFFFD"))
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	if all == nil {
		return []string{}
	}
	return all
}

// splitLines mirrors Python str.splitlines() for \n / \r\n / \r input
// (trailing newline does not produce a trailing empty line).
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// ------------------------------------------------------------------- http ----

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// ServeHTTP routes like the Python do_GET/do_POST pair: the raw request path
// is matched literally (no normalization), unmatched combinations get a JSON
// 404.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Server", serverVersion)
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	s.route(rec, r)
	// Python log_message: "[HH:MM:SS] \"GET /api/apps HTTP/1.1\" 200 -"
	fmt.Fprintf(os.Stderr, "[%s] \"%s %s HTTP/1.1\" %d -\n",
		time.Now().Format("15:04:05"), r.Method, r.RequestURI, rec.status)
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	path, _, _ := strings.Cut(r.RequestURI, "?")
	switch {
	case r.Method == http.MethodGet && (path == "/" || path == "/index.html"):
		s.serveIndex(w)
	case r.Method == http.MethodGet && path == "/api/apps":
		noteActivity()
		s.mu.Lock()
		st := loadState(s.cfg.StateFile)
		s.mu.Unlock()
		statuses := make([]appStatus, 0, len(s.apps))
		for _, a := range s.apps {
			statuses = append(statuses, s.appStatus(a, st))
		}
		s.writeJSON(w, http.StatusOK, statuses)
	case r.Method == http.MethodGet && path == "/api/health":
		noteActivity()
		var out healthResp
		out.OK = true
		if embeddedBuild {
			out.Mode = "single-file"
			out.DataDir = s.cfg.DataDir
		}
		out.Window = launchWindow
		out.OS = runtime.GOOS
		if lan := lanIP(); lan != "" {
			out.Lan = &lan
			u := fmt.Sprintf("http://%s:%d", lan, s.cfg.Port)
			out.LanURL = &u
		}
		s.writeJSON(w, http.StatusOK, out)
	case r.Method == http.MethodPost && path == "/api/heartbeat":
		// Dashboard keepalive; ?bye=1 is the pagehide beacon sent when the
		// last dashboard window/tab goes away (window mode shuts down then).
		noteActivity()
		if parseQuery(r.RequestURI)["bye"] == "1" {
			handleBye(s)
		}
		s.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case r.Method == http.MethodPost && path == "/api/shutdown":
		// Explicit quit from the dashboard. Loopback only — a device on the
		// Wi-Fi may stop apps but not kill the launcher itself.
		if !isLoopback(r.Host) {
			s.writeJSON(w, http.StatusForbidden, map[string]string{"error": "shutdown is only allowed from this computer"})
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		time.AfterFunc(300*time.Millisecond, func() { s.beginShutdown("quit requested from the dashboard") })
	case r.Method == http.MethodPost && path == "/api/open":
		// Open a URL on this computer (tab mode from the native window: the
		// WebView2 window has no tabs, so the app lands in the default
		// browser instead). Loopback only.
		if !isLoopback(r.Host) {
			s.writeJSON(w, http.StatusForbidden, map[string]string{"error": "open is only allowed from this computer"})
			return
		}
		var req openRequest
		readBodyInto(r, &req)
		u := strings.TrimSpace(req.URL)
		if !strings.HasPrefix(u, "http://127.0.0.1") && !strings.HasPrefix(u, "http://[::1]") &&
			!strings.HasPrefix(u, "https://") {
			s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported url"})
			return
		}
		openBrowser(u)
		s.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case r.Method == http.MethodPost && path == "/api/firewall":
		// One-click fix for the per-app Windows Firewall "Allow/Cancel"
		// popups: adds allow-rules for the launcher and every network app via
		// a single UAC elevation. Windows only; loopback only.
		if !isLoopback(r.Host) {
			s.writeJSON(w, http.StatusForbidden, map[string]string{"error": "firewall fix is only allowed from this computer"})
			return
		}
		s.writeJSON(w, http.StatusOK, s.firewallFix())
	case r.Method == http.MethodGet && path == "/api/logs":
		s.serveLogs(w, r.RequestURI)
	case r.Method == http.MethodGet && path == "/favicon.ico":
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && path == "/api/start":
		s.serveStartStop(w, r, true)
	case r.Method == http.MethodPost && path == "/api/stop":
		s.serveStartStop(w, r, false)
	case r.Method == http.MethodPost && path == "/api/all/start":
		s.serveAll(w, true)
	case r.Method == http.MethodPost && path == "/api/all/stop":
		s.serveAll(w, false)
	default:
		s.writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (s *Server) writeJSON(w http.ResponseWriter, code int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "json encode error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

func (s *Server) serveIndex(w http.ResponseWriter) {
	body := []byte(nil)
	if embeddedBuild {
		body = embeddedIndexHTML
	} else {
		body, _ = os.ReadFile(s.cfg.IndexFile)
	}
	if len(body) == 0 {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "index.html missing"})
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *Server) serveLogs(w http.ResponseWriter, requestURI string) {
	qs := parseQuery(requestURI)
	app := s.byID[qs["id"]]
	if app == nil {
		s.writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown id"})
		return
	}
	s.writeJSON(w, http.StatusOK, logsResp{
		ID:    app.ID,
		Name:  app.Name,
		Lines: s.tailLog(app, logTailLines),
	})
}

// parseQuery mirrors the Python handler's ad-hoc parser: split on "&", keep
// only "k=v" pairs (first "=" splits), last duplicate wins, no URL decoding.
func parseQuery(requestURI string) map[string]string {
	qs := map[string]string{}
	i := strings.Index(requestURI, "?")
	if i < 0 {
		return qs
	}
	for _, pair := range strings.Split(requestURI[i+1:], "&") {
		j := strings.Index(pair, "=")
		if j < 0 {
			continue
		}
		qs[pair[:j]] = pair[j+1:]
	}
	return qs
}

type idRequest struct {
	ID string `json:"id"`
}

type openRequest struct {
	URL string `json:"url"`
}

// isLoopback reports whether the request came from this computer (Host is
// 127.0.0.1[:port] or [::1][:port]).
func isLoopback(host string) bool {
	return strings.HasPrefix(host, "127.0.0.1") || strings.HasPrefix(host, "[::1]")
}

// readBodyInto mirrors Python _body(): no Content-Length or invalid JSON →
// leave v untouched.
func readBodyInto(r *http.Request, v any) {
	n, err := strconv.Atoi(strings.TrimSpace(r.Header.Get("Content-Length")))
	if err != nil || n <= 0 {
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, int64(n)))
	if err != nil && len(data) == 0 {
		return
	}
	_ = json.Unmarshal(data, v)
}

// readBody mirrors the Python handler's ad-hoc parser for {id} bodies.
func readBody(r *http.Request) idRequest {
	var out idRequest
	readBodyInto(r, &out)
	return out
}

func (s *Server) serveStartStop(w http.ResponseWriter, r *http.Request, start bool) {
	req := readBody(r)
	app := s.byID[req.ID]
	if app == nil {
		s.writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown id"})
		return
	}
	var res *actionResp
	if start {
		res = s.startApp(app)
	} else {
		res = s.stopApp(app)
	}
	st := s.appStatus(app, loadState(s.cfg.StateFile))
	res.Status = &st
	code := http.StatusOK
	if !res.OK && !res.Busy {
		code = http.StatusInternalServerError
	}
	s.writeJSON(w, code, res)
}

func (s *Server) serveAll(w http.ResponseWriter, start bool) {
	results := []*actionResp{}
	for _, a := range s.apps {
		if start && portOpen(a.Port, startProbeTimeout) {
			continue // already running → nothing to start
		}
		if !start && !portOpen(a.Port, startProbeTimeout) {
			continue // already stopped → nothing to stop
		}
		var res *actionResp
		if start {
			res = s.startApp(a)
		} else {
			res = s.stopApp(a)
		}
		res.ID = a.ID
		results = append(results, res)
	}
	s.writeJSON(w, http.StatusOK, allResp{OK: true, Results: results})
}

// ------------------------------------------------------------------- main ----

// launchWindow records how the dashboard was opened — "native" (own WebView2
// window), "app" (Edge/Chrome app window), "browser" or "none" — and is
// reported by /api/health so the page can adapt (e.g. tab-mode Open requests
// route through /api/open to land in the PC's default browser as a tab).
var launchWindow = "none"

func main() {
	cfg := DefaultConfig()
	if guiBuild {
		// windowsgui builds have no console; keep the output for launcher.log.
		redirectOutputToLog(cfg.LogDir)
	}
	fail := func(msg string) {
		fmt.Fprintln(os.Stderr, msg)
		showErrorMessage("Project Launcher", msg) // no-op off Windows
		os.Exit(1)
	}
	if embeddedBuild {
		// Unpack the carried apps before anything references them.
		if err := extractEmbedded(cfg.DataDir); err != nil {
			fail("unpacking apps failed: " + err.Error())
		}
	}
	if portOpen(cfg.Port, startProbeTimeout) {
		fail(fmt.Sprintf("Port %d is already in use - is the launcher already running?\n"+
			"Open http://127.0.0.1:%d or stop the other process first.", cfg.Port, cfg.Port))
	}
	srv, err := NewServer(cfg)
	if err != nil {
		fail(err.Error())
	}

	url := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	fmt.Println()
	fmt.Println("  Project Launcher")
	fmt.Println("  --------------------------------------------------")
	fmt.Printf("  Dashboard : %s\n", url)
	lan := lanIP()
	if lan != "" && cfg.Host != "127.0.0.1" {
		fmt.Printf("  Wi-Fi     : http://%s:%d   (open from other devices)\n", lan, cfg.Port)
	}
	fmt.Printf("  Apps      : %d configured - start & stop them from the page\n", len(srv.apps))
	startAppsFlag := slices.Contains(os.Args, "--start-apps")
	killOnExit := slices.Contains(os.Args, "--kill-apps-on-exit")
	windowMode := guiBuild // release builds behave like an app: window closed = done
	if slices.Contains(os.Args, "--no-window") {
		windowMode = false
	}
	if slices.Contains(os.Args, "--window") {
		windowMode = true
	}
	noOpen := os.Getenv("NO_OPEN") == "1" || slices.Contains(os.Args, "--no-open")
	useBrowser := slices.Contains(os.Args, "--browser") // skip the native window
	// The native window is the release experience on Windows: a real Win32
	// window (WebView2) whose close button quits the application.
	nativeWindow := guiBuild && windowMode && !useBrowser && !noOpen
	if startAppsFlag {
		fmt.Println("            (--start-apps given: starting every stopped app now)")
	}
	switch {
	case nativeWindow:
		fmt.Println("            (native window: closing it stops the launcher and its apps)")
	case windowMode:
		fmt.Println("            (window mode: closing the dashboard stops the launcher and its apps)")
	case killOnExit:
		fmt.Println("            (--kill-apps-on-exit: apps started by this panel stop when it exits)")
	default:
		fmt.Println("            (launching/quitting this panel does NOT start or stop any app)")
	}
	if embeddedBuild {
		fmt.Printf("  Data      : %s\n", cfg.DataDir)
	}
	if killOnExit || windowMode {
		fmt.Println("  Stop      : close the dashboard / Ctrl+C   (apps started by this panel stop too)")
	} else {
		fmt.Println("  Stop      : Ctrl+C   (apps keep running)")
	}
	fmt.Println("  --------------------------------------------------")
	fmt.Println()

	if startAppsFlag {
		time.AfterFunc(800*time.Millisecond, func() {
			for _, a := range srv.apps {
				if !portOpen(a.Port, startProbeTimeout) {
					srv.startApp(a)
				}
			}
		})
	}
	if !nativeWindow && !noOpen {
		open := openBrowser
		if windowMode {
			open = openAppWindow
		}
		time.AfterFunc(800*time.Millisecond, func() { open(url) })
	}
	if windowMode {
		startWindowWatchdog(srv)
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fail(err.Error())
	}
	httpSrv := &http.Server{Handler: srv}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(ln) }()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-interrupt
		if windowMode || killOnExit {
			srv.beginShutdown("Ctrl+C") // stops managed apps, then exits
		}
		fmt.Print("\n  Launcher stopped. (Apps keep running — stop them from the page next time.)\n")
		os.Exit(0)
	}()

	if nativeWindow {
		runtime.LockOSThread() // the WebView2 message loop owns this thread
		launchWindow = "native"
		dataPath := ""
		if embeddedBuild {
			dataPath = cfg.DataDir
		}
		if runNativeWindow(url, dataPath) {
			// Window closed (or Terminate) → quit the application.
			srv.beginShutdown("launcher window was closed")
			return
		}
		// WebView2 runtime unavailable → chromeless Edge/Chrome window instead.
		launchWindow = "app"
		if !noOpen {
			time.AfterFunc(200*time.Millisecond, func() { openAppWindow(url) })
		}
	} else if !noOpen {
		if windowMode {
			launchWindow = "app"
		} else {
			launchWindow = "browser"
		}
	}

	select {
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			fail(err.Error())
		}
	}
}

// openBrowser is the Go stand-in for Python's webbrowser.open.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	hideWindow(cmd) // `cmd /c start` would otherwise flash a console
	_ = cmd.Start()
}
