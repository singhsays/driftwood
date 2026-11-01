// Package runner orchestrates fetches across nodes.
package runner

import (
	"context"
	"driftwood/internal/config"
	"driftwood/internal/git"
	"driftwood/internal/model"
	"driftwood/internal/version"
	"fmt"
	"log"
	"sync"
	"time"

	dssh "driftwood/internal/ssh"
)

// sink receives fetched artifacts. Satisfied by git.Repo.
type sink interface {
	Store(node string, arts []model.Artifact) (bool, error)
}

// openSink builds the git sink when the config enables git output;
// nil means fetch-only.
func openSink(ctx context.Context, cfg *config.Config) (sink, error) {
	if cfg.Git.Remote == "" {
		return nil, nil
	}
	return git.Open(ctx, cfg.Git)
}

// Fetch loop tuning.
const (
	// maxConcurrentFetches bounds parallel node fetches in Once and Daemon.
	maxConcurrentFetches = 4

	// failBackoffDivisor: failed nodes retry at interval/this.
	failBackoffDivisor = 10

	// minFailBackoff is the floor for the failure retry interval.
	minFailBackoff = time.Minute

	// jitterSteps: wait is jittered by up to ±jitterSteps/2 percent.
	jitterSteps = 21
)

// Result is the outcome of fetching one node.
type Result struct {
	Node      config.Node
	Artifacts []model.Artifact
	Err       error
	Duration  time.Duration
}

// Once fetches all nodes concurrently and returns per-node results.
func Once(ctx context.Context, cfg *config.Config) []Result {
	sk, err := openSink(ctx, cfg)
	if err != nil {
		log.Printf("FAIL git output: %v", err)
		return nil
	}
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		out []Result
		sem = make(chan struct{}, maxConcurrentFetches)
	)
	for _, node := range cfg.Nodes {
		wg.Add(1)
		go func(n config.Node) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := fetchNode(ctx, n)
			if r.Err == nil && sk != nil {
				store(r, sk)
			}
			mu.Lock()
			out = append(out, r)
			mu.Unlock()
		}(node)
	}
	wg.Wait()
	return out
}

// Daemon runs fetches continuously until ctx is cancelled.
//
// Contract: each node is fetched immediately on startup, then again after
// its interval (plus jitter) has elapsed. A failing node retries on its own
// backoff and never blocks the others.
func Daemon(ctx context.Context, cfg *config.Config) error {
	log.Printf("driftwood %s starting (%d nodes)", version.String(), len(cfg.Nodes))
	sk, err := openSink(ctx, cfg)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	for _, node := range cfg.Nodes {
		wg.Add(1)
		go func(n config.Node) {
			defer wg.Done()
			nodeLoop(ctx, n, sk)
		}(node)
	}
	wg.Wait()
	if ctx.Err() != nil {
		// Signal-initiated shutdown is a clean exit, not a failure.
		return nil
	}
	return ctx.Err()
}

// nodeLoop fetches immediately, then on the node's interval with jitter.
// Failures retry at a shorter interval.
func nodeLoop(ctx context.Context, n config.Node, sk sink) {
	interval := n.IntervalOf()
	failBackoff := interval / failBackoffDivisor
	if failBackoff < minFailBackoff {
		failBackoff = minFailBackoff
	}
	for {
		r := fetchNode(ctx, n)
		if r.Err != nil {
			log.Printf("FAIL %s: %v", n.Name, r.Err)
		} else {
			log.Printf("OK   %s: %d artifacts (%s)", n.Name, len(r.Artifacts), r.Duration.Round(time.Millisecond))
		}
		if r.Err == nil && sk != nil {
			store(r, sk)
		}
		wait := interval
		if r.Err != nil {
			wait = failBackoff
		}
		wait += jitter(wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// jitter returns a random duration up to ±10% of d.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return time.Duration(d.Nanoseconds() / failBackoffDivisor * (int64(time.Now().UnixNano()%jitterSteps) - jitterSteps/2))
}

func fetchNode(ctx context.Context, n config.Node) Result {
	start := time.Now()
	r := Result{Node: n}
	fetcher, err := model.Get(n.Model)
	if n.Model == "generic" {
		if len(n.Commands) == 0 && len(n.Files) == 0 {
			r.Err = fmt.Errorf("node %s: generic model requires commands or files", n.Name)
			return r
		}
		fetcher = model.Generic(n.Commands, n.Files)
		err = nil
	}
	if err != nil {
		r.Err = err
		return r
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, derr := dssh.Dial(n.Host, n.Port, n.User, dssh.KeyLoader{KeyFile: n.KeyFile})
		if derr != nil {
			r.Err = fmt.Errorf("dial %s: %w", n.Host, derr)
			return
		}
		defer func() { _ = c.Close() }()
		r.Artifacts, r.Err = fetcher.Fetch(c)
		if r.Err == nil && n.Encrypt != "" {
			// Encrypt in memory, before anything touches the mirror or git.
			if eerr := model.EncryptArtifacts(r.Artifacts, n.Encrypt); eerr != nil {
				r.Err = fmt.Errorf("encrypt: %w", eerr)
			}
		}
	}()
	select {
	case <-done:
	case <-ctx.Done():
		r.Err = ctx.Err()
	}
	r.Duration = time.Since(start)
	return r
}

// store pushes one node's artifacts into the git sink, logging failures.
// A store failure becomes the node's fetch error so the retry backoff
// covers git problems too.
func store(r Result, sk sink) {
	if _, err := sk.Store(r.Node.Name, r.Artifacts); err != nil {
		log.Printf("FAIL %s (git): %v", r.Node.Name, err)
		r.Err = err
	}
}

// PrintResults logs a human-readable summary.
func PrintResults(results []Result) {
	for _, r := range results {
		if r.Err != nil {
			log.Printf("FAIL %-12s %v", r.Node.Name, r.Err)
			continue
		}
		log.Printf("OK   %-12s %s (%d artifacts)", r.Node.Name, r.Duration.Round(time.Millisecond), len(r.Artifacts))
		for _, a := range r.Artifacts {
			log.Printf("     %-12s %s %d bytes", r.Node.Name, a.Name, len(a.Data))
		}
	}
}
