//go:build linux

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
	input := flag.String("private-input", "", "task-private reviewed product20 input")
	pin := flag.String("private-input-sha256", "", "exact input body SHA256")
	flag.Parse()
	plan, err := runtimeacceptance.ReadProduct20Plan(*input, *pin)
	if err != nil {
		os.Exit(2)
	}
	parent, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	ctx, stop := context.WithTimeout(parent, 1200*time.Second)
	defer stop()
	if err = runtimeacceptance.RunProduct20(ctx, plan); err != nil {
		os.Exit(1)
	}
}
