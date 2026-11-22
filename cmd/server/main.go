// Command server runs the pack calculator HTTP API and web UI.
//
// Configuration (environment variables):
//
//	PORT             port to listen on (default 8080)
//	PACK_SIZES       comma-separated default pack sizes (default 250,500,1000,2000,5000)
//	PACK_SIZES_FILE  optional JSON file to persist pack-size changes across restarts
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"pack-calculator/internal/api"
	"pack-calculator/internal/store"
	"pack-calculator/web"
)

const defaultPackSizes = "250,500,1000,2000,5000"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	port := envOr("PORT", "8080")
	sizes, err := parseSizes(envOr("PACK_SIZES", defaultPackSizes))
	if err != nil {
		return fmt.Errorf("PACK_SIZES: %w", err)
	}

	st, err := store.New(sizes, os.Getenv("PACK_SIZES_FILE"))
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           api.NewHandler(st, logger, web.FS()),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", srv.Addr, "packSizes", st.Get())
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func parseSizes(s string) ([]int, error) {
	var sizes []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("%q is not a whole number", part)
		}
		sizes = append(sizes, n)
	}
	return sizes, nil
}
