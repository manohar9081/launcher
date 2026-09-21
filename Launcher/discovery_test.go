package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppsDiscoversStaticSibling(t *testing.T) {
	root := t.TempDir()
	launcher := filepath.Join(root, "Launcher")
	discoveredDir := filepath.Join(root, "New Web App")
	if err := os.MkdirAll(launcher, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(discoveredDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(discoveredDir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry, err := json.Marshal(map[string]any{"apps": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	appsFile := filepath.Join(launcher, "apps.json")
	if err := os.WriteFile(appsFile, registry, 0o644); err != nil {
		t.Fatal(err)
	}

	apps, byID, err := loadApps(Config{Root: launcher, AppsFile: appsFile})
	if err != nil {
		t.Fatal(err)
	}
	app, ok := byID["new-web-app"]
	if !ok || len(apps) != 1 {
		t.Fatalf("expected one discovered app, got %d (%v)", len(apps), byID)
	}
	if app.RawDir != "../New Web App" || app.Port != 9000 {
		t.Fatalf("unexpected discovered app: %+v", app)
	}
}

func TestLoadAppsDiscoversGoModuleAndNonIndexHTML(t *testing.T) {
	root := t.TempDir()
	launcher := filepath.Join(root, "Launcher")
	goDir := filepath.Join(root, "Go Tool")
	htmlDir := filepath.Join(root, "Exercise")
	for _, dir := range []string{launcher, goDir, htmlDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(goDir, "go.mod"), []byte("module example\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goDir, "main.go"), []byte("package main\nimport _ \"net/http\"\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(htmlDir, "exercise-trainer.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	apps, byID, err := loadApps(Config{Root: launcher, AppsFile: filepath.Join(launcher, "missing.json")})
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 2 {
		t.Fatalf("expected two discovered apps, got %d (%v)", len(apps), byID)
	}
	goApp := byID["go-tool"]
	if goApp == nil || goApp.Binary != "../Go Tool/go-tool-launcher" || goApp.Desc != "Discovered Go app" {
		t.Fatalf("unexpected Go app: %+v", goApp)
	}
	htmlApp := byID["exercise"]
	if htmlApp == nil || htmlApp.URLPath != "/exercise-trainer.html" {
		t.Fatalf("unexpected HTML app: %+v", htmlApp)
	}
}

func TestDiscoverableProjectSkipsCLIModules(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module cli\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if kind, _ := discoverableProject(root); kind != "" {
		t.Fatalf("CLI module should not be discovered as a web app: %q", kind)
	}
}

func TestDiscoverableProjectFindsServerGo(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module server\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "server.go"), []byte("package main\nimport \"net/http\"\nfunc main() { http.ListenAndServe(\":0\", nil) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if kind, _ := discoverableProject(root); kind != "go" {
		t.Fatalf("server.go module should be discovered: %q", kind)
	}
}

func TestEnsureExecutableRepairsMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app")
	if err := os.WriteFile(path, []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureExecutable(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("binary is still not executable: %o", info.Mode().Perm())
	}
}

func TestBuildGoBinaryUsesCmdForLibraryRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "server.go"), []byte("package monitor\n\ntype Server struct{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmdDir := filepath.Join(root, "cmd", "monitor")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "monitor")
	built, err := buildGoBinary(output)
	if err != nil {
		t.Fatal(err)
	}
	if built != output {
		t.Fatalf("unexpected build result: %q", built)
	}
	if info, err := os.Stat(output); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("built binary is not executable: %v", err)
	}
}
