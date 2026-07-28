package grouping

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuild_CouplingIsTransitiveAcrossTransactions(t *testing.T) {
	// Topics coupled by a transaction must migrate together, and the coupling is
	// transitive: two transactions sharing a single topic put every topic either of
	// them touched into the same group. a-b, b-c and c-d chain into one group of four.
	res := Build([]Transaction{
		{ID: "1", Topics: []string{"a", "b"}},
		{ID: "2", Topics: []string{"b", "c"}},
		{ID: "3", Topics: []string{"c", "d"}},
	}, Options{})

	require.Len(t, res.Groups, 1)
	assert.Equal(t, []string{"a", "b", "c", "d"}, res.Groups[0].Topics)
	assert.Equal(t, []string{"1", "2", "3"}, res.Groups[0].TxnIDs)
}

func TestBuild_UnrelatedWorkloadsStaySeparateAndAreOrderedLargestFirst(t *testing.T) {
	// Client A touches t1,t2,t3; client B touches t2,t4; client C touches t5,t6.
	// A and B share t2 so they merge; C shares nothing so it stays its own group.
	// Groups are presented largest first, because group size is what an operator
	// planning migration batches reads off the summary.
	res := Build([]Transaction{
		{ID: "A", Topics: []string{"t1", "t2", "t3"}},
		{ID: "B", Topics: []string{"t2", "t4"}},
		{ID: "C", Topics: []string{"t5", "t6"}},
	}, Options{})

	require.Len(t, res.Groups, 2)
	assert.Equal(t, "group-1", res.Groups[0].Name)
	assert.Equal(t, []string{"t1", "t2", "t3", "t4"}, res.Groups[0].Topics)
	assert.Equal(t, "group-2", res.Groups[1].Name)
	assert.Equal(t, []string{"t5", "t6"}, res.Groups[1].Topics)
	assert.Empty(t, res.IndividualTopics)
}

func TestIsInternalTopic(t *testing.T) {
	// Kafka's own internal topics use a double underscore. A single underscore is a
	// naming convention some deployments use for their own topics (_schemas is
	// Schema Registry's, not Kafka's) and must not be swept up.
	cases := map[string]bool{
		"__consumer_offsets":  true,
		"__transaction_state": true,
		"orders":              false,
		"_schemas":            false,
		"":                    false,
	}

	for topic, want := range cases {
		assert.Equal(t, want, IsInternalTopic(topic), "IsInternalTopic(%q)", topic)
	}
}

func TestBuild_InternalTopicsAreFilteredBeforeTheUnion(t *testing.T) {
	// Every exactly-once application commits its consumer offsets inside the
	// transaction, so __consumer_offsets is in the footprint of all of them. It has to
	// be dropped BEFORE the union: filtering it out of the finished components instead
	// would leave the transitive closure already computed through it, chaining every
	// unrelated workload on the cluster into one meaningless group.
	res := Build([]Transaction{
		{ID: "app1", Topics: []string{"orders", "__consumer_offsets"}},
		{ID: "app2", Topics: []string{"payments", "shipments", "__consumer_offsets"}},
	}, Options{})

	require.Len(t, res.Groups, 1, "the two workloads must not chain into one group")
	assert.Equal(t, []string{"payments", "shipments"}, res.Groups[0].Topics)
	assert.Equal(t, []string{"app2"}, res.Groups[0].TxnIDs)
	assert.Equal(t, []string{"orders"}, res.IndividualTopics)
}

func TestBuild_IncludingInternalTopicsChainsEverythingIntoOneGroup(t *testing.T) {
	// The guard rail for the test above: it proves the chaining is real rather than
	// hypothetical, so if someone "simplifies" the filter away the previous test's
	// failure has an explanation sitting next to it. Two workloads with nothing in
	// common fuse into one group the moment the shared internal topic is kept.
	res := Build([]Transaction{
		{ID: "app1", Topics: []string{"orders", "__consumer_offsets"}},
		{ID: "app2", Topics: []string{"payments", "__consumer_offsets"}},
	}, Options{IncludeInternalTopics: true})

	require.Len(t, res.Groups, 1)
	assert.Equal(t, []string{"__consumer_offsets", "orders", "payments"}, res.Groups[0].Topics)
	assert.Empty(t, res.IndividualTopics)
}

func TestBuild_ReadProcessWriteTopicsAreFlaggedEvenWhenUngrouped(t *testing.T) {
	// A consume-transform-produce transaction's CONSUMED topics are invisible in its
	// footprint, so a read-process-write app producing to one topic looks like a safe
	// "move it on its own" topic when it is not. The flag has to survive landing in
	// IndividualTopics, which is exactly the case that would otherwise read as safe.
	res := Build([]Transaction{
		{ID: "app1", Topics: []string{"orders", "__consumer_offsets"}, ReadProcessWrite: true},
		{ID: "app2", Topics: []string{"payments", "shipments", "__consumer_offsets"}, ReadProcessWrite: true},
		{ID: "app3", Topics: []string{"x", "y"}},
	}, Options{})

	require.Len(t, res.Groups, 2)
	assert.Equal(t, []string{"payments", "shipments"}, res.Groups[0].Topics)
	assert.True(t, res.Groups[0].ReadProcessWrite)
	assert.Equal(t, []string{"x", "y"}, res.Groups[1].Topics)
	assert.False(t, res.Groups[1].ReadProcessWrite, "a plain produce-only transaction is not read-process-write")

	assert.Equal(t, []string{"orders"}, res.IndividualTopics)
	assert.Equal(t, []string{"orders", "payments", "shipments"}, res.ReadProcessWriteTopics,
		"the lone produced topic must stay flagged despite grouping alone")
	assert.NotContains(t, res.ReadProcessWriteTopics, "__consumer_offsets",
		"the filtered internal topic must not reappear here")
}

func TestBuild_OutputIsDeterministicAcrossRuns(t *testing.T) {
	// Components are bucketed through Go maps, whose iteration order is randomised on
	// every range. Two runs over the same observations must still produce identical
	// group names, identical membership order within each group, and identical ordering
	// between groups — otherwise a re-run of the command produces a diff that looks
	// like the cluster changed. Every group here is the same size, so the size
	// comparison alone cannot order them and the tiebreak has to carry it.
	txns := []Transaction{
		{ID: "t-zulu", Topics: []string{"zulu", "yankee"}},
		{ID: "t-alpha", Topics: []string{"alpha", "bravo"}, ReadProcessWrite: true},
		{ID: "t-mike", Topics: []string{"mike", "november"}},
		{ID: "t-delta", Topics: []string{"delta", "echo"}},
		{ID: "t-papa", Topics: []string{"papa", "quebec"}},
		{ID: "t-sierra", Topics: []string{"sierra", "tango"}},
		{ID: "t-solo", Topics: []string{"solo", "__consumer_offsets"}, ReadProcessWrite: true},
		{ID: "t-lone", Topics: []string{"lone"}},
	}

	first := Build(txns, Options{})
	require.Len(t, first.Groups, 6, "fixture must produce several equal-sized groups")

	for run := 1; run < 200; run++ {
		assert.Equal(t, first, Build(txns, Options{}), "run %d differed from run 0", run)
	}
}
