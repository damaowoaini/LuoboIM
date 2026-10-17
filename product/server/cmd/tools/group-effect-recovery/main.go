// group-effect-recovery is an explicit operator tool. It never sends an HTTP
// callback, notification, group RPC or business write; confirmation changes only
// one exact durable effect receipt after independently verified drain/outcome.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
)

type receiptStore interface {
	Inspect(context.Context, string) (groupeffects.Summary, error)
	Confirm(context.Context, groupeffects.Confirmation) (groupeffects.Summary, error)
}
type connection struct {
	store receiptStore
	close func(context.Context) error
}
type connector func(context.Context, operatorInput) (connection, error)
type arguments struct {
	mode, input, database, replica, endpoints, id, journal string
	confirmation                                           groupeffects.Confirmation
}

func parse(args []string) (arguments, error) {
	var a arguments
	f := flag.NewFlagSet("group-effect-recovery", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&a.mode, "mode", "inspect", "inspect, plan, or explicit confirm")
	f.StringVar(&a.input, "operator-input", "", "absolute dedicated private operator JSON")
	f.StringVar(&a.database, "expected-database", "", "independent exact database binding")
	f.StringVar(&a.replica, "expected-replica-set", "", "independent exact replica-set binding")
	f.StringVar(&a.endpoints, "expected-endpoints-sha256", "", "independent endpoint fingerprint")
	f.StringVar(&a.id, "id", "", "exact receipt UUID")
	f.StringVar(&a.journal, "journal", "", "fresh private JSONL confirmation journal")
	f.StringVar(&a.confirmation.ExpectedSenderInstanceID, "expected-sender-instance", "", "exact inspected managed sender instance")
	f.Int64Var(&a.confirmation.ExpectedRevision, "expected-revision", 0, "exact inspected revision")
	f.StringVar(&a.confirmation.ExpectedPayloadSHA256, "expected-payload-sha256", "", "exact frozen payload digest")
	f.StringVar(&a.confirmation.Decision, "decision", "", "applied or not-applied")
	f.StringVar(&a.confirmation.EvidenceSHA256, "remote-outcome-evidence-sha256", "", "verified remote outcome/cancellation evidence digest")
	f.StringVar(&a.confirmation.DrainedEvidenceSHA256, "worker-drain-evidence-sha256", "", "verified exact sending-worker drain evidence digest")
	if f.Parse(args) != nil || f.NArg() != 0 || (a.mode != "inspect" && a.mode != "plan" && a.mode != "confirm") || a.input == "" || !validDatabase(a.database) || !plain(a.replica) || !sha(a.endpoints) || !id(a.id) {
		return a, errInput
	}
	a.confirmation.ID = a.id
	if a.mode == "confirm" {
		if a.journal == "" || !id(a.confirmation.ExpectedSenderInstanceID) || a.confirmation.ExpectedRevision < 1 || !sha(a.confirmation.ExpectedPayloadSHA256) || !sha(a.confirmation.EvidenceSHA256) || !sha(a.confirmation.DrainedEvidenceSHA256) || (a.confirmation.Decision != "applied" && a.confirmation.Decision != "not-applied") {
			return a, errInput
		}
	} else if a.journal != "" || a.confirmation.ExpectedSenderInstanceID != "" || a.confirmation.ExpectedRevision != 0 || a.confirmation.ExpectedPayloadSHA256 != "" || a.confirmation.Decision != "" || a.confirmation.EvidenceSHA256 != "" || a.confirmation.DrainedEvidenceSHA256 != "" {
		return a, errInput
	}
	return a, nil
}

func execute(parent context.Context, args []string, out, stderr io.Writer, connect connector) int {
	a, err := parse(args)
	if err != nil {
		fmt.Fprintln(stderr, "invalid_operator_arguments")
		return 2
	}
	input, err := loadInput(a.input, a.database, a.replica, a.endpoints)
	if err != nil {
		fmt.Fprintln(stderr, "invalid_operator_input")
		return 2
	}
	var journal *os.File
	if a.mode == "confirm" {
		journal, err = openJournal(a.journal)
		if err != nil {
			fmt.Fprintln(stderr, "operator_journal_unavailable")
			return 2
		}
		// An unknown crash after this durable intention is always inspected by
		// exact UUID/hash/revision. The command never resumes or retries itself.
		if err = appendJournal(journal, map[string]any{"phase": "confirmation_intent", "confirmation": a.confirmation}); err != nil {
			_ = journal.Close()
			fmt.Fprintln(stderr, "operator_journal_unconfirmed")
			return 1
		}
	}
	code := 0
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	c, err := connect(ctx, input)
	if err != nil || c.store == nil {
		fmt.Fprintln(stderr, "operator_connection_unavailable")
		code = 1
	} else {
		var result groupeffects.Summary
		if a.mode == "confirm" {
			result, err = c.store.Confirm(ctx, a.confirmation)
		} else {
			result, err = c.store.Inspect(ctx, a.id)
		}
		if err != nil {
			label := "operator_action_failed"
			if errors.Is(err, groupeffects.ErrUnconfirmed) {
				label = "operator_result_unconfirmed"
				code = 3
			} else {
				code = 1
			}
			fmt.Fprintln(stderr, label)
			if journal != nil && appendJournal(journal, map[string]any{"phase": label}) != nil {
				code = 3
			}
		} else {
			if journal != nil && appendJournal(journal, map[string]any{"phase": "confirmation_readback", "summary": result}) != nil {
				code = 3
				fmt.Fprintln(stderr, "operator_journal_unconfirmed")
			}
			if json.NewEncoder(out).Encode(result) != nil {
				code = 3
				fmt.Fprintln(stderr, "operator_output_unconfirmed")
			}
		}
	}
	if c.close != nil {
		end, done := context.WithTimeout(context.Background(), 2*time.Second)
		if c.close(end) != nil {
			if code == 0 {
				code = 1
			}
			fmt.Fprintln(stderr, "operator_disconnect_unconfirmed")
		}
		done()
	}
	if journal != nil && journal.Close() != nil {
		code = 3
		fmt.Fprintln(stderr, "operator_journal_close_unconfirmed")
	}
	return code
}
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := execute(ctx, os.Args[1:], os.Stdout, os.Stderr, connectMongo)
	stop()
	os.Exit(code)
}
