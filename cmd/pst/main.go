// Command pst is a minimal terminal-persistence tool: it keeps a shell
// running behind a PTY on a Unix domain socket so an SSH session can
// disconnect and reattach later, in the spirit of screen/tmux but without
// the pane-management features this project doesn't need.
package main

import (
	"fmt"
	"os"

	"github.com/Q0tzly/pstty/internal/client"
	"github.com/Q0tzly/pstty/internal/launch"
	"github.com/Q0tzly/pstty/internal/server"
	"github.com/Q0tzly/pstty/internal/session"
	"github.com/Q0tzly/pstty/internal/setup"
)

func main() {
	args := os.Args[1:]

	if name, ok := launch.IsServerCommand(args); ok {
		if err := server.Run(name); err != nil {
			fmt.Fprintln(os.Stderr, "pst:", err)
			os.Exit(1)
		}
		return
	}

	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	var err error
	switch args[0] {
	case "ls":
		err = runLs()
	case "setup":
		err = runSetup()
	case "watch":
		if len(args) != 2 {
			usage()
			os.Exit(2)
		}
		err = runWatch(args[1])
	case "kill":
		if len(args) != 2 {
			usage()
			os.Exit(2)
		}
		err = client.Kill(args[1])
	case "help", "-h", "--help":
		usage()
		return
	default:
		switch len(args) {
		case 1:
			err = runAttach(args[0])
		case 3:
			if args[1] != "-c" {
				usage()
				os.Exit(2)
			}
			runExec(args[0], args[2]) // exits the process itself
		default:
			usage()
			os.Exit(2)
		}
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "pst:", err)
		os.Exit(1)
	}
}

// detachKey resolves the detach key to use for an attach or watch, from
// $PSTTY_DETACH_KEY if set, else client.DefaultDetachByte.
func detachKey() (byte, error) {
	key := os.Getenv("PSTTY_DETACH_KEY")
	if key == "" {
		return client.DefaultDetachByte, nil
	}
	return client.ParseDetachKey(key)
}

func runAttach(name string) error {
	if cur := os.Getenv("PSTTY_SESSION"); cur != "" {
		return fmt.Errorf("already attached to %q; detach first (Ctrl+])", cur)
	}

	detach, err := detachKey()
	if err != nil {
		return err
	}
	if err := launch.EnsureRunning(name); err != nil {
		return err
	}
	replay := os.Getenv("PSTTY_NO_REPLAY") == ""
	return client.Attach(name, detach, replay)
}

func runWatch(name string) error {
	detach, err := detachKey()
	if err != nil {
		return err
	}
	return client.Watch(name, detach)
}

// runExec runs cmd in name's session and exits the process with the
// command's own exit code; it never returns normally.
func runExec(name, cmd string) {
	if err := launch.EnsureRunning(name); err != nil {
		fmt.Fprintln(os.Stderr, "pst:", err)
		os.Exit(1)
	}
	output, code, err := client.Exec(name, cmd)
	fmt.Print(output)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pst:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func runSetup() error {
	results, err := setup.Run()
	for _, r := range results {
		fmt.Println(r)
	}
	return err
}

func runLs() error {
	infos, err := session.List()
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		fmt.Println("no sessions")
		return nil
	}
	for _, info := range infos {
		status := "dead"
		if info.Alive {
			status = "detached"
			if info.Attached {
				status = "attached"
			}
		}
		fmt.Printf("%s\t%s\n", info.Name, status)
	}
	return nil
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  pst <name>            attach to session <name>, creating it if needed
  pst <name> -c <cmd>   run cmd in session <name> and print its output
  pst watch <name>      view session <name> read-only, without attaching
  pst ls                list known sessions
  pst kill <name>       terminate session <name>
  pst setup             wire $PSTTY_SESSION into .zshrc and starship.toml

detach from an attached or watched session with Ctrl+] ($PSTTY_DETACH_KEY
to use a different key, e.g. for a second pst nested over SSH).
$PSTTY_NO_REPLAY=1 skips the scrollback replay on attach.`)
}
