package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"feedvideo.local/video/repo"
)

// Leave half a second for the timeout response before the server write deadline.
const (
	mediaMetricsQueryBudget   = 4 * time.Second
	mediaMetricsHandlerBudget = 4500 * time.Millisecond
)

type mediaHTTPServer interface {
	Serve(net.Listener) error
	Shutdown(context.Context) error
	Close() error
}

type mediaMetricsRuntime struct {
	listen func(string, string) (net.Listener, error)
	server func(*http.Server) mediaHTTPServer
	run    func(context.Context, repo.MediaJobStore, mediaProcessor, string) error
}

func newMongoMediaMetricsHandler(reader repo.MediaJobMetricsReader, now func() time.Time) http.Handler {
	if now == nil {
		now = time.Now
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(rw http.ResponseWriter, _ *http.Request) { _, _ = rw.Write([]byte("ok")) })
	mux.HandleFunc("/metrics", func(rw http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), mediaMetricsQueryBudget)
		defer cancel()
		if reader == nil {
			http.Error(rw, "media metrics unavailable", http.StatusServiceUnavailable)
			return
		}
		at := now()
		snapshot, err := reader.Snapshot(ctx, at)
		if err != nil || ctx.Err() != nil {
			http.Error(rw, "media metrics unavailable", http.StatusServiceUnavailable)
			return
		}
		age := int64(0)
		if !snapshot.OldestCreatedAt.IsZero() && at.After(snapshot.OldestCreatedAt) {
			age = int64(at.Sub(snapshot.OldestCreatedAt).Seconds())
		}
		rw.Header().Set("Content-Type", "text/plain; version=0.0.4")
		line := func(name, help string, value int64) {
			fmt.Fprintf(rw, "# HELP %s %s\n# TYPE %s gauge\n%s %d\n", name, help, name, name, value)
		}
		line("feedvideo_media_tasks_queue_depth", "queued and retry-wait media jobs", snapshot.Queued+snapshot.RetryWait)
		line("feedvideo_media_processing_depth", "processing media jobs including expired leases", snapshot.Processing)
		line("feedvideo_media_active", "processing media jobs with a valid lease", snapshot.Active)
		line("feedvideo_media_oldest_task_age_seconds", "oldest nonterminal media job age", age)
		line("feedvideo_media_dlq_depth", "failed media jobs", snapshot.Failed)
	})
	// Bound the complete response as well as the Mongo query. No samples are
	// emitted until the entire authoritative snapshot has been read successfully.
	return http.TimeoutHandler(mux, mediaMetricsHandlerBudget, "media metrics unavailable\n")
}

func runMongoMediaService(ctx context.Context, store repo.MediaJobStore, reader repo.MediaJobMetricsReader, processor mediaProcessor, owner, addr string, runtime mediaMetricsRuntime) (runErr error) {
	// A dependency timeout is a startup/runtime error unless shutdown was
	// actually requested through the root context.
	defer func() {
		if ctx.Err() != nil {
			runErr = nil
		}
	}()
	if err := ensureMongoMediaIndexes(ctx, store); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if addr == "" {
		addr = "127.0.0.1:9091"
	}
	if runtime.listen == nil {
		runtime.listen = net.Listen
	}
	if runtime.server == nil {
		runtime.server = func(s *http.Server) mediaHTTPServer { return s }
	}
	if runtime.run == nil {
		runtime.run = runMongoMediaScan
	}
	listener, err := runtime.listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("media metrics listen: %w", err)
	}
	server := runtime.server(&http.Server{Addr: addr, Handler: newMongoMediaMetricsHandler(reader, time.Now), ReadHeaderTimeout: mediaStoreBudget, WriteTimeout: mediaStoreBudget,
		BaseContext: func(net.Listener) context.Context { return ctx }})
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	serveDone, loopDone := make(chan error, 1), make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	go func() { loopDone <- runtime.run(runCtx, store, processor, owner) }()
	select {
	case <-ctx.Done():
		runErr = ctx.Err()
	case err := <-serveDone:
		serveDone = nil
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			err = errors.New("media metrics server stopped")
		}
		runErr = fmt.Errorf("media metrics serve: %w", err)
	case runErr = <-loopDone:
		loopDone = nil
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), mediaStoreBudget)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		runErr = errors.Join(runErr, err, server.Close())
	}
	for serveDone != nil || loopDone != nil {
		select {
		case <-serveDone:
			serveDone = nil
		case <-loopDone:
			loopDone = nil
		case <-shutdownCtx.Done():
			return errors.Join(runErr, shutdownCtx.Err(), server.Close())
		}
	}
	return runErr
}
