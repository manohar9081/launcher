// Single-file (embed) mode runtime support.
//
// The release launcher.exe carries every app: Go binaries under embedded/bin/
// and each app's web assets under embedded/assets/<app-id>/. At startup the
// binaries (and the assets of apps that read them from disk) are unpacked into
// a per-user data directory; the plain HTML apps are served straight out of
// memory by the launcher itself — no files beside the exe, no separate
// processes for them.
//
// Data directory layout (LAUNCHER_DATA_DIR overrides):
//
//	<data>/bin/<app>            extracted app binaries
//	<data>/apps/<app-id>/...    extracted assets for apps that serve from disk
//	<data>/logs/                launcher + per-app logs
//	<data>/state.json           which apps the launcher started
package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// defaultDataDir is the per-user home for extracted binaries, logs and state.
// Nothing is ever written next to launcher.exe.
func defaultDataDir() string {
	if v := os.Getenv("LAUNCHER_DATA_DIR"); v != "" {
		return v
	}
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("LocalAppData"), "ProjectLauncher")
	case "darwin":
		return filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "ProjectLauncher")
	default:
		return filepath.Join(os.Getenv("HOME"), ".local", "share", "ProjectLauncher")
	}
}

// embeddedConfigJSON reads the packaged apps.json.
func embeddedConfigJSON() ([]byte, error) {
	return fs.ReadFile(embeddedFS, "embedded/apps.json")
}

// extractEmbedded unpacks app binaries and disk-served assets into dataDir.
// Binaries are refreshed on every start (they are part of the exe); asset
// files are only written when missing so user data that lives inside an app
// folder (Investment's data.js, LawBuddy's db) survives launcher updates.
// Deleting the data directory resets everything.
func extractEmbedded(dataDir string) error {
	var parsed struct {
		Apps []*App `json:"apps"`
	}
	data, err := embeddedConfigJSON()
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("embedded apps.json: %w", err)
	}
	return extractApps(dataDir, parsed.Apps)
}

