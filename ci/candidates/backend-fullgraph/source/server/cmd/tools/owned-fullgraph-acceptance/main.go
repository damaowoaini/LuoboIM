//go:build linux

// Explicit cloud acceptance operator, not a replacement service entrypoint.
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/runtimeacceptance"
)

func main() {
	input := flag.String("private-input", "", "exact private fixture input")
	pin := flag.String("private-input-sha256", "", "exact reviewed input SHA256")
	flag.Parse()
	if flag.NArg() != 0 {
		os.Exit(2)
	}
	plan, e := runtimeacceptance.ReadPlan(*input, *pin)
	if e != nil {
		os.Exit(2)
	}
	lifetime, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	ctx, stop := context.WithTimeout(lifetime, 20*time.Minute)
	defer stop()
	_, e = runtimeacceptance.RunAcceptance(ctx, plan)
	// Detailed original child/DB/provider logs remain private. The finite result
	// and safe phase are collected separately; no raw error may expose a DSN.
	if e != nil {
		os.Exit(1)
	}
}
