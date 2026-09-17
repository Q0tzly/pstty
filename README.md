# pstty

A minimal terminal-persistence tool: keep a shell alive behind a PTY on a
Unix domain socket so an SSH session can disconnect and reattach later, in
the spirit of `screen`/`tmux` but without pane management or the other
features this project doesn't need.

The PTY and the client/server transport are implemented directly on
`golang.org/x/sys/unix` syscalls, with no PTY library dependency.

## Install

```sh
go install github.com/Q0tzly/pstty/cmd/pst@latest
```

Or via [mise](https://mise.jdx.dev):

```sh
mise use -g go:github.com/Q0tzly/pstty/cmd/pst
```

Both install the `pst` binary (named after the `cmd/pst` directory —
`go install github.com/Q0tzly/pstty@latest` on its own would install a
binary named `pstty` instead).

## Usage

```sh
pst <name>       # attach to session <name>, creating it if it doesn't exist
pst ls           # list known sessions (attached / detached / dead)
pst kill <name>  # terminate session <name>
pst setup        # wire $PSTTY_SESSION into .zshrc and starship.toml
```

Detach from an attached session with `Ctrl+]`. The shell keeps running on
the server; reattach later with `pst <name>` to pick up where you left off.

Sessions live in a temp directory scoped to your uid
(`$TMPDIR/pst-<uid>/<name>.sock`), overridable with `$PSTTY_DIR`. Each
session's server logs to `<name>.log` next to its socket (session start,
attach/detach, teardown reason) for debugging after the fact.

The shell inside a session has `$PSTTY_SESSION` set to the session name.
Run `pst setup` to wire it into your shell:

- adds `[env_var.PSTTY_SESSION]` to your [starship](https://starship.rs)
  config (`$STARSHIP_CONFIG`, or `~/.config/starship.toml`, created if
  it doesn't exist yet) so the prompt shows which session you're in.
- adds `unsetopt PROMPT_SP` (guarded to only apply inside a pstty
  session) to `~/.zshrc`, if you have one, to stop zsh's own
  end-of-line `%` mark from showing up after commands whose output
  doesn't end in a newline — harmless, but easy to run into inside a
  session and unrelated to pstty itself.

Both edits are idempotent, so running `pst setup` again later (say,
after installing starship) picks up whatever wasn't there yet without
duplicating anything.

## Limitations

- Single client per session, no shared view: a second `pst <name>` takes
  over from whichever client was already attached (disconnecting it)
  instead of both seeing the session at once.
- No nesting: running `pst <name>` from inside an already-attached
  session is rejected. Detach (`Ctrl+]`) first.
- Scrollback is capped at the last 64 KiB of output produced while nobody
  was attached; anything beyond that is dropped, not replayed on reattach.

## Ideas not pursued

- Shared/multi-client view (several `pst <name>` on the same session at
  once, tmux `-x`-style): would need `pumpMaster` to broadcast to a set
  of clients instead of one, a policy for interleaving input from more
  than one client, and a policy for whose terminal size wins. Bigger
  than this project's scope for now, but not ruled out.

- Seamless session switching (running `pst B` from inside session A
  detaches A and attaches B on the same terminal, instead of nesting):
  not achievable by the attach client alone, since a `pst B` invoked
  from A's shell is a child process wired to A's PTY, with no path to
  the real terminal. The only way to make it work is tmux's approach: a
  single central server holding every session, with clients as thin
  renderers the server can repoint on command. That's a deliberate
  non-goal here — pstty is closer to `screen`, where each named session
  is its own independent process, precisely so one session's server
  crashing can't take every other session down with it. Revisiting this
  later means an architecture change, not a patch, so it's not something
  to do after this is in wider use. A nested `pst <name>` is rejected
  outright instead of letting it silently stack (see Limitations).

## License

MIT
