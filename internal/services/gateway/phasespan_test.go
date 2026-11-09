package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/confluentinc/kcp/internal/tracing/tracingtest"
)

func TestPhaseSpan_EnterTwiceOpensOneSpan(t *testing.T) {
	rec := tracingtest.Install(t)
	var p phaseSpan
	p.enter(context.Background(), "gateway.some_phase", attribute.Int("kcp.n", 1))
	p.enter(context.Background(), "gateway.some_phase", attribute.Int("kcp.n", 2))
	p.leave(nil)
	p.leave(nil)

	spans := tracingtest.FindAll(tracingtest.Spans(rec), "gateway.some_phase")
	require.Len(t, spans, 1, "a repeated enter and a repeated leave must not open or close a second span")
	n, _ := tracingtest.Attr(spans[0], "kcp.n")
	assert.Equal(t, int64(1), n.AsInt64(), "the first enter's attributes are the ones recorded")
}

func TestPhaseSpan_LeaveWithoutEnterRecordsNothing(t *testing.T) {
	rec := tracingtest.Install(t)
	var p phaseSpan
	p.leave(errors.New("ignored"))
	assert.Empty(t, tracingtest.Spans(rec))
}

func TestPhaseSpan_LeaveRecordsTheError(t *testing.T) {
	rec := tracingtest.Install(t)
	var p phaseSpan
	p.enter(context.Background(), "gateway.some_phase")
	p.leave(errors.New("boom"))

	span := tracingtest.Find(t, tracingtest.Spans(rec), "gateway.some_phase")
	assert.Equal(t, codes.Error, span.Status().Code)
}
