package agent

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"

	"github.com/cocardoso/gh-runners-manager/internal/ingest"
)

// Runner runs the GitHub Actions runner once with a JIT config.
type Runner struct {
	Dir    string // runner install directory
	Script string // run.sh
	JIT    string
	UID    uint32 // run as this user when non-zero
	GID    uint32
	Env    []string
	OnLine func(line string)
}

// Run starts the runner and waits for it, returning its exit code.
func (r Runner) Run(ctx context.Context) (int, error) {
	cmd := exec.CommandContext(ctx, r.Script, "--jitconfig", r.JIT)
	cmd.Dir = r.Dir
	cmd.Env = r.Env
	if r.UID != 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: r.UID, Gid: r.GID}}
	}
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			if r.OnLine != nil {
				r.OnLine(sc.Text())
			}
		}
		_, _ = io.Copy(io.Discard, pr)
	}()
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		wg.Wait()
		return -1, err
	}
	err := cmd.Wait()
	_ = pw.Close()
	wg.Wait()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
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
