package migration_execute

import (
	"github.com/confluentinc/kcp/internal/types"
)

type MigrationExecuteOpts struct {
	stateFile string
	state     types.State
	migrationId string
}

type MigrationExecute struct {
	stateFile string
	state     types.State
	migrationId string
}

func NewMigrationExecute(opts MigrationExecuteOpts) *MigrationExecute {
	return &MigrationExecute{
		stateFile:            opts.stateFile,
		state:                opts.state,
		migrationId:          opts.migrationId,
	}
}

func (m *MigrationExecute) Run() error {

	// migrationOpts := types.MigrationOpts{
	// 	GatewayNamespace:     m.gatewayNamespace,
	// 	GatewayCrdName:       m.gatewayCrdName,
	// 	SourceName:           m.sourceName,
	// 	DestinationName:      m.destinationName,
	// 	SourceRouteName:      m.sourceRouteName,
	// 	DestinationRouteName: m.destinationRouteName,
	// 	KubeConfigPath:       m.kubeConfigPath,
	// 	ClusterId:            m.clusterId,
	// 	ClusterRestEndpoint:  m.clusterRestEndpoint,
	// 	ClusterLinkName:      m.clusterLinkName,
	// 	Topics:               m.topics,
	// 	AuthMode:             m.authMode,
	// 	ClusterApiKey:        m.clusterApiKey,
	// 	ClusterApiSecret:     m.clusterApiSecret,
	// }

	// migrationId := fmt.Sprintf("migration-%s", time.Now().Format("20060102-150405"))
	// migration := types.NewMigration(migrationId, migrationOpts)
	// err := migration.FSM.Event(context.Background(), types.EventKcpExecute)
	// if err != nil {
	// 	return fmt.Errorf("failed to initialize migration: %v", err)
	// }
	// slog.Info("migration initialized", "migrationId", migration.MigrationId, "currentState", migration.CurrentState, "fsm", migration.FSM.Current())
	// m.state.UpsertMigration(*migration)
	// return m.state.PersistStateFile(m.stateFile)
	return nil
}
