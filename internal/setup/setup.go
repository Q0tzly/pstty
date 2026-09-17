// Package setup wires pstty into a user's shell config: the zsh tweak
// that suppresses a cosmetic end-of-line mark inside a session, and the
// starship snippet that shows which session is active. Both edits are
// idempotent, so `pst setup` is safe to run again later (e.g. after
// installing starship, to pick up the prompt snippet at that point).
package setup

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	zshMarker  = `[[ -n "$PSTTY_SESSION" ]] && unsetopt PROMPT_SP`
	zshComment = "# Added by `pst setup` (github.com/Q0tzly/pstty): suppress zsh's \"%\" end-of-line mark inside a pstty session."

	starshipHeader = "[env_var.PSTTY_SESSION]"
	starshipBlock  = `# Added by ` + "`pst setup`" + ` (github.com/Q0tzly/pstty): show the active session in the prompt.
[env_var.PSTTY_SESSION]
variable = "PSTTY_SESSION"
format = "[pstty:$env_value]($style) "
style = "bold yellow"
`
)

// Run ensures both integrations are in place and returns a one-line
// result per step (added / already configured / skipped), regardless of
// whether the other step failed.
func Run() (results []string, err error) {
	zshResult, zshErr := ensureZshrc()
	results = append(results, "zsh: "+zshResult)

	starshipResult, starshipErr := ensureStarship()
	results = append(results, "starship: "+starshipResult)

	return results, errors.Join(zshErr, starshipErr)
}

func zshrcPath() (string, error) {
	dir := os.Getenv("ZDOTDIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = home
	}
	return filepath.Join(dir, ".zshrc"), nil
}

func starshipConfigPath() (string, error) {
	if p := os.Getenv("STARSHIP_CONFIG"); p != "" {
		return p, nil
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "starship.toml"), nil
}

// ensureZshrc appends the PROMPT_SP snippet to .zshrc if it isn't there
// already. It does nothing if .zshrc doesn't exist, treating that as a
// signal the user isn't using zsh rather than creating one from scratch.
func ensureZshrc() (string, error) {
	path, err := zshrcPath()
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return fmt.Sprintf("skipped, %s not found", path), nil
	}
	if err != nil {
		return "", err
	}

	if bytes.Contains(data, []byte(zshMarker)) {
		return fmt.Sprintf("already configured in %s", path), nil
	}

	if err := appendBlock(path, data, zshComment+"\n"+zshMarker+"\n"); err != nil {
		return "", err
	}
	return fmt.Sprintf("added to %s", path), nil
}

// ensureStarship appends the PSTTY_SESSION env_var snippet to the
// starship config, creating the file (and its directory) if needed.
func ensureStarship() (string, error) {
	path, err := starshipConfigPath()
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	existed := err == nil

	if bytes.Contains(data, []byte(starshipHeader)) {
		return fmt.Sprintf("already configured in %s", path), nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := appendBlock(path, data, starshipBlock); err != nil {
		return "", err
	}
	if existed {
		return fmt.Sprintf("added to %s", path), nil
	}
	return fmt.Sprintf("created %s", path), nil
}

// appendBlock appends block to the file at path (creating it if it
// doesn't exist), separating it from any existing content with a blank
// line.
func appendBlock(path string, existing []byte, block string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	prefix := ""
	if len(existing) > 0 {
		prefix = "\n"
		if existing[len(existing)-1] != '\n' {
			prefix = "\n\n"
		}
	}
	_, err = fmt.Fprint(f, prefix+block)
	return err
}
