# Project Launcher — Go port

A faithful Go (stdlib-only) port of the Python launcher:

| Python source | Go source |
|---|---|
| `../server.py` (581 lines) | `main.go` (+ `sys_windows.go` / `sys_unix.go` for process/syscall bits) |
| `../test_api.py` (API end-to-end test) | `server_test.go` |

The HTTP surface is unchanged:

```
GET  /                  dashboard (index.html)
GET  /api/apps          status of every app
POST /api/start   {id}  start one app
POST /api/stop    {id}  stop one app (also stops instances started elsewhere)
POST /api/all/start     start everything that is stopped
POST /api/all/stop      stop everything that is running
GET  /api/logs?id=…     tail of an app's log
GET  /api/health        launcher self-report (LAN URL)
GET  /favicon.ico       204
```

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
are always preferred, so normal startup does not rebuild applications.

The launcher scans sibling project folders at startup. Any folder containing a
`go.mod` or an HTML entry page is added when it is not already listed in
`apps.json`. Go modules are built using the same automatic build logic as
configured apps; HTML projects use `StaticServer`. Each discovered app receives
the next free port from `9000` and is shown in the dashboard. `apps.json` is
optional and acts as an override layer for custom metadata, commands, and fixed
ports; configured entries always take priority over discovery.

## Build & run

```bash
cd Launcher
go build -o launcher.exe .      # or: go run .
./launcher.exe                  # http://127.0.0.1:9090
./launcher.exe --start-apps     # also boot every stopped app after 0.8 s
./launcher.exe --no-open        # don't open the browser (or NO_OPEN=1)
./launcher.exe --kill-apps-on-exit  # Ctrl+C / closing the window also stops the apps this panel started
PORT=8080 HOST=127.0.0.1 ./launcher.exe
```

`--kill-apps-on-exit` (Go-only addition, off by default): on Ctrl+C or closing
the console window, the launcher stops every app *it started* (tracked in
`state.json`) before exiting; apps running without a launcher state entry are
left alone. Windows gives a console ~5 s on window close, so with many apps
running at once a force-closed window may finish only some of the stops — the
dashboard Stop buttons / `POST /api/stop` remain the reliable path.

## Tests

```bash
cd Launcher
go test ./...
```

`test_api.py` required a live server on `127.0.0.1:9090` plus the 11 real apps of the author's machine. The Go port keeps every check but runs **in-process** (`httptest.NewServer(NewServer(cfg))`) against a synthetic `apps.json` in a temp dir — no external server or real apps needed. The "apps" are instances of the test binary itself, re-executed in helper mode (`LAUNCHER_HELPER=1` + `PORT=…` via `TestMain`), standing in for `python -m http.server`; one helper is started *outside* the launcher to exercise external detection and the external stop path exactly like the Python `mlops` section.

Adapted checks (noted inline in the test):
- app counts/ports come from the synthetic registry (3 Wi-Fi apps + 1 local) instead of the author's 11-app registry;
- the hard-coded `http://192.168.*` LAN assertion now verifies each Wi-Fi app's `lanUrl` against the machine's LAN as reported by `/api/health`, and is skipped offline;
- the `vedic`/`monitor` "already running externally" preconditions are provided by the self-spawned helper;
- page-load checks additionally assert the helper received the injected `PORT` and `{port}`-substituted env.

## Deviations from server.py

- Errors Python would have turned into a handler traceback (log-file open failure, `Popen` spawn failure) return `{"ok": false, "error": …}` with HTTP 500 instead of dropping the connection.
- `HEAD` requests get a JSON 404 (Python's BaseHTTPRequestHandler answered 501 for unsupported methods).
- Log tails are decoded as UTF-8 with replacement characters; Python used the locale default encoding with `errors="replace"`.
- An invalid `Content-Length` header or a save-state failure no longer crashes the request.
- HTTP responses carry Go's default `Date`/`Content-Type` handling; only `Server: ProjectLauncher/1.0` is set explicitly (Python advertised its full server string).
- Only the `{port}` placeholder is supported in `cmd`/`env` values (the only one `apps.json` uses).
