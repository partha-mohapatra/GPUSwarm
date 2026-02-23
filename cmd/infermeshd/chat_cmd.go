package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"infermeshai/internal/cli"
)

func runChatCommand(args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		daemonURL = fs.String("daemon-url", "http://127.0.0.1:5555", "infermeshd daemon base URL")
		modelName = fs.String("model", "llama3-8b", "model name")
		prompt    = fs.String("prompt", "", "single prompt mode; if set, sends once and exits")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	runner := cli.NewChatRunner(cli.ChatOptions{
		BaseURL: *daemonURL,
		Model:   *modelName,
	})
	ctx := context.Background()

	if strings.TrimSpace(*prompt) != "" {
		if err := runner.SendPrompt(ctx, strings.TrimSpace(*prompt), os.Stdout); err != nil {
			return err
		}
		return nil
	}

	_, _ = fmt.Fprintln(os.Stdout, "interactive chat mode; /quit to exit")
	logger.Info("chat client connected", slog.String("daemon_url", *daemonURL), slog.String("model", *modelName))
	interactive := isInteractiveStdin()
	_ = runner.EnsureModelAvailable(ctx, os.Stdout, interactive)
	if interactive {
		return runner.RunInteractive(ctx, os.Stdout, os.Stderr)
	}
	return runner.RunREPL(ctx, os.Stdin, os.Stdout, os.Stderr)
}
