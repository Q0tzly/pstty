package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureZshrcSkipsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZDOTDIR", dir)

	result, err := ensureZshrc()
	if err != nil {
		t.Fatalf("ensureZshrc: %v", err)
	}
	if !strings.Contains(result, "skipped") {
		t.Errorf("result = %q, want it to mention skipping a missing .zshrc", result)
	}
	if _, err := os.Stat(filepath.Join(dir, ".zshrc")); err == nil {
		t.Error("ensureZshrc created .zshrc when it didn't exist")
	}
}

func TestEnsureZshrcAppendsWhenMissingMarker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZDOTDIR", dir)
	path := filepath.Join(dir, ".zshrc")
	if err := os.WriteFile(path, []byte("alias ll='ls -l'\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := ensureZshrc()
	if err != nil {
		t.Fatalf("ensureZshrc: %v", err)
	}
	if !strings.Contains(result, "added") {
		t.Errorf("result = %q, want it to mention adding the snippet", result)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), zshMarker) {
		t.Errorf(".zshrc = %q, want it to contain %q", data, zshMarker)
	}
	if !strings.Contains(string(data), "alias ll='ls -l'") {
		t.Error("ensureZshrc clobbered existing .zshrc content")
	}
}

func TestEnsureZshrcIdempotent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZDOTDIR", dir)
	path := filepath.Join(dir, ".zshrc")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := ensureZshrc(); err != nil {
		t.Fatalf("first ensureZshrc: %v", err)
	}
	after1, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	result, err := ensureZshrc()
	if err != nil {
		t.Fatalf("second ensureZshrc: %v", err)
	}
	if !strings.Contains(result, "already configured") {
		t.Errorf("second run result = %q, want it to report already configured", result)
	}
	after2, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(after1) != string(after2) {
		t.Errorf("second run changed the file:\nbefore: %q\nafter:  %q", after1, after2)
	}
}

func TestEnsureStarshipCreatesFileAndDir(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "nested", "starship.toml")
	t.Setenv("STARSHIP_CONFIG", cfgPath)

	result, err := ensureStarship()
	if err != nil {
		t.Fatalf("ensureStarship: %v", err)
	}
	if !strings.Contains(result, "created") {
		t.Errorf("result = %q, want it to mention creating the file", result)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), starshipHeader) {
		t.Errorf("starship.toml = %q, want it to contain %q", data, starshipHeader)
	}
}

func TestEnsureStarshipAppendsToExistingConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "starship.toml")
	t.Setenv("STARSHIP_CONFIG", cfgPath)
	if err := os.WriteFile(cfgPath, []byte(`format = "$all"`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := ensureStarship()
	if err != nil {
		t.Fatalf("ensureStarship: %v", err)
	}
	if !strings.Contains(result, "added") {
		t.Errorf("result = %q, want it to mention adding the snippet", result)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), `format = "$all"`) {
		t.Error("ensureStarship clobbered existing starship.toml content")
	}
	if !strings.Contains(string(data), starshipHeader) {
		t.Error("ensureStarship didn't append the PSTTY_SESSION block")
	}
}

func TestEnsureStarshipIdempotentEvenWithCustomizedBlock(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "starship.toml")
	t.Setenv("STARSHIP_CONFIG", cfgPath)
	// A user who already added (and customized) the block by hand.
	custom := "[env_var.PSTTY_SESSION]\nformat = \"custom\"\n"
	if err := os.WriteFile(cfgPath, []byte(custom), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := ensureStarship()
	if err != nil {
		t.Fatalf("ensureStarship: %v", err)
	}
	if !strings.Contains(result, "already configured") {
		t.Errorf("result = %q, want it to report already configured", result)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != custom {
		t.Errorf("ensureStarship modified a customized block:\nbefore: %q\nafter:  %q", custom, data)
	}
}

func TestRunCombinesResultsFromBothSteps(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZDOTDIR", dir) // no .zshrc here: zsh step is skipped
	t.Setenv("STARSHIP_CONFIG", filepath.Join(dir, "starship.toml"))

	results, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("Run returned %d results, want 2: %v", len(results), results)
	}
	if !strings.HasPrefix(results[0], "zsh:") {
		t.Errorf("results[0] = %q, want it to start with \"zsh:\"", results[0])
	}
	if !strings.HasPrefix(results[1], "starship:") {
		t.Errorf("results[1] = %q, want it to start with \"starship:\"", results[1])
	}
}
