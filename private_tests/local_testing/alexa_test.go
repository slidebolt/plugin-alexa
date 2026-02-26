package local_testing

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	framework "github.com/slidebolt/plugin-framework"
	"github.com/slidebolt/plugin-alexa/pkg/bundle"
)

func init() {
	loadEnv(".env")
}

func loadEnv(filename string) {
	file, err := os.Open(filename)
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		os.Setenv(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
	}
}

// TestAlexaConnect dials the real relay using credentials from .env and asserts
// the plugin reaches a connected state within 5 seconds.
func TestAlexaConnect(t *testing.T) {
	requireEnv(t, "ALEXA_WS_ENDPOINT", "ALEXA_CLIENT_SECRET", "ALEXA_CLIENT_ID")

	b, err := framework.RegisterBundle("plugin-alexa-connect-test")
	if err != nil {
		t.Fatalf("RegisterBundle: %v", err)
	}
	defer os.RemoveAll("state")

	p := bundle.NewPlugin()
	if err := p.Init(b); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer p.Shutdown()

	fmt.Println("Waiting for Alexa relay connection...")
	deadline := time.After(5 * time.Second)
	for {
		if p.IsConnected() {
			break
		}
		select {
		case <-deadline:
			t.Fatal("not connected within 5s — check credentials and relay endpoint")
		case <-time.After(100 * time.Millisecond):
		}
	}

	fmt.Printf("Connected to Alexa relay at %s\n", os.Getenv("ALEXA_WS_ENDPOINT"))
}

// TestAlexaShutdown verifies that Shutdown() returns cleanly within 3 seconds
// (no goroutine leaks from the relay or sync loops).
func TestAlexaShutdown(t *testing.T) {
	requireEnv(t, "ALEXA_WS_ENDPOINT", "ALEXA_CLIENT_SECRET", "ALEXA_CLIENT_ID")

	b, err := framework.RegisterBundle("plugin-alexa-shutdown-test")
	if err != nil {
		t.Fatalf("RegisterBundle: %v", err)
	}
	defer os.RemoveAll("state")

	p := bundle.NewPlugin()
	if err := p.Init(b); err != nil {
		t.Fatalf("Init: %v", err)
	}

	done := make(chan struct{})
	go func() { p.Shutdown(); close(done) }()

	select {
	case <-done:
		// Shutdown returned cleanly — connection loop and sync loop exited.
	case <-time.After(3 * time.Second):
		t.Error("Shutdown did not return within 3s — goroutine may have leaked")
	}
}

// TestAlexaListDevices triggers list_devices and waits long enough for the relay
// to respond, then passes — the comparison table appears in test output.
func TestAlexaListDevices(t *testing.T) {
	requireEnv(t, "ALEXA_WS_ENDPOINT", "ALEXA_CLIENT_SECRET", "ALEXA_CLIENT_ID")

	b, err := framework.RegisterBundle("plugin-alexa-list-test")
	if err != nil {
		t.Fatalf("RegisterBundle: %v", err)
	}
	defer os.RemoveAll("state")

	p := bundle.NewPlugin()
	if err := p.Init(b); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer p.Shutdown()

	// Wait for relay connection.
	deadline := time.After(5 * time.Second)
	for !p.IsConnected() {
		select {
		case <-deadline:
			t.Fatal("not connected within 5s")
		case <-time.After(100 * time.Millisecond):
		}
	}

	fmt.Println("Triggering list_devices...")
	p.RunCommand("list_devices", nil)

	// Give the relay time to respond.
	time.Sleep(3 * time.Second)
}

// requireEnv skips the test with a clear message if any required env var is missing.
func requireEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if os.Getenv(key) == "" {
			t.Skipf("skipping: %s not set (add to .env)", key)
		}
	}
}
