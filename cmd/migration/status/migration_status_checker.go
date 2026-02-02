package status

type MigationStatusFlags struct {
	migrationId string
}

type MigationStatusChecker struct {
	opts MigationStatusFlags
}

func NewMigationStatusChecker(opts MigationStatusFlags) *MigationStatusChecker {
	return &MigationStatusChecker{
		opts: opts,
	}
}

func (m *MigationStatusChecker) Run() error {
	return nil
}
