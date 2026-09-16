package plan

import (
	"time"

	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
)

// ScanlessClusterName is the key of the single synthetic cluster used when the
// planner runs with no scan file. It becomes the cluster's heading and its key
// in plan-inputs.yaml.
const ScanlessClusterName = "your-cluster"

// ScanlessState returns a processed state with one placeholder cluster and no
// scanned data, so the planner can run as a pure questionnaire without a state
// file: every fact the scan would normally derive is absent, so it surfaces as a
// question in plan-inputs.yaml for the customer to fill in. The cluster carries
// zero-value AWS/Kafka client info (nil slices), which the profile builder reads
// as "not scanned" for every signal.
func ScanlessState() report.ProcessedState {
	return report.ProcessedState{
		Timestamp: time.Time{},
		Sources: []report.ProcessedSource{{
			Type: types.SourceTypeMSK,
			MSKData: &report.ProcessedMSKSource{
				Regions: []report.ProcessedRegion{{
					Clusters: []report.ProcessedCluster{{
						Name: ScanlessClusterName,
					}},
				}},
			},
		}},
	}
}
