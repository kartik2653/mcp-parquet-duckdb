package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Intellicars/intellicarai/internal/agent"
	"github.com/Intellicars/intellicarai/internal/cli"
	"github.com/Intellicars/intellicarai/internal/clientcfg"
	"github.com/Intellicars/intellicarai/internal/envfile"
	"github.com/Intellicars/intellicarai/internal/llm"
	"github.com/Intellicars/intellicarai/internal/logging"
	"github.com/Intellicars/intellicarai/internal/mcpclient"
)

func main() {
	logger := logging.New(os.Stderr, "info")
	if err := run(); err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}

func run() error {
	if err := envfile.Load(".env"); err != nil {
		return err
	}
	cfg, err := clientcfg.Load("")
	if err != nil {
		return err
	}
	if cfg.APIKey == "" {
		return errors.New("XAI_API_KEY is required")
	}
	logger := logging.New(os.Stderr, cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	conn, err := mcpclient.Connect(ctx, mcpclient.Config{
		Transport:     cfg.Transport,
		ServerURL:     cfg.ServerURL,
		AuthToken:     cfg.AuthToken,
		ServerCommand: cfg.ServerCommand,
		Timeout:       cfg.RequestTimeout,
	})
	if err != nil {
		return err
	}
	defer conn.Close()

	session := &agent.Agent{
		Provider: llm.XAI{
			APIKey:  cfg.APIKey,
			Model:   cfg.Model,
			BaseURL: cfg.BaseURL,
		},
		Tools:          conn,
		MaxRounds:      cfg.MaxToolRounds,
		MaxHistory:     cfg.MaxHistoryMessages,
		MaxResultBytes: cfg.MaxToolResultBytes,
		Progress: func(text string) {
			if text != "" {
				fmt.Fprintf(os.Stderr, "… %s\n", text)
			}
		},
	}
	if err := session.Start(ctx); err != nil {
		return err
	}
	logger.Info("client ready", "model", cfg.Model, "transport", cfg.Transport)
	fmt.Fprintln(os.Stderr, "Ask a question about the dataset. Type exit to quit.")
	return cli.Run(ctx, os.Stdin, os.Stdout, os.Stderr, session.Ask, nil)
}
