// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package keyoku

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// CommandRunner executes external commands. Extracted for testability.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// CommandRunnerFunc adapts a plain function to the CommandRunner interface.
type CommandRunnerFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

func (f CommandRunnerFunc) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f(ctx, name, args...)
}

// execRunner is the default CommandRunner using os/exec.
type execRunner struct{}

func (r *execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// CLIDeliverer delivers heartbeat messages by shelling out to the OpenClaw CLI.
type CLIDeliverer struct {
	config DeliveryConfig
	logger *slog.Logger
	runner CommandRunner
}

// NewCLIDeliverer creates a new CLI-based deliverer.
func NewCLIDeliverer(config DeliveryConfig) *CLIDeliverer {
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}
	if config.Command == "" {
		config.Command = "openclaw"
	}
	return &CLIDeliverer{
		config: config,
		logger: slog.Default().With("component", "delivery-cli"),
		runner: &execRunner{},
	}
}

// SetLogger sets a custom logger for the deliverer.
func (d *CLIDeliverer) SetLogger(logger *slog.Logger) {
	d.logger = logger.With("component", "delivery-cli")
}

// Deliver sends a heartbeat message via the OpenClaw CLI.
func (d *CLIDeliverer) Deliver(ctx context.Context, entityID string, result *HeartbeatResult) error {
	message := buildDeliveryMessage(result)
	if message == "" {
		d.logger.Warn("delivery skipped: empty message",
			"entity", entityID,
			"decision", result.DecisionReason,
		)
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, d.config.Timeout)
	defer cancel()

	args := d.buildArgs(message)
	cmdParts := strings.Fields(d.config.Command)
	if len(cmdParts) == 0 {
		return fmt.Errorf("delivery: empty command")
	}

	// Append our args to any base command args (e.g. "docker exec kumo openclaw" + "agent --message ...")
	allArgs := append(cmdParts[1:], args...)
	output, err := d.runner.Run(ctx, cmdParts[0], allArgs...)
	if err != nil {
		d.logger.Error("delivery failed",
			"entity", entityID,
			"error", err,
			"output", truncate(string(output), 500),
			"command", cmdParts[0],
		)
		return fmt.Errorf("delivery: %w: %s", err, truncate(string(output), 200))
	}

	d.logger.Info("delivered heartbeat",
		"entity", entityID,
		"channel", d.config.Channel,
		"recipient", d.config.Recipient,
		"decision", result.DecisionReason,
		"urgency", result.Urgency,
	)
	return nil
}

// buildArgs constructs the CLI arguments for openclaw agent.
func (d *CLIDeliverer) buildArgs(message string) []string {
	args := []string{"agent", "--message", message, "--deliver"}

	// Session ID is required for OpenClaw to route the message.
	// Use explicit SessionID, or derive from Channel + Recipient.
	sessionID := d.config.SessionID
	if sessionID == "" && d.config.Channel != "" && d.config.Recipient != "" {
		sessionID = d.config.Channel + ":group:" + d.config.Recipient
	}
	if sessionID != "" {
		args = append(args, "--session-id", sessionID)
	}

	if d.config.Channel != "" {
		args = append(args, "--channel", d.config.Channel)
	}
	if d.config.Recipient != "" {
		args = append(args, "--reply-to", d.config.Recipient)
	}

	return args
}
