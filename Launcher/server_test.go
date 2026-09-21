// Go port of test_api.py — end-to-end test of the Project Launcher API.
//
// Difference from the Python original: instead of requiring a live server on
// 127.0.0.1:9090 plus the user's 11 real apps, the test drives the handler
// in-process via httptest against a synthetic apps.json, and the "apps" are
// instances of this test binary re-executed in helper mode (TestMain below).
// All original checks are preserved; the environment-dependent ones
// (192.168.* LAN prefix, vedic/monitor preconditions) were adapted and are
// noted inline.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const helperEnvVar = "LAUNCHER_HELPER"

// TestMain doubles as the "app" used by the test registry: re-executed with
// LAUNCHER_HELPER=1 it serves a small HTTP server on $PORT, so the launcher
// can start/stop/detect it like any real app (test_api.py used
// `python -m http.server` for this).
func TestMain(m *testing.M) {
	if os.Getenv(helperEnvVar) == "1" {
		os.Exit(runHelperServer())
	}
	os.Exit(m.Run())
}

func runHelperServer() int {
	port := os.Getenv("PORT")
	if port == "" {
		fmt.Fprintln(os.Stderr, "helper: PORT not set")
		return 1
	}
	body := fmt.Sprintf("launcher-helper ok PORT=%s EXTRA=%s", port, os.Getenv("LAUNCHER_HELPER_EXTRA"))
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	})
	addr := net.JoinHostPort("127.0.0.1", port)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "helper: %v\n", err)
		return 1
	}
	return 0
}

// ------------------------------------------------------------- test helpers ----

var apiClient = &http.Client{Timeout: 60 * time.Second}

// apiCall mirrors test_api.py's api(): POST with a JSON body, or plain GET.
func apiCall(t *testing.T, method, url string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatalf("new request %s: %v", url, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := apiClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return resp.StatusCode, raw
}

func decodeObj(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode JSON object %s: %v", raw, err)
	}
	return out
}

// decodeApps turns a /api/apps response (a bare JSON array) into a map keyed
// by app id, mirroring test_api.py's `{a["id"]: a for a in api("/api/apps")}`.
func decodeApps(t *testing.T, raw []byte) map[string]map[string]any {
	t.Helper()
	var list []any
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode /api/apps: %v", err)
	}
	apps := make(map[string]map[string]any, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("decode /api/apps: entry is %T, want object", item)
		}
		id, _ := m["id"].(string)
		apps[id] = m
	}
	return apps
}

func asBool(m map[string]any, key string) bool     { b, _ := m[key].(bool); return b }
func asString(m map[string]any, key string) string { s, _ := m[key].(string); return s }
func asFloat(m map[string]any, key string) float64 { f, _ := m[key].(float64); return f }

func httpOK(url string) bool {
	status, _, err := fetchPage(url)
	return err == nil && status >= 100 && status < 400
}

func fetchPage(url string) (int, string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", err
	}
	return resp.StatusCode, string(data), nil
}

// portClosed mirrors test_api.py's port_closed (0.4 s connect timeout).
func portClosed(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 400*time.Millisecond)
	if err != nil {
		return true
	}
	conn.Close()
	return false
}

