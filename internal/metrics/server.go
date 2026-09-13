package metrics

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"conduit/internal/recover"
)

// Server serves the /metrics endpoint for the worker's Prometheus scraper. It
// binds a net.Listener synchronously in Start so a port conflict surfaces as an
// immediate error, then serves in a goroutine guarded by recover.Protect.
//
// Start and Stop are designed for the worker's sequential lifecycle (Start at
// boot, Stop during shutdown) and are not safe to call concurrently from
// multiple goroutines.
type Server struct {
	addr    string
	handler http.Handler
	logger  *log.Logger

	mu     sync.Mutex
	server *http.Server
	ln     net.Listener
	wg     sync.WaitGroup
}

// NewServer builds a metrics server that serves handler on addr. The listener is
// not bound until Start, so construction never fails on a port conflict. The
// /metrics endpoint is served for GET requests; any other method is rejected by
// the ServeMux with a 405 Method Not Allowed carrying an Allow: GET header.
func NewServer(addr string, handler http.Handler, logger *log.Logger) *Server {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", handler)

	return &Server{
		addr:    addr,
		handler: mux,
		logger:  logger,
	}
}

// Start binds the listen address and begins serving in a background goroutine.
// A bind error (e.g. port already in use) is returned synchronously. A second
// Start on an already-started server is a no-op. The ctx parameter is accepted
// only for lifecycle-signature consistency across the package's Start/Stop
// components; it is not otherwise used by the server.
func (s *Server) Start(_ context.Context) error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server != nil {
		return nil
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}

	s.server = &http.Server{
		Addr:              s.addr,
		Handler:           s.handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.ln = ln

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		recover.Protect(s.logger, "metrics:server", func() {
			if serveErr := s.server.Serve(s.ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				s.logger.Printf("metrics server error: %v", serveErr)
			}
		})
	}()

	s.logger.Printf("metrics server listening on %s", s.addr)
	return nil
}

// Stop gracefully shuts the metrics server down and waits for its serving
// goroutine to exit. It is idempotent and safe to call on a server that was
// never started.
func (s *Server) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	server := s.server
	s.server = nil
	s.mu.Unlock()

	if server == nil {
		// Never started, or already stopped.
		return nil
	}

	err := server.Shutdown(ctx)
	// Shutdown closes the listener and waits for in-flight requests; the serve
	// goroutine exits with http.ErrServerClosed, which it filters out.
	s.wg.Wait()
	if err != nil {
		return err
	}
	s.logger.Println("metrics server stopped")
	return nil
}
