# Project Launcher — Go port

A Go (stdlib-only) launcher that starts/stops/opens every local web app from
one dashboard — and, in its release form, a **single `launcher.exe`** that
carries every app inside it, opens its own native window, and quits cleanly
when that window closes.

| Python source | Go source |
|---|---|
| `../server.py` (581 lines) | `main.go` (+ `sys_windows.go` / `sys_unix.go` for process/syscall bits, `window.go` for window mode, `embed.go` for single-file mode) |

The HTTP surface:

```
GET  /                  dashboard (index.html)
GET  /api/apps          status of every app
POST /api/start   {id}  start one app
POST /api/stop    {id}  stop one app (also stops instances started elsewhere)
POST /api/all/start     start everything that is stopped
POST /api/all/stop      stop everything that is running
GET  /api/logs?id=…     tail of an app's log
GET  /api/health        launcher self-report (LAN URL, build mode, data dir)
POST /api/heartbeat     dashboard keepalive (?bye=1 = page closed; window mode)
POST /api/shutdown      quit the launcher + stop its apps (loopback callers only)
GET  /favicon.ico       204
```

## Two build modes

**Folder mode** (plain `go build` / `sh build.sh`): the development layout.
Apps live in sibling folders next to `Launcher/`; `apps.json` configures them;
a missing binary is auto-built with `go build`; folders with an `index.html`
or a Go module that appear beside `Launcher/` are discovered automatically.
The dev `launcher.exe`/`launcher-console.exe` built by `build.sh` use this
mode.

**Single-file mode** (`-tags embed`, what `release.sh` builds): every app
binary and web asset is packed inside the executable.

- Startup unpacks binaries (always) and each disk-asset app's files (only if
  missing) into a per-user data dir: `%LOCALAPPDATA%\ProjectLauncher`
  (`~/Library/Application Support/ProjectLauncher` on macOS,
  `~/.local/share/ProjectLauncher` on Linux, `LAUNCHER_DATA_DIR` overrides).
  Nothing is written next to the exe.
- The 7 plain HTML apps (Cheatsheet, Tarot, Exercise, Astro, MLOPS, Kundli
  Explorer, House Design) are hosted **inside** the launcher process — no
  child process at all; Start/Stop simply opens/closes their port.
- The 7 Go apps run from the unpacked binaries, exactly as before.
- The packaged app registry is generated from a folder scan at build time
  (see "Adding or removing apps"); folder discovery is
  disabled (the launcher never scans the user's folders).
- To fully reset a client install: delete the data directory.
  Note: app asset files on disk are only written when missing, so user data
  (Investment's `data.js`, LawBuddy's db) survives launcher updates; delete
  the data dir to refresh app assets after an update.

## The window (release builds)

Release builds (`-tags gui`, Windows adds `-ldflags "-H windowsgui"`) have no
console at all; output goes to `logs/launcher.log` inside the data dir, and
fatal startup errors (port busy, unpack failure) pop a message box.

Window order on launch:

1. **Native window** (Windows, default): a real Win32 window hosting WebView2
   (`github.com/jchv/go-webview2`, pure Go — no cgo, runtime preinstalled on
   Windows 10/11). Its close button quits the launcher and stops its apps —
   there is no console process behind it to keep alive.
2. **App window** (`--browser` or if WebView2 is unavailable): a chromeless
   Edge/Chrome `--app=` window (falls back to the default browser tab).
   In this mode the dashboard heartbeats every 4 s and closing the last
   dashboard (or 8 min of silence after a crash) stops the launcher and its
   apps, so closing the window still closes the application.
3. `--no-window` / `NO_OPEN=1`: no window; server keeps running (service use).

Dashboards opened on other devices (Wi-Fi link in the header) count as
activity and keep the launcher alive; **Quit** in the dashboard does an
explicit `POST /api/shutdown` (loopback-only — phones can stop apps but not
kill the launcher).

## Opening apps: window or tab

The header's **🪟 Window / 🗂 Browser tab** button chooses where apps open
when you click **Open ↗** (remembered per device in browser storage):

- **Window** (default): each app opens in its own window — a second window of
  the launcher's WebView2 when running natively, a popup window in browsers.
- **Browser tab**: the app opens as a tab in your default browser. From the
  native launcher window (which has no tabs) the launcher hands the URL to
  the default browser itself via `POST /api/open` (loopback-only); from a
  phone or a regular browser it's a plain new tab.

**Closing on stop:** the dashboard keeps a reference to every window/tab it
opened, and **Stop**, **Stop All** and **Quit** close them along with the
app's server. Launcher-hosted HTML apps additionally carry an injected
watchdog (a tiny script in every served page): when their server goes away
the open tab/window closes itself — so even app windows opened before a
dashboard reload get cleaned up. Two honest limits, imposed by browsers:
tabs the launcher sent to your **default browser** (tab mode from the native
window) live in another browser process and can't be closed remotely — they
show a connection error once the app stops; and windows opened by a page
other than the dashboard (typed by hand) are intentionally left alone.

Study Hub used to open a browser tab by itself whenever it started; it no
longer does — the launcher's Open button decides. (Run `studyapp -open` to
restore the standalone auto-open.)

