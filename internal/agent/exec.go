package agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// Commander runs external commands for the build and self-test modes (a fake in tests).
type Commander interface {
	// Run runs a command and passes every output line (stdout and stderr) to out.
	Run(ctx context.Context, dir, name string, args []string, out func(string)) error
	// Stream runs a command whose stdout is data written to stdout; stderr lines go to out.
	Stream(ctx context.Context, dir, name string, args []string, stdout io.Writer, out func(string)) error
}

// OSCommander runs real processes.
type OSCommander struct{}

func lines(r io.Reader, out func(string), wg *sync.WaitGroup) {
	defer wg.Done()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		out(sc.Text())
	}
	_, _ = io.Copy(io.Discard, r)
}

// Run implements Commander.
func (OSCommander) Run(ctx context.Context, dir, name string, args []string, out func(string)) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	var wg sync.WaitGroup
	wg.Add(1)
	go lines(pr, out, &wg)
	err := cmd.Run()
	_ = pw.Close()
	wg.Wait()
	if err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

// Stream implements Commander.
func (OSCommander) Stream(ctx context.Context, dir, name string, args []string, stdout io.Writer, out func(string)) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	pr, pw := io.Pipe()
	cmd.Stderr = pw
	var wg sync.WaitGroup
	wg.Add(1)
	go lines(pr, out, &wg)
	err := cmd.Run()
	_ = pw.Close()
	wg.Wait()
	if err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}
