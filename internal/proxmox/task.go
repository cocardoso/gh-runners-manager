package proxmox

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WaitTask polls a task until it stops. Exit status "OK" or "WARNINGS: n" is success.
func (c *Client) WaitTask(ctx context.Context, node, upid string) error {
	path := fmt.Sprintf("/nodes/%s/tasks/%s", url.PathEscape(node), url.PathEscape(upid))
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var st struct {
			Status     string `json:"status"`
			ExitStatus string `json:"exitstatus"`
		}
		if err := c.do(ctx, http.MethodGet, path+"/status", nil, &st); err != nil {
			return err
		}
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

// postTask issues a request that returns a UPID and waits for the task.
func (c *Client) postTask(ctx context.Context, node, method, path string, params url.Values) error {
	var upid string
	if err := c.do(ctx, method, path, params, &upid); err != nil {
		return err
	}
	return c.WaitTask(ctx, node, upid)
}
