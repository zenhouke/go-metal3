package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	metal3sdk "github.com/zenhouke/go-metal3"
	sdkclient "github.com/zenhouke/go-metal3/pkg/client"
	"github.com/zenhouke/go-metal3/pkg/httpapi"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	apiKey, err := readSecretFile("API_KEY_FILE")
	if err != nil {
		return err
	}
	restConfig, err := sdkclient.LoadConfig(sdkclient.ConfigOptions{})
	if err != nil {
		return err
	}
	sdk, err := metal3sdk.New(metal3sdk.Options{Config: restConfig})
	if err != nil {
		return err
	}
	managedNamespaces, err := splitRequiredEnv("MANAGED_NAMESPACES")
	if err != nil {
		return err
	}
	api, err := httpapi.New(httpapi.Options{SDK: sdk, APIKey: apiKey, AllowedNamespaces: managedNamespaces})
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              envOrDefault("LISTEN_ADDR", ":8080"),
		Handler:           api,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 << 10,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	log.Printf("go-metal3 API listening on %s", server.Addr)
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := httpapi.ShutdownContext(context.Background())
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func readSecretFile(envName string) (string, error) {
	path := os.Getenv(envName)
	if path == "" {
		return "", fmt.Errorf("%s is required", envName)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", envName, err)
	}
	value := strings.TrimRight(string(raw), "\r\n")
	if value == "" {
		return "", fmt.Errorf("%s points to an empty file", envName)
	}
	return value, nil
}

func requiredEnv(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func splitRequiredEnv(name string) ([]string, error) {
	value, err := requiredEnv(name)
	if err != nil {
		return nil, err
	}
	return splitOrDefault(value, ""), nil
}

func splitOrDefault(value, fallback string) []string {
	if value == "" {
		value = fallback
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
