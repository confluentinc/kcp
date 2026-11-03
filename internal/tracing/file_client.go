package tracing

import (
	"context"
	"os"
	"sync"

	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// fileClient is an otlptrace.Client that appends each export to a file as one
// line of OTLP/JSON. otlptrace.New turns the SDK's spans into OTLP protobuf;
// this client only encodes and writes. The JSON comes from the Collector's own
// pdata marshaler, which follows the OTLP/JSON rules (hex trace and span IDs,
// integer enums) that plain protojson does not, so the file can be replayed
// into any OTLP backend with the Collector's otlpjsonfile receiver.
type fileClient struct {
	mu sync.Mutex
	f  *os.File
}

var _ otlptrace.Client = (*fileClient)(nil)

func (c *fileClient) Start(context.Context) error { return nil }

func (c *fileClient) Stop(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.f.Close()
}

// UploadTraces may be called concurrently, so writes are serialised.
func (c *fileClient) UploadTraces(_ context.Context, rs []*tracepb.ResourceSpans) error {
	bin, err := proto.Marshal(&coltracepb.ExportTraceServiceRequest{ResourceSpans: rs})
	if err != nil {
		return err
	}
	req := ptraceotlp.NewExportRequest()
	if err := req.UnmarshalProto(bin); err != nil {
		return err
	}
	line, err := req.MarshalJSON()
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.f.Write(append(line, '\n'))
	return err
}
