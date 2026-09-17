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

Or via [mise](https://mise.jdx.dev), pinned to a released version — at
the time of writing, `@latest` fails in mise's `go` backend with
`no versions found ... matching date filter`, seemingly a bug on mise's
side, unrelated to this project:

```sh
mise use -g go:github.com/Q0tzly/pstty/cmd/pst@0.2.0
```

Both install the `pst` binary, named after the `cmd/pst` directory (the
bare module path, `github.com/Q0tzly/pstty`, isn't installable on its
own — it has no `main` package at its root).

## Usage

```sh
pst <name>            # attach to session <name>, creating it if it doesn't exist
pst <name> -c <cmd>   # run cmd in session <name> and print its output
pst watch <name>      # view session <name> read-only, without attaching
pst ls                # list known sessions (attached / detached / dead)
pst kill <name>       # terminate session <name>
pst setup             # wire $PSTTY_SESSION into .zshrc and starship.toml
```

Detach from an attached or watched session with `Ctrl+]`. The shell keeps
running on the server; reattach later with `pst <name>` to pick up where
you left off.

`pst <name> -c <cmd>` runs a single command non-interactively — no real
terminal required, so it works from a plain pipe or another program
driving it, not just a shell someone's typing into. It base64-encodes
`cmd` and pipes it through `bash` on the far side (so quoting in `cmd`
can't collide with the raw keystroke stream), then watches for a random
completion marker to know when it's done and to recover the real exit
code, which becomes `pst`'s own exit code. It evicts an existing
attached client the same way a normal attach would and disconnects as
soon as the command finishes, rather than staying attached.

`pst watch <name>` opens a read-only view: you see everything the
attached client sees, live, but anything you type is discarded rather
than reaching the shell, and it never takes over (or evicts) the
attached client. Useful for watching another person's — or an agent's —
session without any risk of interfering with it. To take over instead
of just watching, detach and run a normal `pst <name>`.

Reattaching (or `pst watch`) replays the session's recent scrollback so
it isn't silent, followed by a `--- live ---` marker so replayed history
isn't mistaken for what's happening right now (a scrollback line that
happens to read `Password:`, say). Set `$PSTTY_NO_REPLAY=1` to skip the
replay entirely for a lighter reattach that only shows what happens from
here on — `pst <name> -c` always does this, since a one-shot command has
no reason to see old history.

If you `pst` into a session on one machine and then, from inside it, SSH
to a second machine and `pst` into a session there too, `Ctrl+]` always
detaches the outer (first) one — it reads your keystrokes before the
inner one ever sees them, and `$PSTTY_SESSION` can't be seen across the
SSH hop to guard against it. Give the inner attach a different detach
key with `$PSTTY_DETACH_KEY` (caret notation, e.g. `^^` for Ctrl+^):

```sh
PSTTY_DETACH_KEY='^^' pst <name>
```

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

- Single read-write client per session: a second `pst <name>` takes over
  from whichever client was already attached (disconnecting it, with a
  message to both sides) instead of both being able to type into the
  session at once. `pst watch <name>` gives read-only access to any
  number of others at the same time; only writing is exclusive.
- No nesting: running `pst <name>` from inside an already-attached
  session is rejected. Detach (`Ctrl+]`) first.
- Scrollback is capped at the last 64 KiB of output produced while nobody
  was attached; anything beyond that is dropped, not replayed on reattach.

## Ideas not pursued

- Shared multi-*writer* view (more than one `pst <name>` typing into the
  same session at once, tmux `-x`-style): would need a policy for
  interleaving input from more than one client and a policy for whose
  terminal size wins, on top of the broadcast-to-many-clients plumbing
  `pst watch` already added for the read-only case. Bigger than this
  project's scope for now, but not ruled out.

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