// extractApps does the actual unpacking for the given app list. Apps whose
// binary is not packaged in this build are skipped with a warning — that is
// the "app was removed from release.sh but still listed in apps-embedded.json"
// case, and it must never keep the launcher from starting.
func extractApps(dataDir string, apps []*App) error {
	binDir := filepath.Join(dataDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	for _, a := range apps {
		if a == nil || a.ServedBy == "launcher" {
			continue // hosted in-process, nothing to extract
		}
		if _, err := fs.Stat(embeddedFS, "embedded/bin/"+a.Binary); err != nil {
			fmt.Fprintf(os.Stderr, "launcher: warning: skipping app %q — %s is not packaged in this build\n",
				a.ID, a.Binary)
			continue
		}
		name := embeddedBinaryName(a.Binary)
		if err := extractFile(embeddedFS, "embedded/bin/"+a.Binary, filepath.Join(binDir, name), true); err != nil {
			return fmt.Errorf("extract %s: %w", a.Binary, err)
		}
		// The app's working directory must exist even when the app carries
		// its assets inside the binary (redact, study) — loadApps checks it.
		if err := os.MkdirAll(filepath.Join(dataDir, a.RawDir), 0o755); err != nil {
			return err
		}
		if err := extractTree(embeddedFS, "embedded/assets/"+a.ID, filepath.Join(dataDir, a.RawDir), false); err != nil {
			return fmt.Errorf("extract assets for %s: %w", a.ID, err)
		}
	}
	pruneStaleBinaries(binDir, apps)
	return nil
}

// pruneStaleBinaries deletes executables in bin/ that this build no longer
// carries (apps removed from the release). App data dirs are intentionally
// left alone — they can hold user data; delete the data dir to reset.
func pruneStaleBinaries(binDir string, apps []*App) {
	keep := map[string]bool{}
	for _, a := range apps {
		if a == nil || a.ServedBy == "launcher" || a.Binary == "" {
			continue
		}
		if _, err := fs.Stat(embeddedFS, "embedded/bin/"+a.Binary); err != nil {
			continue // not actually packaged — its stale exe may be pruned
		}
		keep[embeddedBinaryName(a.Binary)] = true
	}
	entries, err := os.ReadDir(binDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || keep[e.Name()] {
			continue
		}
		_ = os.Remove(filepath.Join(binDir, e.Name()))
	}
}

// embeddedBinaryName maps the packaged binary name to the on-disk name.
// Binaries are packaged without an extension; Windows needs .exe.
func embeddedBinaryName(binary string) string {
	if runtime.GOOS == "windows" && !strings.HasSuffix(binary, ".exe") {
		return binary + ".exe"
	}
	return binary
}

// extractFile copies one file out of the embed. overwrite=false keeps an
// existing file on disk.
func extractFile(src embed.FS, name, dst string, overwrite bool) error {
	data, err := fs.ReadFile(src, name)
	if err != nil {
		return fmt.Errorf("%s is missing from this build (did release.sh stage it?)", name)
	}
	if !overwrite {
		if _, err := os.Stat(dst); err == nil {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o755)
}

// extractTree copies a whole directory from the embed. overwrite=false keeps
// every existing file on disk.
func extractTree(src embed.FS, dir, dst string, overwrite bool) error {
	entries, err := fs.ReadDir(src, dir)
	if err != nil {
		return nil // no assets packaged for this app (embedded-asset apps) — fine
	}
	for _, e := range entries {
		s := dir + "/" + e.Name()
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := extractTree(src, s, d, overwrite); err != nil {
				return err
			}
			continue
		}
		if err := extractFile(src, s, d, overwrite); err != nil {
			return err
		}
	}
	return nil
}

// staticHandlerFor serves a hosted HTML app: from the exe's embedded assets
// in single-file builds, from the app's disk folder in dev builds. Both get
// no-store headers and the self-close watchdog injected into every page.
func staticHandlerFor(a *App) (http.Handler, error) {
	if embeddedBuild {
		sub, err := fs.Sub(embeddedFS, "embedded/assets/"+a.ID)
		if err != nil {
			return nil, err
		}
		if entries, err := fs.ReadDir(embeddedFS, "embedded/assets/"+a.ID); err != nil || len(entries) == 0 {
			return nil, fmt.Errorf("no packaged assets for app %q", a.ID)
		}
		return noCacheHeaders(injectWatchdog(http.FileServer(http.FS(sub)))), nil
	}
	if a.Dir == "" {
		return nil, fmt.Errorf("app %q has no folder to serve", a.ID)
	}
	if entries, err := os.ReadDir(a.Dir); err != nil || len(entries) == 0 {
		return nil, fmt.Errorf("nothing to serve in %s", a.Dir)
	}
	return noCacheHeaders(injectWatchdog(http.FileServer(http.Dir(a.Dir)))), nil
}

// appWatchdogJS: injected into hosted app pages so an open tab/window closes
// itself when the app's server goes away (Stop, Stop All, launcher quit) —
// even if the dashboard that opened it was reloaded and lost track of it.
// Browsers only honor window.close() for script-opened windows (exactly what
// the dashboard's Open button creates); a tab the user opened by hand just
// stays, which is the correct behavior anyway.
const appWatchdogJS = `<script>(function(){
var f=0;
window.setInterval(function(){
  fetch(location.origin+"/__launcher_watchdog__?t="+Date.now(),{cache:"no-store"})
    .then(function(){f=0})
    .catch(function(){if(++f>=2){window.setInterval(function(){try{window.close()}catch(e){}},400)}});
},2000);
})();</script>`

// injectWatchdog buffers 200 text/html responses and appends the watchdog
// script before </body>. Everything else streams through untouched.
func injectWatchdog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		wr := &watchResponse{ResponseWriter: w}
		defer func() {
			if !wr.hold {
				return
			}
			body := injectBeforeBodyEnd(wr.buf.Bytes(), appWatchdogJS)
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(wr.code)
			_, _ = w.Write(body)
		}()
		next.ServeHTTP(wr, r)
	})
}

// injectBeforeBodyEnd inserts inject before the last "</body>" (case
// insensitive); without one it appends at the end.
func injectBeforeBodyEnd(html []byte, inject string) []byte {
	lower := bytes.ToLower(html)
	idx := bytes.LastIndex(lower, []byte("</body>"))
	out := make([]byte, 0, len(html)+len(inject))
	if idx < 0 {
		out = append(out, html...)
		return append(out, inject...)
	}
	out = append(out, html[:idx]...)
	out = append(out, inject...)
	return append(out, html[idx:]...)
}

// watchResponse decides on the first WriteHeader whether the response is a
// bufferable HTML body (inject) or passes straight through.
type watchResponse struct {
	http.ResponseWriter
	hold bool
	code int
	buf  bytes.Buffer
	done bool
}

func (w *watchResponse) WriteHeader(code int) {
	if w.done {
		return
	}
	w.done = true
	if code == http.StatusOK && strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		w.hold = true
		w.code = code
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *watchResponse) Write(p []byte) (int, error) {
	if !w.done { // Write implies 200 when WriteHeader wasn't called
		w.WriteHeader(http.StatusOK)
	}
	if w.hold {
		w.buf.Write(p)
		return len(p), nil
	}
	return w.ResponseWriter.Write(p)
}

func (w *watchResponse) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok && !w.hold {
		f.Flush()
	}
}

// noCacheHeaders keeps the browser honest while apps are being updated.
func noCacheHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}
