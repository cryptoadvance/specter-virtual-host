//go:build windows

package main

import (
	"golang.org/x/sys/windows/svc"
)

type windowsServiceProgram struct {
	run func(<-chan struct{}, chan<- error) error
}

func runWindowsService(run func(<-chan struct{}, chan<- error) error) (bool, error) {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return true, err
	}
	if !isService {
		return false, nil
	}
	return true, svc.Run("SpecterVirtualHost", windowsServiceProgram{run: run})
}

func (p windowsServiceProgram) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	stop := make(chan struct{})
	ready := make(chan error, 1)
	done := make(chan error, 1)
	go func() { done <- p.run(stop, ready) }()
	status := svc.Status{State: svc.StartPending, CheckPoint: 1, WaitHint: 10_000}
	changes <- status
	started := false
	for !started {
		select {
		case err := <-ready:
			if err != nil {
				<-done
				return false, 1
			}
			started = true
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				changes <- status
			case svc.Stop, svc.Shutdown:
				close(stop)
				if err := <-done; err != nil {
					return false, 1
				}
				return false, 0
			}
		}
	}

	status = svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	changes <- status
	for {
		select {
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				changes <- status
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending, CheckPoint: 1, WaitHint: 10_000}
				close(stop)
				if err := <-done; err != nil {
					return false, 1
				}
				return false, 0
			}
		case err := <-done:
			if err != nil {
				return false, 1
			}
			return false, 0
		}
	}
}
