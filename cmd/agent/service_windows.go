//go:build windows
// SPDX-License-Identifier: Apache-2.0


package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/imogenlabs/eightly-agent/internal/config"
	"github.com/imogenlabs/eightly-agent/internal/server"
)

const (
	serviceName    = "EightlyAgent"
	serviceDisplay = "Eight.ly Agent"
	serviceDesc    = "Lightweight Docker management agent for Eight.ly OS"
)

type agentService struct {
	cfg *config.Config
}

func (s *agentService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Run(ctx, s.cfg)
	}()

	changes <- svc.Status{
		State:   svc.Running,
		Accepts: svc.AcceptStop | svc.AcceptShutdown,
	}
	log.Info().Msg("windows service running")

	for {
		select {
		case err := <-errCh:
			if err != nil {
				log.Error().Err(err).Msg("server exited with error")
				changes <- svc.Status{State: svc.StopPending}
				return false, 1
			}
			changes <- svc.Status{State: svc.StopPending}
			return false, 0

		case c := <-r:
			switch c.Cmd {
			case svc.Stop, svc.Shutdown:
				log.Info().Str("cmd", fmt.Sprintf("%d", c.Cmd)).Msg("service stop requested")
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-errCh:
				case <-time.After(15 * time.Second):
					log.Warn().Msg("shutdown timed out")
				}
				return false, 0

			case svc.Interrogate:
				changes <- c.CurrentStatus
			}
		}
	}
}

func isWindowsService() bool {
	is, err := svc.IsWindowsService()
	if err != nil {
		return false
	}
	return is
}

func runAsService(cfg *config.Config) {
	log.Info().Msg("starting as windows service")
	if err := svc.Run(serviceName, &agentService{cfg: cfg}); err != nil {
		log.Fatal().Err(err).Msg("service failed")
	}
}

func handleServiceCommand(cmd string) {
	var err error
	switch cmd {
	case "install":
		err = installService()
	case "uninstall":
		err = uninstallService()
	case "start":
		err = startService()
	case "stop":
		err = stopService()
	case "status":
		err = queryStatus()
	default:
		fmt.Fprintf(os.Stderr, "unknown service command: %s\n", cmd)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func installService() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err == nil {
		s.Close()
		return fmt.Errorf("service %s already exists", serviceName)
	}

	s, err = m.CreateService(serviceName, exePath, mgr.Config{
		DisplayName: serviceDisplay,
		Description: serviceDesc,
		StartType:   mgr.StartAutomatic,
	})
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}
	defer s.Close()

	fmt.Printf("service %s installed\n", serviceName)
	fmt.Println("WARNING: Set EIGHTLY_AGENT_SECRET as a system environment variable before starting the service.")
	return nil
}

func uninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %s not found: %w", serviceName, err)
	}
	defer s.Close()

	status, err := s.Query()
	if err == nil && status.State != svc.Stopped {
		_, _ = s.Control(svc.Stop)
		time.Sleep(2 * time.Second)
	}

	if err := s.Delete(); err != nil {
		return fmt.Errorf("failed to delete service: %w", err)
	}

	fmt.Printf("service %s removed\n", serviceName)
	return nil
}

func startService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %s not found: %w", serviceName, err)
	}
	defer s.Close()

	if err := s.Start(); err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}

	fmt.Printf("service %s started\n", serviceName)
	return nil
}

func stopService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %s not found: %w", serviceName, err)
	}
	defer s.Close()

	_, err = s.Control(svc.Stop)
	if err != nil {
		return fmt.Errorf("failed to stop service: %w", err)
	}

	fmt.Printf("service %s stop signal sent\n", serviceName)
	return nil
}

func queryStatus() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %s not found: %w", serviceName, err)
	}
	defer s.Close()

	status, err := s.Query()
	if err != nil {
		return fmt.Errorf("failed to query service: %w", err)
	}

	var state string
	switch status.State {
	case svc.Stopped:
		state = "stopped"
	case svc.StartPending:
		state = "start pending"
	case svc.StopPending:
		state = "stop pending"
	case svc.Running:
		state = "running"
	case svc.ContinuePending:
		state = "continue pending"
	case svc.PausePending:
		state = "pause pending"
	case svc.Paused:
		state = "paused"
	default:
		state = fmt.Sprintf("unknown (%d)", status.State)
	}

	fmt.Printf("service %s: %s\n", serviceName, state)
	return nil
}
