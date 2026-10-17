package main

import (
	"os"

	"github.com/openimsdk/open-im-server/v3/pkg/common/cmd"
)

func main() {
	if err := cmd.NewManagedMsgTransferCmd().Exec(); err != nil {
		// Detailed original receipts remain private. Never print deployment
		// configuration, connection errors, signing material or source payloads.
		_, _ = os.Stderr.WriteString("managed ordinary startup or drain failed; inspect owned evidence\n")
		os.Exit(1)
	}
}
