// +build windows

package main

import (
	"code.google.com/p/winsvc/svc"
	"fmt"
	"os"
)

type WindowsService struct {
}

func (ws *WindowsService) Execute(args []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (svcSpecificEC bool, exitCode uint32) {
	s <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPauseAndContinue}

	go runAgent()

loop:
	for {
		select {
		case change := <-r:
			switch change.Cmd {
			case svc.Interrogate:
				s <- change.CurrentStatus
			case svc.Stop, svc.Shutdown:
				{
					break loop
				}
			case svc.Pause:
				s <- svc.Status{State: svc.Paused, Accepts: svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPauseAndContinue}
			case svc.Continue:
				s <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPauseAndContinue}
			default:
				{
					break loop
				}
			}
		}
	}
	s <- svc.Status{State: svc.StopPending}
	return
}

func main() {
	console := false
	for _, a := range os.Args {
		if a == "-debug" {
			console = true
		}
	}

	if console {
		runAgent()
	} else {
		ws := WindowsService{}
		run := svc.Run

		err := run("deaagent", &ws)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}
}
