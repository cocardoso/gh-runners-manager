// Package layer holds the ghrm layer added on top of the official ubuntu-slim image (spec §8.2).
package layer

import (
	"archive/tar"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"
)

// Version identifies the layer. Bump it with every change to the files in this directory:
// a new version triggers a template rebuild (spec §8.5).
const Version = "3"

//go:embed Dockerfile ghrm-agent.service apt-ipv4.conf persist-env.sh mirrors.sh
var files embed.FS

// Files returns the layer files.
func Files() fs.FS { return files }

// Tar writes the docker build context: the layer files plus the ghrm-agent binary.
func Tar(w io.Writer, agent io.Reader, agentSize int64) error {
	tw := tar.NewWriter(w)
	mtime := time.Unix(0, 0)
	for _, name := range []string{"Dockerfile", "ghrm-agent.service", "apt-ipv4.conf", "persist-env.sh", "mirrors.sh"} {
		b, err := fs.ReadFile(files, name)
		if err != nil {
			return err
		}
		mode := int64(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(b)), ModTime: mtime}); err != nil {
			return err
		}
		if _, err := tw.Write(b); err != nil {
			return err
		}
	}
	if err := tw.WriteHeader(&tar.Header{Name: "ghrm-agent", Mode: 0o755, Size: agentSize, ModTime: mtime}); err != nil {
		return err
	}
	n, err := io.Copy(tw, agent)
	if err != nil {
		return err
	}
	if n != agentSize {
		return fmt.Errorf("layer: agent binary is %d bytes, expected %d", n, agentSize)
	}
	return tw.Close()
}
