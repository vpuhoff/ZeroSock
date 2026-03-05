package metrics

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"
)

func StartHTTP(ctx context.Context, listenAddr string, collector *Collector, logger *log.Logger) <-chan error {
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(collector.RenderPrometheusText()))
	})

	server := &http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}

	go func() {
		logger.Printf("metrics: listening on %s", listenAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("metrics listen failed: %w", err)
		}
		close(errCh)
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	return errCh
}
