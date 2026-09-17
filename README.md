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
pst ls           # list known sessions
pst kill <name>  # terminate session <name>
```

Detach from an attached session with `Ctrl+]`. The shell keeps running on
the server; reattach later with `pst <name>` to pick up where you left off.

Sessions live in a temp directory scoped to your uid
(`$TMPDIR/pst-<uid>/<name>.sock`), overridable with `$PSTTY_DIR`.

The shell inside a session has `$PSTTY_SESSION` set to the session name,
so it can be added to a prompt (starship, powerlevel10k, etc.) to show
which session you're in.

## Limitations

- Single client per session: a second `pst <name>` while one is already
  attached is rejected rather than sharing the view.
- No output scrollback buffer: output produced while nobody is attached is
  dropped, not replayed on reattach.

## License

MIT