func check(t *testing.T, label string, cond bool) {
	t.Helper()
	if cond {
		t.Logf("PASS  %s", label)
	} else {
		t.Errorf("FAIL  %s", label)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// helperBinaryPath returns this test binary's path, made safe for embedding in
// a shell command line (space-free on Windows via 8.3 short paths).
func helperBinaryPath(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if runtime.GOOS == "windows" {
		exe = shellSafePath(exe)
	}
	return exe
}

// startExternalHelper mirrors the Popen of `python -m http.server 8137` in
// test_api.py: a server the launcher did NOT start, used to exercise external
// detection and the external stop path.
func startExternalHelper(t *testing.T, bin string, port int) (*exec.Cmd, chan struct{}) {
	t.Helper()
	cmd := exec.Command(bin, "--port", strconv.Itoa(port))
	cmd.Env = append(os.Environ(), helperEnvVar+"=1", "PORT="+strconv.Itoa(port))
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = processSysProcAttr()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start external helper: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !portClosed(port) {
			return cmd, done
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	t.Fatalf("external helper on port %d never started listening", port)
	return nil, nil
}

// ---------------------------------------------------------------- the test ----

func TestLauncherAPI(t *testing.T) {
	bin := helperBinaryPath(t)
	root := t.TempDir()
	appDir := filepath.Join(root, "appdir")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("mkdir appdir: %v", err)
	}

	pDemo := freePort(t)
	pTarot := freePort(t)
	pMlops := freePort(t)
	pMonitor := freePort(t)

	helperCmd := bin + " --port {port}"
	if runtime.GOOS != "windows" {
		helperCmd = "'" + bin + "' --port {port}"
	}
	helperEnv := map[string]string{
		helperEnvVar:            "1",
		"LAUNCHER_HELPER_EXTRA": "extra-{port}",
	}

	// Synthetic registry mirroring apps.json: 3 Wi-Fi apps + 1 local app.
	registry := map[string]any{"apps": []*App{
		{ID: "demo", Name: "Demo", Icon: "rocket", Desc: "demo app", Category: "Tools",
			Port: pDemo, URLPath: "/", Cmd: helperCmd, Network: true, RawDir: "appdir", Env: helperEnv},
		{ID: "tarot", Name: "Moonlit Arcana", Icon: "moon", Desc: "tarot deck", Category: "Astrology",
			Port: pTarot, URLPath: "/", Cmd: helperCmd, Network: true, RawDir: "appdir", Env: helperEnv},
		{ID: "mlops", Name: "PyMLOps", Icon: "robot", Desc: "MLOps page", Category: "Reference",
			Port: pMlops, URLPath: "/", Cmd: helperCmd, Network: true, RawDir: "appdir", Env: helperEnv},
		{ID: "monitor", Name: "AppScope Monitor", Icon: "shield", Desc: "monitor dashboard", Category: "Tools",
			Port: pMonitor, URLPath: "/ui", Cmd: helperCmd, Network: false, RawDir: "appdir", Env: helperEnv},
	}}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		t.Fatalf("marshal apps.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "apps.json"), data, 0o644); err != nil {
		t.Fatalf("write apps.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<html>dashboard ok</html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}

	srv, err := NewServer(Config{
		ProjectRoot: filepath.Dir(root),
		Root:        root,
		AppsFile:    filepath.Join(root, "apps.json"),
		StateFile:   filepath.Join(root, "state.json"),
		LogDir:      filepath.Join(root, "logs"),
		IndexFile:   filepath.Join(root, "index.html"),
		Host:        "127.0.0.1",
		Port:        9090,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	base := ts.URL

	// Externally started helper (played by "monitor"/"vedic" in the Python
	// test's preconditions and by "mlops" in the external-stop section).
	extCmd, extDone := startExternalHelper(t, bin, pMlops)

	// Safety net: make sure nothing we spawned outlives the test (the temp
	// dir cannot be removed while a helper still runs inside it).
	defer func() {
		for _, p := range []int{pDemo, pTarot, pMlops, pMonitor} {
			for _, pid := range listenerPIDs(p) {
				killProcForce(pid)
			}
		}
		if extCmd != nil && extCmd.Process != nil {
			_ = extCmd.Process.Kill()
		}
	}()

	// --- status endpoint ---------------------------------------------------------
	_, raw := apiCall(t, "GET", base+"/api/apps", nil)
	apps := decodeApps(t, raw)
	check(t, fmt.Sprintf("GET /api/apps returned %d apps", len(apps)), len(apps) == 4)
	_, hasHumanos := apps["humanos"]
	check(t, "humanos removed from registry", !hasHumanos)

	mlops := apps["mlops"]
	check(t, "externally-started mlops detected",
		asBool(mlops, "running") && asBool(mlops, "external"))
	if proc := asString(mlops, "proc"); proc != "" {
		check(t, "external proc label mentions our helper",
			strings.Contains(strings.ToLower(proc), "launcher"))
	} else {
		t.Log("SKIP  external proc label check (proc unavailable)")
	}
	check(t, "demo not running yet", !asBool(apps["demo"], "running"))

	// --- network vs local separation ----------------------------------------------
	var wifi, local []string
	for id, m := range apps {
		if asBool(m, "network") {
			wifi = append(wifi, id)
		} else {
			local = append(local, id)
		}
	}
	check(t, fmt.Sprintf("%d apps flagged Wi-Fi, %d local (%d/%d)", len(wifi), len(local), len(wifi), len(local)),
		len(wifi) == 3 && len(local) == 1)
	check(t, "local app is monitor (binds 127.0.0.1 via its config)",
		len(local) == 1 && local[0] == "monitor")
	check(t, "local apps have no lanUrl",
		apps["monitor"]["lanUrl"] == nil)
	check(t, "Wi-Fi cmds bind 0.0.0.0 or default to it", func() bool {
		for _, id := range wifi {
			cmd := asString(apps[id], "cmd")
			if !strings.Contains(cmd, "--bind 0.0.0.0") && strings.Contains(cmd, "--bind") {
				return false
			}
		}
		return true
	}())

	code, raw := apiCall(t, "GET", base+"/api/health", nil)
	check(t, "GET /api/health answered 200", code == http.StatusOK)
	health := decodeObj(t, raw)
	lan, hasLAN := health["lan"].(string)
	if hasLAN && lan != "" {
		check(t, "launcher reports its own LAN url",
			asString(health, "lanUrl") == fmt.Sprintf("http://%s:9090", lan))
		check(t, "every Wi-Fi app has a lanUrl on this machine's LAN", func() bool {
			for _, id := range wifi {
				prefix := fmt.Sprintf("http://%s:%d", lan, int(asFloat(apps[id], "port")))
				if got := asString(apps[id], "lanUrl"); got != prefix && !strings.HasPrefix(got, prefix+"/") {
					return false
				}
			}
			return true
		}())
	} else {
		t.Log("SKIP  Wi-Fi lanUrl checks (no LAN address detected on this machine; " +
			"the Python original asserted a hard-coded http://192.168.* prefix)")
	}

	// --- start / verify page / stop, per app ---------------------------------------
	// (The Python original cycled cheatsheet/house/lawbuddy/investment; here we
	// cycle one Wi-Fi app and the local app.)
	portByID := map[string]int{"demo": pDemo, "monitor": pMonitor}
	for _, appID := range []string{"demo", "monitor"} {
		code, raw = apiCall(t, "POST", base+"/api/start", map[string]any{"id": appID})
		r := decodeObj(t, raw)
		check(t, fmt.Sprintf("start %s answered 200", appID), code == http.StatusOK)
		check(t, fmt.Sprintf("start %s ok", appID), r["ok"] == true)
		a, _ := r["status"].(map[string]any)
		check(t, fmt.Sprintf("%s reports running on :%v", appID, a["port"]),
			asBool(a, "running") && asBool(a, "managed"))
		pageURL := asString(a, "url")
		check(t, fmt.Sprintf("%s page loads (%s)", appID, pageURL), httpOK(pageURL))
		_, body, err := fetchPage(pageURL)
		check(t, fmt.Sprintf("%s page served by helper with injected env", appID),
			err == nil && strings.Contains(body, fmt.Sprintf("PORT=%d", portByID[appID])) &&
				strings.Contains(body, fmt.Sprintf("extra-%d", portByID[appID])))
	}
	for _, appID := range []string{"demo", "monitor"} {
		code, raw = apiCall(t, "POST", base+"/api/stop", map[string]any{"id": appID})
		r := decodeObj(t, raw)
		check(t, fmt.Sprintf("stop %s answered 200", appID), code == http.StatusOK)
		check(t, fmt.Sprintf("stop %s ok", appID), r["ok"] == true)
		a, _ := r["status"].(map[string]any)
		check(t, fmt.Sprintf("%s port closed", appID), portClosed(int(asFloat(a, "port"))))
	}

	// --- logs endpoint ---------------------------------------------------------------
	code, raw = apiCall(t, "GET", base+"/api/logs?id=demo", nil)
	check(t, "GET /api/logs answered 200", code == http.StatusOK)
	logs := decodeObj(t, raw)
	lines, isList := logs["lines"].([]any)
	check(t, "GET /api/logs returns lines list", isList)
	check(t, "log tail contains the startup header",
		strings.Contains(joinLines(lines), "===== started"))

	// --- unknown id handling -----------------------------------------------------------
	code, _ = apiCall(t, "POST", base+"/api/start", map[string]any{"id": "nope"})
	check(t, "start unknown id rejected", code == http.StatusNotFound)

	// --- concurrent start of same app (in-flight guard) ---------------------------------
	type startResult struct {
		code int
		body map[string]any
	}
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []startResult
	)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, respBody := apiCall(t, "POST", base+"/api/start", map[string]any{"id": "tarot"})
			mu.Lock()
			results = append(results, startResult{code: c, body: decodeObj(t, respBody)})
			mu.Unlock()
		}()
	}
	wg.Wait()
	realStarts, busyCount := 0, 0
	for _, res := range results {
		if res.body["ok"] == true {
			realStarts++
		}
		if res.body["busy"] == true {
			busyCount++
		}
	}
	check(t, "3 concurrent starts: exactly one executed", realStarts == 1)
	check(t, "3 concurrent starts: others answered busy, not 5xx", busyCount == 2)

	_, raw = apiCall(t, "GET", base+"/api/apps", nil)
	appsNow := decodeApps(t, raw)
	check(t, "tarot running after concurrent starts", asBool(appsNow["tarot"], "running"))

	code, raw = apiCall(t, "POST", base+"/api/stop", map[string]any{"id": "tarot"})
	r := decodeObj(t, raw)
	check(t, "stop tarot ok", code == http.StatusOK && r["ok"] == true)

	// --- external stop path: externally-started app, stopped via launcher ---------------
	_, raw = apiCall(t, "GET", base+"/api/apps", nil)
	appsNow = decodeApps(t, raw)
	check(t, "externally-started mlops detected (pre-stop)",
		asBool(appsNow["mlops"], "running") && asBool(appsNow["mlops"], "external"))

	code, raw = apiCall(t, "POST", base+"/api/stop", map[string]any{"id": "mlops"})
	r = decodeObj(t, raw)
	check(t, "stop external mlops ok",
		code == http.StatusOK && r["ok"] == true && portClosed(pMlops))

	select {
	case <-extDone:
		check(t, "external process really dead", true)
	case <-time.After(5 * time.Second):
		check(t, "external process really dead", false)
	}
}

func joinLines(lines []any) string {
	parts := make([]string, 0, len(lines))
	for _, l := range lines {
		parts = append(parts, fmt.Sprint(l))
	}
	return strings.Join(parts, "\n")
}
