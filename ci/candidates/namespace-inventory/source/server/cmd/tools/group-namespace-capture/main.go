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
	mode := flags.String("mode", "inspect", "inspect or explicit capture")
	path := flags.String("snapshot", "", "absolute private snapshot path")
	connection := flags.String("connection", "", "absolute private connection JSON path")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *path == "" {
		return namespaceinventory.ErrInvalid
	}
	var summary namespaceinventory.PublicSummary
	var err error
	switch *mode {
	case "inspect":
		if *connection != "" {
			return namespaceinventory.ErrInvalid
		}
		summary, err = namespaceinventory.Inspect(*path)
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
