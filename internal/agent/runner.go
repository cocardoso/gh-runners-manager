package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/ingest"
)

// Runner runs the GitHub Actions runner once with a JIT config.
type Runner struct {
	Dir    string // runner install directory
	Script string // run.sh
	JIT    string
	UID    uint32 // run as this user when non-zero
	GID    uint32
	Groups []uint32 // supplementary groups (docker access needs the docker group)
	Env    []string
	// WaitDelay bounds how long Run waits for output after the runner exits
	// (a lingering child can keep stdout open). Default 10s.
	WaitDelay time.Duration
	OnLine    func(line string)
}

// Run starts the runner and waits for it, returning its exit code.
func (r Runner) Run(ctx context.Context) (int, error) {
	cmd := exec.CommandContext(ctx, r.Script, "--jitconfig", r.JIT)
	cmd.Dir = r.Dir
	cmd.Env = r.Env
	cmd.SysProcAttr = r.sysProcAttr()
	// On cancellation, kill the whole process group, not only run.sh.
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = r.WaitDelay
	if cmd.WaitDelay <= 0 {
		cmd.WaitDelay = 10 * time.Second
	}
	// An io.Pipe (not *os.File) makes exec copy the output in its own goroutine,
	// which WaitDelay bounds; reading continues until the writer is closed below.
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	done := make(chan struct{})
	go func() {
		defer close(done)
		readLines(pr, func(line string) {
			if r.OnLine != nil {
				r.OnLine(line)
			}
		})
		_, _ = io.Copy(io.Discard, pr)
	}()
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		<-done
		return -1, err
	}
	err := cmd.Wait()
	_ = pw.Close()
	<-done
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		return -1, err
	}
	return cmd.ProcessState.ExitCode(), nil
}

// readLines calls fn for every line; lines longer than MaxFrameText are cut, the
// rest of such a line is skipped, and reading continues with the next line.
func readLines(r io.Reader, fn func(string)) {
	br := bufio.NewReaderSize(r, 64*1024)
	var cur []byte
	skipped := 0
	for {
		chunk, err := br.ReadSlice('\n')
		switch {
		case len(cur)+len(chunk) <= MaxFrameText:
			cur = append(cur, chunk...)
		case len(cur) < MaxFrameText:
			room := MaxFrameText - len(cur)
			cur = append(cur, chunk[:room]...)
			skipped += len(chunk) - room
		default:
			skipped += len(chunk)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if len(cur) > 0 || skipped > 0 {
			line := strings.TrimRight(string(cur), "\r\n")
			if skipped > 0 {
				line += fmt.Sprintf(" …[truncated %d bytes]", skipped)
			}
			fn(line)
		}
		cur, skipped = cur[:0], 0
		if err != nil {
			return
		}
	}
}

func (r Runner) sysProcAttr() *syscall.SysProcAttr {
	attr := &syscall.SysProcAttr{Setpgid: true}
	if r.UID != 0 {
		attr.Credential = &syscall.Credential{Uid: r.UID, Gid: r.GID, Groups: r.Groups}
	}
	return attr
}

var jobRequest = regexp.MustCompile(`Job request \d+ for plan \S+ job (\S+) received`)

// JobRecordID extracts the job's timeline record ID from a runner diagnostic line.
// The job's full log is written to _diag/pages/<plan>_<record id>_<page>.log.
func JobRecordID(line string) (string, bool) {
	if m := jobRequest.FindStringSubmatch(line); m != nil {
		return m[1], true
	}
	return "", false
}

var jobResult = regexp.MustCompile(`Job (.+) completed with result: (\w+)`)

// ClassifyLine maps a runner output line to an agent event name ("" for none).
func ClassifyLine(line string) (string, map[string]any) {
	switch {
	case strings.Contains(line, "Listening for Jobs"):
		return ingest.EventRunnerOnline, nil
	case strings.Contains(line, "Running job:"):
		_, job, _ := strings.Cut(line, "Running job:")
		return ingest.EventJobStarted, map[string]any{"job": strings.TrimSpace(job)}
	}
	if m := jobResult.FindStringSubmatch(line); m != nil {
		return ingest.EventJobFinished, map[string]any{"job": m[1], "result": m[2]}
	}
	return "", nil
}
