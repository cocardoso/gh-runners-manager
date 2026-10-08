package agent

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"

	"github.com/cocardoso/gh-runners-manager/internal/ingest"
)

// RunnerImagesURL is the repository holding GitHub's image recipes (spec §8.1).
const RunnerImagesURL = "https://github.com/actions/runner-images"

// exportSkip are files a container runtime adds; the template must not carry them.
var exportSkip = map[string]bool{".dockerenv": true, "run/.containerenv": true}

// RunBuild builds a template root filesystem (spec §8.3): the official ubuntu-slim
// Dockerfile, unmodified, then the ghrm layer, exported and uploaded to the control plane.
func RunBuild(ctx context.Context, c *Client, cmd Commander, work string) (err error) {
	step := ""
	log := func(line string) { c.Log("build", line) }
	begin := func(name, msg string) {
		step = name
		c.Event(ingest.EventBuildStep, map[string]any{"step": name})
		log("==> " + msg)
	}
	defer func() {
		if err != nil {
			log("build failed during " + step + ": " + err.Error())
			c.Event(ingest.EventBuildFailed, map[string]any{"step": step, "error": err.Error()})
		}
	}()

	begin("spec", "reading the build spec")
	var spec ingest.BuildSpec
	if err := c.GetJSON(ctx, ingest.BuildSpecPath, &spec); err != nil {
		return err
	}
	log(fmt.Sprintf("template %s: %s, runner %s, layer %s", spec.TemplateID, spec.SlimTag, spec.RunnerVersion, spec.LayerVersion))
	slim, tpl := "ghrm-slim:"+spec.TemplateID, "ghrm-tpl:"+spec.TemplateID
	recipe := filepath.Join(work, "runner-images")
	layerDir := filepath.Join(work, "layer")

	begin("clone", "cloning "+RunnerImagesURL+" at "+spec.SlimTag)
	_ = os.RemoveAll(recipe)
	if err := cmd.Run(ctx, work, "git", []string{"clone", "--depth", "1", "--branch", spec.SlimTag, RunnerImagesURL, recipe}, log); err != nil {
		return err
	}

	if len(spec.Remove) > 0 {
		begin("recipe", "leaving out "+strings.Join(spec.Remove, ", ")+" (template profile)")
		dockerfile := filepath.Join(recipe, "images", "ubuntu-slim", "Dockerfile")
		b, err := os.ReadFile(dockerfile)
		if err != nil {
			return err
		}
		edited, err := RemoveComponents(string(b), spec.Remove)
		if err != nil {
			return fmt.Errorf("%w at %s", err, spec.SlimTag)
		}
		if err := os.WriteFile(dockerfile, []byte(edited), 0o644); err != nil {
			return err
		}
	}

	begin("slim", "building the official ubuntu-slim image")
	// GitHub builds the image with its release as IMAGE_VERSION; the software report shows it.
	imageVersion := strings.TrimPrefix(spec.SlimTag, "ubuntu-slim/")
	if err := cmd.Run(ctx, work, "docker", []string{"build", "--progress=plain", "--build-arg", "IMAGE_VERSION=" + imageVersion,
		"-t", slim, filepath.Join(recipe, "images", "ubuntu-slim")}, log); err != nil {
		return err
	}

	begin("layer", "building the ghrm layer")
	if err := os.MkdirAll(layerDir, 0o755); err != nil {
		return err
	}
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(c.Download(ctx, ingest.BuildLayerPath, pw)) }()
	if err := untar(pr, layerDir); err != nil {
		_ = pr.CloseWithError(err)
		return fmt.Errorf("unpack layer: %w", err)
	}
	if err := cmd.Run(ctx, work, "docker", []string{"build", "--progress=plain",
		"--build-arg", "BASE=" + slim, "--build-arg", "RUNNER_VERSION=" + spec.RunnerVersion,
		"--build-arg", "RUNNER_SHA256=" + spec.RunnerSHA256, "--build-arg", "LAYER_VERSION=" + spec.LayerVersion,
		"--build-arg", "CACHE_MIRRORS=" + spec.CacheMirrors, "-t", tpl, layerDir}, log); err != nil {
		return err
	}

	begin("export", "exporting the root filesystem")
	name := "ghrm-export-" + spec.TemplateID
	_ = cmd.Run(ctx, work, "docker", []string{"rm", "-f", name}, func(string) {})
	if err := cmd.Run(ctx, work, "docker", []string{"create", "--name", name, tpl}, log); err != nil {
		return err
	}
	archive := filepath.Join(work, "rootfs.tar.zst")
	sum, size, err := exportArchive(ctx, cmd, work, name, archive, log)
	if err != nil {
		return err
	}
	log(fmt.Sprintf("archive: %d bytes, sha256 %s", size, sum))

	begin("upload", "uploading the root filesystem")
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := c.Send(ctx, "PUT", ingest.BuildRootFSPath, f, size, map[string]string{ingest.HeaderSHA256: sum, "Content-Type": "application/zstd"}); err != nil {
		return err
	}
	log("build finished")
	c.Event(ingest.EventBuildFinished, map[string]any{"sha256": sum, "size": size})
	return nil
}

// exportArchive streams `docker export` through a filter into a zstd file, hashing it.
func exportArchive(ctx context.Context, cmd Commander, dir, container, path string, log func(string)) (string, int64, error) {
	f, err := os.Create(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	counter := &countingWriter{w: io.MultiWriter(f, h)}
	enc, err := zstd.NewWriter(counter, zstd.WithEncoderLevel(zstd.SpeedBetterCompression))
	if err != nil {
		return "", 0, err
	}
	pr, pw := io.Pipe()
	filterErr := make(chan error, 1)
	go func() {
		err := filterTar(pr, enc)
		_ = pr.CloseWithError(err)
		filterErr <- err
	}()
	streamErr := cmd.Stream(ctx, dir, "docker", []string{"export", container}, pw, log)
	_ = pw.CloseWithError(streamErr)
	if err := errors.Join(streamErr, <-filterErr, enc.Close()); err != nil {
		return "", 0, err
	}
	if err := f.Sync(); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), counter.n, nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// filterTar copies a tar stream, dropping the container runtime's marker files.
func filterTar(r io.Reader, w io.Writer) error {
	tr := tar.NewReader(r)
	tw := tar.NewWriter(w)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return tw.Close()
		}
		if err != nil {
			return err
		}
		if exportSkip[strings.TrimPrefix(h.Name, "./")] {
			continue
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return err
		}
	}
}

// untar extracts regular files and directories into dir, refusing paths that escape it.
func untar(r io.Reader, dir string) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dir, h.Name)
		if !strings.HasPrefix(target, filepath.Clean(dir)+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe path %q", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0o777)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported entry %q (type %s)", h.Name, strconv.Itoa(int(h.Typeflag)))
		}
	}
}

// RemoveComponents drops the install scripts of the given components from the recipe's
// Dockerfile, whose RUN chain calls each as "/tmp/scripts/build/install-<id>.sh && \".
// A component the recipe does not call is an error: the recipe changed under the profile.
func RemoveComponents(dockerfile string, ids []string) (string, error) {
	lines := strings.Split(dockerfile, "\n")
	for _, id := range ids {
		call := "/tmp/scripts/build/install-" + id + ".sh && \\"
		found := false
		kept := lines[:0:0]
		for _, l := range lines {
			if strings.TrimSpace(l) == call {
				found = true
				continue
			}
			kept = append(kept, l)
		}
		if !found {
			return "", fmt.Errorf("the recipe does not install %q (install-%s.sh)", id, id)
		}
		lines = kept
	}
	return strings.Join(lines, "\n"), nil
}
