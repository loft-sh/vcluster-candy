package dnsserver

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/miekg/dns"
)

type Server struct {
	dns.Server
	logger logr.Logger
}

// NewServer creates a new DNS server that uses the given handler.
func NewServer(addr string, protocol string, handler dns.Handler, logger logr.Logger) *Server {
	return &Server{
		Server: dns.Server{
			Addr:    addr,
			Net:     protocol,
			Handler: handler,
		},
		logger: logger,
	}
}

// Start implements the manager.Runnable interface.
func (s *Server) Start(ctx context.Context) error {
	log := s.logger

	log.Info("starting DNS server", "addr", s.Addr, "protocol", s.Net)

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- s.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		log.Info("shutdown requested, stopping DNS server")
		shutCtx, shutCancel := context.WithTimeout(ctx, 5*time.Second)
		defer shutCancel()
		if err := s.ShutdownContext(shutCtx); err != nil {
			return fmt.Errorf("server shutdown: %w", err)
		}
		return nil
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("dns server: %w", err)
		}
		return nil
	}
}

// NeedLeaderElection implements the manager.LeaderElectionRunnable interface.
func (s *Server) NeedLeaderElection() bool {
	return false
}
