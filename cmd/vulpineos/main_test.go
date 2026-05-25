package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun_VersionFlag(t *testing.T) {
	var outBuf, errBuf bytes.Buffer

	prevOut, prevErr := stdout, stderr
	stdout = &outBuf
	stderr = &errBuf
	t.Cleanup(func() {
		stdout = prevOut
		stderr = prevErr
	})

	code := Run([]string{"vulpineos", "--version"})
	if code != 0 {
		t.Fatalf("Run(--version) exit code = %d, want 0 (stderr=%q)", code, errBuf.String())
	}
	out := outBuf.String()
	if !strings.Contains(out, "vulpineos") {
		t.Errorf("Run(--version) stdout = %q, expected to contain %q", out, "vulpineos")
	}
	if strings.TrimSpace(out) == "" {
		t.Errorf("Run(--version) produced empty stdout")
	}
}

func TestRun_HelpFlagsExitZero(t *testing.T) {
	for _, args := range [][]string{
		{"vulpineos", "--help"},
		{"vulpineos", "remote", "--help"},
		{"vulpineos", "serve", "--help"},
		{"vulpineos", "mcp", "--help"},
	} {
		t.Run(strings.Join(args[1:], "_"), func(t *testing.T) {
			var outBuf, errBuf bytes.Buffer

			prevOut, prevErr := stdout, stderr
			stdout = &outBuf
			stderr = &errBuf
			t.Cleanup(func() {
				stdout = prevOut
				stderr = prevErr
			})

			if code := Run(args); code != 0 {
				t.Fatalf("Run(%v) exit code = %d, want 0 (stderr=%q)", args, code, errBuf.String())
			}
			if strings.TrimSpace(errBuf.String()) == "" {
				t.Fatalf("Run(%v) should print help text to stderr", args)
			}
		})
	}
}

func TestRun_RemovedPanelAndTUISubcommandsFail(t *testing.T) {
	for _, subcommand := range []string{"tui", "panel"} {
		t.Run(subcommand, func(t *testing.T) {
			var outBuf, errBuf bytes.Buffer
			prevOut, prevErr := stdout, stderr
			stdout = &outBuf
			stderr = &errBuf
			t.Cleanup(func() {
				stdout = prevOut
				stderr = prevErr
			})

			code := Run([]string{"vulpineos", subcommand, "--help"})
			if code == 0 {
				t.Fatalf("Run(%s --help) exit code = 0, want failure", subcommand)
			}
			if !strings.Contains(errBuf.String(), "removed") {
				t.Fatalf("stderr = %q, want removed notice", errBuf.String())
			}
		})
	}
}

func TestPairingURLConvertsRemoteWebSocketURL(t *testing.T) {
	got, err := pairingURL("ws://example.test:8443/ws")
	if err != nil {
		t.Fatalf("pairingURL: %v", err)
	}
	if got != "http://example.test:8443/pair" {
		t.Fatalf("pairingURL = %q, want http pair endpoint", got)
	}
	got, err = pairingURL("wss://example.test/remote/ws?token=old")
	if err != nil {
		t.Fatalf("pairingURL wss: %v", err)
	}
	if got != "https://example.test/pair" {
		t.Fatalf("pairingURL wss = %q, want https pair endpoint", got)
	}
}
