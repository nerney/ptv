package defs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nerney/ptv/internal/logger"
)

type State int

const (
	StatePending         State = iota // startup, not yet attempted
	StateSyncing                      // git op in progress
	StateOK                           // definitions up to date
	StateStalePullFailed              // pull failed, stale clone in use
	StateUnavailable                  // no clone and clone failed
)

func (s State) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateSyncing:
		return "syncing"
	case StateOK:
		return "ok"
	case StateStalePullFailed:
		return "stale"
	case StateUnavailable:
		return "unavailable"
	}
	return "unknown"
}

const repoURL = "https://github.com/Prowlarr/Indexers.git"

type Syncer struct {
	dir   string
	log   *logger.Logger
	mu    sync.RWMutex
	state State
	msg   string
	ready chan struct{} // closed once first sync attempt completes
}

func New(configDir string, log *logger.Logger) *Syncer {
	return &Syncer{
		dir:   filepath.Join(configDir, ".hidden", "Indexers"),
		log:   log,
		state: StatePending,
		ready: make(chan struct{}),
	}
}

// Start launches the sync goroutine. Returns immediately.
func (s *Syncer) Start(ctx context.Context) {
	go s.run(ctx)
}

// WaitReady blocks until the first sync attempt completes or ctx expires.
// Returns non-nil only if no definitions are available at all.
func (s *Syncer) WaitReady(ctx context.Context) error {
	select {
	case <-s.ready:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state == StateUnavailable {
		return fmt.Errorf("indexer definitions unavailable: %s", s.msg)
	}
	return nil
}

// Status returns the current sync state and an optional human-readable message.
func (s *Syncer) Status() (State, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state, s.msg
}

func (s *Syncer) run(ctx context.Context) {
	defer close(s.ready)
	s.set(StateSyncing, "")
	s.log.Info("DEFS", "Starting definitions sync — "+repoURL)

	if _, err := os.Stat(s.dir); os.IsNotExist(err) {
		if err := s.gitClone(ctx); err != nil {
			s.log.Err("DEFS", "Clone failed: "+err.Error())
			s.set(StateUnavailable, err.Error())
			return
		}
		s.log.Info("DEFS", "Clone complete")
		s.set(StateOK, "")
		return
	}

	if err := s.gitPull(ctx); err != nil {
		s.log.Err("DEFS", "Pull failed — using stale definitions: "+err.Error())
		s.set(StateStalePullFailed, err.Error())
		return
	}
	s.log.Info("DEFS", "Pull complete")
	s.set(StateOK, "")
}

func (s *Syncer) set(state State, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
	s.msg = msg
}

func (s *Syncer) gitClone(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(s.dir), 0700); err != nil {
		return err
	}
	return s.git(ctx, "clone", "--depth=1", "--branch=master", repoURL, s.dir)
}

func (s *Syncer) gitPull(ctx context.Context) error {
	return s.git(ctx, "-C", s.dir, "pull", "--ff-only")
}

func (s *Syncer) git(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	if err != nil {
		msg := err.Error()
		if trimmed != "" {
			msg = trimmed
		}
		return fmt.Errorf("%s", msg)
	}
	if trimmed != "" {
		s.log.Info("DEFS", "git: "+trimmed)
	}
	return nil
}
