// group-namespace-capture is read-only observation, never a migration apply or
// runtime activation command. It does not consume application configuration.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

func run(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("group-namespace-capture", flag.ContinueOnError)
	flags.SetOutput(io.Discard) // Never echo an accidentally supplied URI/secret.
	mode := flags.String("mode", "inspect", "inspect, capture, inspect-chunks, capture-chunks, resume-chunks or explicit resumable modes")
	path := flags.String("snapshot", "", "absolute private snapshot path")
	connection := flags.String("connection", "", "absolute private connection JSON path")
	pages := flags.Int("pages", 16, "maximum new pages per explicit chunk capture/resume")
	steps := flags.Int("steps", 64, "bounded work units for capture-resumable/resume-resumable")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *path == "" {
		return namespaceinventory.ErrInvalid
	}
	chunkMode := *mode == "inspect-chunks" || *mode == "capture-chunks" || *mode == "resume-chunks"
	resumableMode := *mode == "inspect-resumable" || *mode == "capture-resumable" || *mode == "resume-resumable"
	stepsExplicit := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "steps" {
			stepsExplicit = true
		}
	})
	if (!resumableMode || *mode == "inspect-resumable") && stepsExplicit {
		return namespaceinventory.ErrInvalid
	}
	pagesExplicit := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "pages" {
			pagesExplicit = true
		}
	})
	if (!chunkMode || *mode == "inspect-chunks") && pagesExplicit {
		return namespaceinventory.ErrInvalid
	}
	var summary interface{}
	var err error
	switch *mode {
	case "inspect":
		if *connection != "" {
			return namespaceinventory.ErrInvalid
		}
		summary, err = namespaceinventory.Inspect(*path)
	case "inspect-resumable":
		if *connection != "" {
			return namespaceinventory.ErrInvalid
		}
		summary, err = namespaceinventory.InspectResumableProgress(*path)
	case "capture-resumable", "resume-resumable":
		if *connection == "" {
			return namespaceinventory.ErrInvalid
		}
		summary, err = namespaceinventory.CaptureResumablePrivate(ctx, *connection, *path, *mode == "resume-resumable", *steps)
	case "inspect-chunks":
		if *connection != "" {
			return namespaceinventory.ErrInvalid
		}
		summary, err = namespaceinventory.InspectChunks(ctx, *path)
	case "capture-chunks", "resume-chunks":
		if *connection == "" {
			return namespaceinventory.ErrInvalid
		}
		summary, err = namespaceinventory.CaptureChunksPrivate(ctx, *connection, *path, *mode == "resume-chunks", *pages)
	case "capture":
		if *connection == "" {
			return namespaceinventory.ErrInvalid
		}
		summary, err = namespaceinventory.CapturePrivate(ctx, *connection, *path)
	default:
		return namespaceinventory.ErrInvalid
	}
	if err != nil {
		return err
	}
	if json.NewEncoder(out).Encode(summary) != nil {
		return namespaceinventory.ErrSource
	}
	return nil
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, namespaceinventory.SafeFailure(err))
		os.Exit(1)
	}
}