## Windows Firewall prompts ("Allow access?")

Any exe that listens on the LAN (0.0.0.0) triggers Windows Defender
Firewall's Allow/Cancel dialog once per binary — Windows does not allow
skipping this from a non-elevated process. The dashboard's
**🔓 Wi-Fi permission** button (Windows only) collapses all of them into
**one** prompt: it generates a PowerShell script that adds inbound
allow-rules for the launcher and every network app and runs it through a
single UAC elevation (`POST /api/firewall`, loopback-only). Approve once and
no app asks again. Rules are named `ProjectLauncher: <app>` — remove them in
Windows Firewall settings if you ever want the prompts back.

## Icon

`icon.ico` (rocket on the dashboard gradient) is embedded into every Windows
build via the `rsrc_windows_{amd64,386,arm64}.syso` files in this folder —
Explorer, the taskbar and the native window (title bar + taskbar) all use it.
To change the icon: edit `tools/makeicon.py`, run `python tools/makeicon.py`
(needs Pillow), then regenerate the .syso files:

    go run github.com/akavel/rsrc@latest -arch amd64 -ico icon.ico -o rsrc_windows_amd64.syso
    go run github.com/akavel/rsrc@latest -arch 386   -ico icon.ico -o rsrc_windows_386.syso
    go run github.com/akavel/rsrc@latest -arch arm64 -ico icon.ico -o rsrc_windows_arm64.syso

