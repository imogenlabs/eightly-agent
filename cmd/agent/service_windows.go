//go:build windows

// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/imogenlabs/eightly-agent/internal/config"
	"github.com/imogenlabs/eightly-agent/internal/server"
	"github.com/imogenlabs/eightly-agent/internal/winacl"
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
	protectDataDirs(cfg)
	if err := svc.Run(serviceName, &agentService{cfg: cfg}); err != nil {
		log.Fatal().Err(err).Msg("service failed")
	}
}

// protectDataDirs closes the data and state folders to everyone but SYSTEM and
// Administrators before the agent writes anything into them. Under ProgramData
// a new folder otherwise lets every local user read what is put there, and
// State.Save runs before EnsureCert. The files themselves are protected one by
// one as they are written or loaded, so a folder that is skipped because it
// holds other things leaves no secret open.
func protectDataDirs(cfg *config.Config) {
	if err := os.MkdirAll(cfg.DataPath, 0o700); err != nil {
		log.Warn().Err(err).Str("dir", cfg.DataPath).Msg("cannot create the data folder")
	}
	for _, dir := range []string{cfg.DataPath, filepath.Dir(cfg.StatePath)} {
		done, err := winacl.ProtectDir(dir)
		switch {
		case err != nil:
			log.Warn().Err(err).Str("dir", dir).Msg("cannot restrict access to folder")
		case !done:
			log.Info().Str("dir", dir).Msg("folder holds files that are not the agent's and nobody else can change it; leaving its access list alone and protecting the agent's files one by one")
		}
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

// installService registers the service to read its secret and enrollment
// token from files rather than Machine environment variables, which every
// local user can read. The folders are created and locked down first, so
// files written into them afterwards inherit SYSTEM and Administrators access
// only.
func installService() error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	secretFile := fs.String("secret-file", config.DefaultSecretPath, "file the service reads the HMAC shared secret from")
	tokenFile := fs.String("enroll-token-file", config.DefaultEnrollTokenPath, "file the service reads the single-use enrollment token from")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}
	path, err := filepath.Abs(*secretFile)
	if err != nil {
		return err
	}
	tokenPath, err := filepath.Abs(*tokenFile)
	if err != nil {
		return err
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// The service runs with the default data folder and state file, so those
	// and the two files named here are the agent's; anything else in their
	// folders keeps its access list.
	winacl.SetAgentFiles(winacl.AgentFiles{
		State:       config.DefaultStatePath,
		Data:        config.DefaultDataPath,
		Secret:      path,
		EnrollToken: tokenPath,
	})
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create %s: %w", dir, err)
	}
	protected, err := winacl.ProtectDir(dir)
	if err != nil {
		return fmt.Errorf("failed to restrict access to %s: %w", dir, err)
	}
	if tokenDir := filepath.Dir(tokenPath); tokenDir != dir {
		if err := os.MkdirAll(tokenDir, 0o700); err != nil {
			return fmt.Errorf("failed to create %s: %w", tokenDir, err)
		}
		if _, err := winacl.ProtectDir(tokenDir); err != nil {
			return fmt.Errorf("failed to restrict access to %s: %w", tokenDir, err)
		}
	}
	haveSecret := false
	if _, err := os.Stat(path); err == nil {
		if _, err := config.ReadSecretFile(path); err != nil {
			return err
		}
		haveSecret = true
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to service manager: %w", err)
	}
	defer func() { _ = m.Disconnect() }()

	s, err := m.OpenService(serviceName)
	if err == nil {
		_ = s.Close()
		return fmt.Errorf("service %s already exists", serviceName)
	}

	s, err = m.CreateService(serviceName, exePath, mgr.Config{
		DisplayName: serviceDisplay,
		Description: serviceDesc,
		StartType:   mgr.StartAutomatic,
	}, "--secret-file", path, "--enroll-token-file", tokenPath)
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}
	defer func() { _ = s.Close() }()

	fmt.Printf("service %s installed; it reads its secret from %s and its enrollment token from %s\n", serviceName, path, tokenPath)
	if !protected {
		fmt.Printf("WARNING: %s holds files that are not the agent's, so its access list was left alone. The secret file itself is locked down when the service reads it.\n", dir)
	}
	if !haveSecret {
		fmt.Println("Write the shared secret to that file before starting the service (the folder is already closed to other users):")
		fmt.Printf("  Set-Content -NoNewline -Path '%s' -Value 'your-shared-secret'\n", path)
	}
	fmt.Println("Write the enrollment token from Eight.ly OS to its file before the first start, and delete the file once the agent has registered (the token is single-use):")
	fmt.Printf("  Set-Content -NoNewline -Path '%s' -Value 'your-enrollment-token'\n", tokenPath)
	if os.Getenv("EIGHTLY_ENROLL_TOKEN") != "" {
		fmt.Println("WARNING: EIGHTLY_ENROLL_TOKEN is set in the environment, where every local user can read it, and the service refuses to start while it is set alongside the file. Remove it:")
		fmt.Println("  [Environment]::SetEnvironmentVariable('EIGHTLY_ENROLL_TOKEN', $null, 'Machine')")
	}
	if os.Getenv("EIGHTLY_AGENT_SECRET") != "" {
		fmt.Println("WARNING: EIGHTLY_AGENT_SECRET is set in the environment. Every local user can read a Machine variable, and the service refuses to start while it is set alongside the file. Remove it:")
		fmt.Println("  [Environment]::SetEnvironmentVariable('EIGHTLY_AGENT_SECRET', $null, 'Machine')")
	}
	return nil
}

func uninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to service manager: %w", err)
	}
	defer func() { _ = m.Disconnect() }()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %s not found: %w", serviceName, err)
	}
	defer func() { _ = s.Close() }()

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
	defer func() { _ = m.Disconnect() }()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %s not found: %w", serviceName, err)
	}
	defer func() { _ = s.Close() }()

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
	defer func() { _ = m.Disconnect() }()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %s not found: %w", serviceName, err)
	}
	defer func() { _ = s.Close() }()

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
	defer func() { _ = m.Disconnect() }()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %s not found: %w", serviceName, err)
	}
	defer func() { _ = s.Close() }()

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
