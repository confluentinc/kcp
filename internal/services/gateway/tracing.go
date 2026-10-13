package gateway

import "go.opentelemetry.io/otel/attribute"

// tracerName is the instrumentation scope of this package's spans.
const tracerName = "kcp/services/gateway"

// stepAttr names the transition a verifier call serves ("fence", "switch",
// "hot-reload check", …), so fence, switch and unfence spans are told apart.
func stepAttr(step string) attribute.KeyValue { return attribute.String("kcp.gateway.step", step) }
