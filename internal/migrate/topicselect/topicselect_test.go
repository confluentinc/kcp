package topicselect

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelectTopics(t *testing.T) {
	all := []string{"orders", "orders.created", "events", "_schemas", "__consumer_offsets", "_confluent-x"}

	got, err := SelectTopics(all, []string{"*"}, nil)
	require.NoError(t, err)
	// internal topics (leading "_") always dropped; result sorted
	require.Equal(t, []string{"events", "orders", "orders.created"}, got)

	got, err = SelectTopics(all, []string{"orders*"}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"orders", "orders.created"}, got)

	got, err = SelectTopics(all, []string{"*"}, []string{"orders.*"})
	require.NoError(t, err)
	require.Equal(t, []string{"events", "orders"}, got) // exclude removes orders.created (orders has no dot suffix)

	_, err = SelectTopics(all, []string{"["}, nil)
	require.Error(t, err)
}