(## Adding or removing apps — just folders (then re-releasing))

The app set is **discovered automatically**. `tools/genembedded` scans the
project root at build time (every folder with a `go.mod` or an HTML entry
page, except `Launcher/`) and generates the packaged
registry + build manifest. There is no per-app list to maintain anywhere —
`apps-embedded.json` does not even exist in the repo, it is generated into
the exe on every build.

**Add an app:** create a folder under the project root —
- a plain HTML folder (any `.html` at its root) → hosted in-process, or
- a Go module (`go.mod`) → compiled and shipped as a binary; if a root `.go`
  file uses `//go:embed` it ships binary-only, otherwise its web assets
  (root `index.html`, or `templates/ static/ web/ css/ js/ data/` dirs) are
  packaged beside it. Entrypoint is the root `package main`, or the single
  `cmd/<name>/main.go`.

**Remove an app:** delete (or move away) its folder. The rebuilt exe does
not contain it at all — no binary, no assets, no dashboard card.

Then `sh release.sh` → new `launcher-<os>-<arch>.exe`. Clients just replace
their exe; stale binaries are pruned from their data dir on next start
(their `apps/<id>/` data folder is kept until the data dir is deleted).

**Customize an app** (name, icon, description, port, open path, Wi-Fi on/off,
custom CLI args): add/edit its entry in **`Launcher/apps.json`** — the one
registry worth maintaining; `"dir": "../<FolderName>"` ties it to the folder.
Folders not listed there still ship with defaults (📦 icon, next free port
from 9100, opens at `/`). An apps.json entry whose folder is gone is dropped
from the release with a printed notice and skipped with a warning in dev.

**Change ports / descriptions / icons:** edit `apps.json`, rebuild, done.

## Releasing to clients (one file)

```bash
cd Launcher
sh release.sh                    # all default targets
sh release.sh windows/amd64      # one target
```

**Where you can run it:** any of the three desktop OSes — the script is plain
POSIX sh and every target is cross-compiled (`CGO_ENABLED=0`, pure Go), so a
Linux box can produce Windows/Mac exes and vice versa. On Windows run it from
**Git Bash**, or just double-click **`release.cmd`** (same for `build.cmd`);
both need only Go and Git for Windows on PATH. A macOS host additionally
ad-hoc signs the darwin builds.

Targets: `windows/{amd64,arm64,386}`, `linux/{amd64,arm64,arm}`,
`darwin/{amd64,arm64}`.

Output under `Launcher/release/`:

- `launcher-<os>-<arch>[.exe]` — THE deliverable. ~80 MB, everything inside.
- `SHA256SUMS`.

`release.sh` runs `tools/genembedded` to scan the project root, stages
`Launcher/embedded/` (per-target binaries from each app module + assets + the
generated registry), builds with `-tags "gui embed"`, then restores the
placeholder. Misconfigurations degrade gracefully: an app whose binary never
got staged, or whose folder vanished, is skipped with a warning at startup
and never keeps the launcher from booting the rest.

For real distribution: sign the exe (`signtool /fd sha256`) to avoid
SmartScreen warnings; unsigned macOS builds need `xattr -dr
com.apple.quarantine <path>` on first run.

## Dev: build & run

```bash
cd Launcher
sh build.sh --console    # launcher.exe (GUI dev) + launcher-console.exe + all app binaries
go run .                 # folder mode, console, opens the dashboard
go run . --start-apps          # also boot every stopped app after 0.8 s
go run . --no-open             # don't open anything (or NO_OPEN=1)
go run . --window              # window mode on a console build
go run . --browser             # even in GUI builds, use an app window not the native one
go run . --kill-apps-on-exit   # Ctrl+C also stops the apps this panel started
PORT=8080 HOST=127.0.0.1 go run .
```

`--kill-apps-on-exit` (off by default in folder mode): on Ctrl+C or closing
the console window, the launcher stops every app *it started* (tracked in
`state.json`) before exiting; apps running without a launcher state entry are
left alone. Windows gives a console ~5 s on window close, so with many apps
running at once a force-closed window may finish only some of the stops — the
dashboard Stop buttons / `POST /api/stop` remain the reliable path.

## Python → Go mapping

| Python (`server.py`) | Go (`main.go` et al.) |
|---|---|
| `ThreadingHTTPServer` + `BaseHTTPRequestHandler.do_GET/do_POST` | `http.Server` + `Server.ServeHTTP`/`route` (raw-path matching, JSON 404 fallback) |
| `_json()` helper (Content-Type/Length, `Cache-Control: no-store`) | `writeJSON()` — same headers, plus `Server: ProjectLauncher/1.0` |
| `APPS = load_apps()` at import, `APP_BY_ID` | `NewServer(cfg)` → `loadApps`, duplicate-id / missing-folder errors abort startup with the same messages |
| `load_state` / `save_state` (tmp file + replace, `indent=1`) | `loadState` / `saveState` (`os.WriteFile` + `os.Rename`) |
| `lan_ip()` (PowerShell → macOS `ipconfig` → UDP 8.8.8.8 trick, 30 s cache) | `lanIP()` — identical command sequence and fallbacks |
| `port_open()` | `portOpen()` (`net.DialTimeout`) |
| `pid_alive()` — Windows `tasklist /FO CSV /NH` parse / POSIX `kill(pid,0)` | `pidAlive` in `sys_windows.go` / `sys_unix.go` |
| `listener_pids()` — Windows `netstat -ano -p tcp` parse / POSIX `lsof` + `_owns_and_listens` | `listenerPIDs` + `ownsAndListens` |
| `proc_cmdline()` — PowerShell `Get-CimInstance` / `ps -o command=` with 58-char truncation | `procCmdline` per platform |
| `subprocess.Popen(cmd.exe /d /s /c …` or `/bin/sh -c …`, `start_new_session`, `CREATE_NEW_PROCESS_GROUP|CREATE_NO_WINDOW`) | `os/exec` + `shellCommand` + `SysProcAttr` per platform (`Setpgid` on POSIX) |
| `_kill_group` / escalation (`taskkill /T /F`, `killpg SIGTERM`→`SIGKILL`) | `killTree` / `killTreeForce` / `killProc` / `killProcForce` |
| `MUTEX` + `INFLIGHT` set | `sync.Mutex` + `inflight` map (`busy` responses preserved) |
| `tail_log(errors="replace", last 150)` | `tailLog` + `splitLines` (UTF-8 replacement, last 150) |
| `PORT` / `HOST` env, default `0.0.0.0:9090` | identical (`DefaultConfig`) |
| `--start-apps`, `--no-open`, `NO_OPEN=1`, `webbrowser.open` | same flags/env, `openBrowser` (cmd/open/xdg-open) |
| banner prints, port-busy exit 1, Ctrl+C farewell | same strings in `main()` |

