package types

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestJMXMetricSnapshot_StoresMetrics(t *testing.T) {
	now := time.Now()
	metrics := map[string]float64{
		"kafka.server:type=BrokerTopicMetrics,name=MessagesInPerSec:OneMinuteRate": 1234.56,
		"kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec:OneMinuteRate":    78910.11,
		"java.lang:type=Memory:HeapMemoryUsage.used":                                 524288000,
	}

	snapshot := JMXMetricSnapshot{
		Timestamp: now,
		Metrics:   metrics,
	}

	assert.Equal(t, now, snapshot.Timestamp)
	assert.Equal(t, 3, len(snapshot.Metrics))
	assert.Equal(t, 1234.56, snapshot.Metrics["kafka.server:type=BrokerTopicMetrics,name=MessagesInPerSec:OneMinuteRate"])
	assert.Equal(t, 78910.11, snapshot.Metrics["kafka.server:type=BrokerTopicMetrics,name=BytesInPerSec:OneMinuteRate"])
	assert.Equal(t, 524288000.0, snapshot.Metrics["java.lang:type=Memory:HeapMemoryUsage.used"])
}

func TestJMXMetrics_StoresSnapshots(t *testing.T) {
	scanStart := time.Now()
	scanDuration := "5m30s"

	snapshot1 := JMXMetricSnapshot{
		Timestamp: scanStart,
		Metrics: map[string]float64{
			"kafka.server:type=BrokerTopicMetrics,name=MessagesInPerSec:OneMinuteRate": 1000.0,
		},
	}

	snapshot2 := JMXMetricSnapshot{
		Timestamp: scanStart.Add(30 * time.Second),
		Metrics: map[string]float64{
			"kafka.server:type=BrokerTopicMetrics,name=MessagesInPerSec:OneMinuteRate": 1500.0,
		},
	}

	jmxMetrics := JMXMetrics{
		ScanDuration:  scanDuration,
		ScanStartTime: scanStart,
		Snapshots:     []JMXMetricSnapshot{snapshot1, snapshot2},
	}

	assert.Equal(t, scanDuration, jmxMetrics.ScanDuration)
	assert.Equal(t, scanStart, jmxMetrics.ScanStartTime)
	assert.Equal(t, 2, len(jmxMetrics.Snapshots))
	assert.Equal(t, 1000.0, jmxMetrics.Snapshots[0].Metrics["kafka.server:type=BrokerTopicMetrics,name=MessagesInPerSec:OneMinuteRate"])
	assert.Equal(t, 1500.0, jmxMetrics.Snapshots[1].Metrics["kafka.server:type=BrokerTopicMetrics,name=MessagesInPerSec:OneMinuteRate"])
}
