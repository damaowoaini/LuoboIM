package cmd

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/internal/msgtransfer"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/spf13/cobra"
)

// A separate executable selects this command. Existing deployments cannot
// accidentally activate it via the old binary's ordinary/half-on env flags.
type ManagedMsgTransferCmd struct{ *RootCmd }

func NewManagedMsgTransferCmd() *ManagedMsgTransferCmd {
	m := &ManagedMsgTransferCmd{RootCmd: NewRootCmd("managedMsgTransfer")}
	m.SetRootCmdPt(m)
	m.AddPrometheusPortFlag()
	return m
}

func (m *ManagedMsgTransferCmd) GetPortFromConfig(kind string) int {
	if kind == constant.FlagPrometheusPort && len(config.Config.Prometheus.MessageTransferPrometheusPort) > 0 {
		return config.Config.Prometheus.MessageTransferPrometheusPort[0]
	}
	return 0
}

func (m *ManagedMsgTransferCmd) Exec() error {
	m.Command.RunE = func(command *cobra.Command, args []string) error {
		announcementPath, ownerNonce, err := msgtransfer.LoadManagedOwnerAnnouncement()
		if err != nil {
			return err
		}
		parent, cancel := signal.NotifyContext(command.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		runtime, startErr := msgtransfer.StartManagedOrdinary(parent, m.getPrometheusPortFlag(command))
		if startErr == nil && announcementPath != "" {
			value, err := runtime.OwnerAnnouncement(parent)
			if err == nil {
				_, err = ownedcontrol.WriteRuntimeAnnouncement(announcementPath, ownerNonce, value)
			}
			if err != nil {
				startErr = err
			} // Original shutdown still joins every owned resource.
		}
		var primary error
		if startErr != nil {
			primary = startErr
		} else {
			primary = runtime.Wait(parent)
		}
		if runtime == nil {
			return primary
		}
		end, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if err := runtime.Stop(end); err != nil {
			return errors.Join(primary, err)
		}
		if startErr != nil {
			return errors.Join(primary, runtime.WriteUnconfirmedSource(end))
		} // Only a nonpermit observation may follow partial startup.
		_, drainErr := runtime.WriteDrain(end)
		return errors.Join(primary, drainErr)
	}
	return m.Execute()
}
