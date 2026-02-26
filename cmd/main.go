package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/slidebolt/plugin-alexa/pkg/bundle"
	framework "github.com/slidebolt/plugin-framework"
)

const lockPath = "/tmp/plugin-alexa.lock"
const manualBuildVersion = "alpha-2026-02-25.1"

func acquireLock() (*os.File, error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another instance is already running")
	}
	return f, nil
}

func releaseLock(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
	os.Remove(lockPath)
}

func main() {
	fmt.Printf("Starting Alexa Plugin... instance=%s\n", bundle.ProcessInstanceID())

	lock, err := acquireLock()
	if err != nil {
		fmt.Printf("Failed to acquire instance lock: %v\n", err)
		return
	}
	defer releaseLock(lock)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	framework.Init()
	framework.SetBuildInfo(framework.BuildInfo{
		AppName:       "plugin-alexa",
		ManualVersion: manualBuildVersion,
	})

	b, err := framework.RegisterBundle("plugin-alexa")
	if err != nil {
		fmt.Printf("Failed to register bundle: %v\n", err)
		return
	}

	p := bundle.NewPlugin()
	if err := p.Init(b); err != nil {
		fmt.Printf("Failed to init plugin: %v\n", err)
		return
	}

	fmt.Printf("Alexa Plugin is running. instance=%s\n", bundle.ProcessInstanceID())
	<-ctx.Done()
	fmt.Printf("Alexa Plugin shutting down. instance=%s\n", bundle.ProcessInstanceID())
	p.Shutdown()
}
