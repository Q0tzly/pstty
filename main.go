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
		if len(args) != 1 {
			usage()
			os.Exit(2)
		}
		err = runAttach(args[0])
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "pst:", err)
		os.Exit(1)
	}
}

func runAttach(name string) error {
	if err := launch.EnsureRunning(name); err != nil {
		return err
	}
	return client.Attach(name)
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
			status = "running"
		}
		fmt.Printf("%s\t%s\n", info.Name, status)
	}
	return nil
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  pst <name>       attach to session <name>, creating it if needed
  pst ls           list known sessions
  pst kill <name>  terminate session <name>

detach from an attached session with Ctrl+]`)
}
