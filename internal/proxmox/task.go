package proxmox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxTransientPollErrors bounds consecutive transient errors while polling a task.
const maxTransientPollErrors = 5

// transient reports errors worth retrying while polling: proxy errors (595/596
// during a pveproxy reload, 502-504) and network errors.
func transient(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 502, 503, 504, 595, 596:
			return true
		}
		return false
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// WaitTask polls a task until it stops. Exit status "OK" or "WARNINGS: n" is success.
func (c *Client) WaitTask(ctx context.Context, node, upid string) error {
	path := fmt.Sprintf("/nodes/%s/tasks/%s", url.PathEscape(node), url.PathEscape(upid))
	failures := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var st struct {
			Status     string `json:"status"`
			ExitStatus string `json:"exitstatus"`
		}
		if err := c.do(ctx, http.MethodGet, path+"/status", nil, &st); err != nil {
			failures++
			if !transient(err) || failures > maxTransientPollErrors {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.PollInterval):
			}
			continue
		}
		failures = 0
		if st.Status == "stopped" {
			if st.ExitStatus == "OK" || strings.HasPrefix(st.ExitStatus, "WARNINGS") {
				return nil
			}
			return fmt.Errorf("proxmox task %s failed: %s%s", upid, st.ExitStatus, c.taskLogTail(ctx, path))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.PollInterval):
		}
	}
}

func (c *Client) taskLogTail(ctx context.Context, path string) string {
	var lines []struct {
		T string `json:"t"`
	}
	if err := c.do(ctx, http.MethodGet, path+"/log", url.Values{"limit": {"200"}}, &lines); err != nil || len(lines) == 0 {
		return ""
	}
	if len(lines) > 10 {
		lines = lines[len(lines)-10:]
	}
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = l.T
	}
	return "\n" + strings.Join(parts, "\n")
}

// TaskError means the server accepted the request and started a task, but waiting
// for it failed (the task failed, or ctx ended first). The task may still be running.
type TaskError struct {
	UPID string
	Err  error
}

func (e *TaskError) Error() string { return e.Err.Error() }
func (e *TaskError) Unwrap() error { return e.Err }

// postTask issues a request that returns a UPID and waits for the task.
// Errors after the task started are wrapped in *TaskError.
func (c *Client) postTask(ctx context.Context, node, method, path string, params url.Values) error {
	var upid string
	if err := c.do(ctx, method, path, params, &upid); err != nil {
		return err
	}
	if err := c.WaitTask(ctx, node, upid); err != nil {
		return &TaskError{UPID: upid, Err: err}
	}
	return nil
}
