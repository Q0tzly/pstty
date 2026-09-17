# pstty

A minimal terminal-persistence tool: keep a shell alive behind a PTY on a
Unix domain socket so an SSH session can disconnect and reattach later, in
the spirit of `screen`/`tmux` but without pane management or the other
features this project doesn't need.

The PTY and the client/server transport are implemented directly on
`golang.org/x/sys/unix` syscalls, with no PTY library dependency.

## Install

```sh
go install github.com/Q0tzly/pstty@latest
```

This installs the `pst` binary.

## Usage

```sh
pst <name>       # attach to session <name>, creating it if it doesn't exist
pst ls           # list known sessions (attached / detached / dead)
pst kill <name>  # terminate session <name>
```

Detach from an attached session with `Ctrl+]`. The shell keeps running on
the server; reattach later with `pst <name>` to pick up where you left off.

Sessions live in a temp directory scoped to your uid
(`$TMPDIR/pst-<uid>/<name>.sock`), overridable with `$PSTTY_DIR`.

The shell inside a session has `$PSTTY_SESSION` set to the session name,
so it can be added to a prompt (starship, powerlevel10k, etc.) to show
which session you're in. For [starship](https://starship.rs), add to
`~/.config/starship.toml`:

```toml
[env_var.PSTTY_SESSION]
variable = "PSTTY_SESSION"
format = "[pstty:$env_value]($style) "
style = "bold yellow"
```

## Limitations

- Single client per session, no shared view: a second `pst <name>` takes
  over from whichever client was already attached (disconnecting it)
  instead of both seeing the session at once.
- Scrollback is capped at the last 64 KiB of output produced while nobody
  was attached; anything beyond that is dropped, not replayed on reattach.

## License

MIT
