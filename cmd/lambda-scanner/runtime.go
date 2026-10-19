package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	// Lambda's synchronous response limit is 6 MB; stay under it.
	maxResponseBytes = 5_500_000
	// Time reserved before the Lambda deadline to collect state and respond.
	deadlineMargin = 30 * time.Second
)

// runtimeLoop implements the Lambda custom runtime API: poll for an invocation, handle it, post
// the response. It only returns on a runtime API error.
func runtimeLoop(apiHost string, h *handler) error {
	api := "http://" + apiHost + "/2018-06-01/runtime/invocation/"
	h.cleanStale()
	for {
		resp, err := http.Get(api + "next")
		if err != nil {
			return fmt.Errorf("failed to get next invocation: %w", err)
		}
		id := resp.Header.Get("Lambda-Runtime-Aws-Request-Id")
		if resp.StatusCode < 200 || resp.StatusCode > 299 || id == "" {
			_ = resp.Body.Close()
			return fmt.Errorf("unexpected response from runtime API /next: status %d, request id present: %t", resp.StatusCode, id != "")
		}
		deadlineMs, _ := strconv.ParseInt(resp.Header.Get("Lambda-Runtime-Deadline-Ms"), 10, 64)
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return fmt.Errorf("failed to read invocation body: %w", err)
		}

		ctx, cancel := invocationContext(deadlineMs)

		var res result
		var ev event
		if err := json.Unmarshal(body, &ev); err != nil {
			// The body carries credentials; json errors do not echo input values.
			res = result{Error: "bad event: " + err.Error(), fatal: true}
		} else {
			res = h.handle(ctx, ev)
		}
		cancel()

		// Never log the payload (it carries credentials); log only a summary.
		for _, s := range res.Steps {
			h.logger.Info("step finished", "step", s.Step, "exit_code", s.ExitCode, "duration_ms", s.DurationMs)
		}

		if res.fatal {
			errBody, _ := json.Marshal(map[string]string{"errorMessage": res.Error, "errorType": "InvalidInvocation"})
			err = post(api+id+"/error", errBody)
		} else {
			err = post(api+id+"/response", fitResponse(res))
		}
		if err != nil {
			return err
		}
	}
}

// fitResponse marshals res; if it would exceed the Lambda response limit it first drops the log
// tails and warnings, and only then the state, noting what was dropped in the error.
func fitResponse(res result) []byte {
	out, err := json.Marshal(res)
	if err != nil {
		return slimResponse(res, "failed to marshal response: "+err.Error())
	}
	if len(out) <= maxResponseBytes {
		return out
	}
	size := len(out)

	noLogs := res
	noLogs.Steps = make([]stepResult, len(res.Steps))
	for i, s := range res.Steps {
		s.LogTail, s.Warnings = "", nil
		noLogs.Steps[i] = s
	}
	noLogs.addError(fmt.Sprintf("response too large (%d bytes, limit %d); logs dropped", size, maxResponseBytes))
	if out, err = json.Marshal(noLogs); err == nil && len(out) <= maxResponseBytes {
		return out
	}
	return slimResponse(res, fmt.Sprintf("response too large (%d bytes, limit %d); state and logs dropped", size, maxResponseBytes))
}

// slimResponse reports only step outcomes, with msg appended to any existing error.
func slimResponse(res result, msg string) []byte {
	slim := result{Steps: make([]stepResult, len(res.Steps)), DurationMs: res.DurationMs, StateBytes: res.StateBytes, Error: res.Error}
	for i, s := range res.Steps {
		slim.Steps[i] = stepResult{Step: s.Step, ExitCode: s.ExitCode, DurationMs: s.DurationMs}
	}
	slim.addError(msg)
	out, _ := json.Marshal(slim)
	return out
}

func post(url string, body []byte) error {
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to post to runtime API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("runtime API returned status %d", resp.StatusCode)
	}
	return nil
}

// invocationContext returns a context that ends deadlineMargin before the Lambda deadline
// (epoch milliseconds); with no deadline it is cancel-only.
func invocationContext(deadlineMs int64) (context.Context, context.CancelFunc) {
	if deadlineMs <= 0 {
		return context.WithCancel(context.Background())
	}
	return context.WithDeadline(context.Background(), time.UnixMilli(deadlineMs).Add(-deadlineMargin))
}
