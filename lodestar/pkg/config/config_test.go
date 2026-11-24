package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/session/mail"
)

// TestCoreConfiguration opens the core level as development does, with no logging
// project, and with a trace sampling word the driver does not know: the exporter the
// level hands out is the driver's console exporter, and the refusal is the driver's,
// since the level reads the sampling through the settings it embeds and nothing else.
//
// Demonstrates: cloud.driver.
func TestCoreConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		sampling string
		wantErr  string
	}{
		{name: "no logging project: the console exporter", sampling: "edge"},
		{name: "a sampling word the driver does not know is refused", sampling: "sometimes", wantErr: "tracer.ParseSampling()"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("APP_SERVICE_NAME", "lodestar")
			t.Setenv("GOOGLE_CLOUD_LOGGING_PROJECT", "")
			t.Setenv("APP_TRACE_SAMPLING", tt.sampling)

			core, err := newCoreConfiguration(t.Context())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("newCoreConfiguration() error = %v, want one naming %s", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("newCoreConfiguration() error = %v", err)
			}
			defer core.Close()

			if _, ok := core.LogExporter().(*logger.ConsoleExporter); !ok {
				t.Errorf("LogExporter() = %T, want *logger.ConsoleExporter", core.LogExporter())
			}
			if got := core.ServiceName(); got != "lodestar" {
				t.Errorf("ServiceName() = %q, want lodestar", got)
			}
		})
	}
}

// TestOpenMailSender opens the mail driver over the three cases of the data level: a
// mailbox with its app password delivers in every environment; development with no
// mailbox, or with a mailbox and no app password, takes the log, which delivers nothing;
// any other environment (none named, or tst) is refused in those two cases, with the
// sentence that says why.
func TestOpenMailSender(t *testing.T) {
	t.Parallel()

	const (
		mailbox     = "no-reply@example.com"
		appPassword = "fixture-app-password"
	)
	tests := []struct {
		name        string
		environment string
		mailbox     string
		appPassword string
		wantRefused bool
		wantDeliver bool
	}{
		{name: "development, both set: the mailbox delivers", environment: "development", mailbox: mailbox, appPassword: appPassword, wantDeliver: true},
		{name: "no environment named, both set: the mailbox delivers", mailbox: mailbox, appPassword: appPassword, wantDeliver: true},
		{name: "tst, both set: the mailbox delivers", environment: "tst", mailbox: mailbox, appPassword: appPassword, wantDeliver: true},
		{name: "development, no mailbox: the log", environment: "development"},
		{name: "development, a mailbox and no app password: the log", environment: "development", mailbox: mailbox},
		{name: "no environment named, no mailbox: refused", wantRefused: true},
		{name: "no environment named, a mailbox and no app password: refused", mailbox: mailbox, wantRefused: true},
		{name: "tst, no mailbox: refused", environment: "tst", wantRefused: true},
		{name: "tst, a mailbox and no app password: refused", environment: "tst", mailbox: mailbox, wantRefused: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sender, err := openMailSender(&dataConfig{MailMailbox: tt.mailbox, MailAppPassword: tt.appPassword}, tt.environment)
			if tt.wantRefused {
				if !errors.Is(err, errNoMailbox) {
					t.Fatalf("openMailSender() error = %v, want errNoMailbox", err)
				}
				if !strings.Contains(err.Error(), "does not start") || !strings.Contains(err.Error(), "APP_ENVIRONMENT=development") {
					t.Errorf("openMailSender() error = %q, want the sentence that says why the application does not start", err)
				}

				return
			}
			if err != nil {
				t.Fatalf("openMailSender() error = %v", err)
			}
			if got := mail.Delivers(sender); got != tt.wantDeliver {
				t.Errorf("mail.Delivers(openMailSender()) = %v, want %v", got, tt.wantDeliver)
			}
			if _, isLog := sender.(mail.Log); isLog == tt.wantDeliver {
				t.Errorf("openMailSender() = %T, the log: %v, want %v", sender, isLog, !tt.wantDeliver)
			}
		})
	}
}