Response JSON field names are byte-for-byte the same (`ok`, `busy`, `alreadyRunning`, `error`, `pid`, `slow`, `note`, `killed`, `stillRunning`, `status`, `id`, `name`, `icon`, `desc`, `category`, `port`, `url`, `cmd`, `network`, `lanUrl`, `running`, `managed`, `external`, `proc`, `startedAt`). Pointer fields are used so a key is present exactly when the Python dict would include it (e.g. `"killed": []` stays visible, `"lanUrl": null` stays null).

## Path resolution

The Go launcher resolves its files at runtime from the executable directory instead of using a compile-time source path. LAUNCHER_ROOT=/path/to/Launcher overrides the asset root for packaged deployments.

When an app has a `binary` entry but no matching executable for the current
platform, the launcher automatically runs `go build` in that app's module.
Modules with a root `main.go` are built directly; modules using a single
`cmd/<name>/main.go` entrypoint are detected automatically. Existing binaries
are always preferred, so normal startup does not rebuild applications. Binaries
are named `<configured-name>-<GOOS>-<GOARCH>[.exe]`; unqualified binaries are never
used, preventing a binary built for another platform from being executed.

The launcher scans sibling project folders at startup. Any folder containing a
`go.mod` or an HTML entry page is added when it is not already listed in
`apps.json`. Go modules are built using the same automatic build logic as
configured apps; HTML projects are hosted in-process by the launcher. Each
discovered app receives
the next free port from `9000` and is shown in the dashboard. `apps.json` is
optional and acts as an override layer for custom metadata, commands, and fixed
ports; configured entries always take priority over discovery.


## Deviations from server.py

- Errors Python would have turned into a handler traceback (log-file open failure, `Popen` spawn failure) return `{"ok": false, "error": …}` with HTTP 500 instead of dropping the connection.
- `HEAD` requests get a JSON 404 (Python's BaseHTTPRequestHandler answered 501 for unsupported methods).
- Log tails are decoded as UTF-8 with replacement characters; Python used the locale default encoding with `errors="replace"`.
- An invalid `Content-Length` header or a save-state failure no longer crashes the request.
- HTTP responses carry Go's default `Date`/`Content-Type` handling; only `Server: ProjectLauncher/1.0` is set explicitly (Python advertised its full server string).
- Only the `{port}` placeholder is supported in `cmd`/`env` values (the only one `apps.json` uses).
