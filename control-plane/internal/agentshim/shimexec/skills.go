package shimexec

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
)

// checkSkills validates the skill name and refuses when the shim does not
// declare the skills capability. The capability check reads the cached meta
// document, so callers probing for support (e.g. best-effort removal on
// connection teardown) never pay for a verb exec on unsupported agents.
func (c *Client) checkSkills(ctx context.Context, name string) error {
	// A skill name is a single path segment: the verb installs it as
	// <skills_dir>/<name>.
	if !agentshim.SkillPathSafe(name) || strings.Contains(name, "/") {
		return fmt.Errorf("invalid skill name %q", name)
	}
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return err
	}
	if !caps.Skills {
		return agentshim.ErrSkillsUnsupported
	}
	return nil
}

// skillErr keeps both ErrSkillsUnsupported and ErrUnsupported in the chain
// when the shim answers a skill verb with exit 3.
func skillErr(err error) error {
	if errors.Is(err, ErrUnsupported) {
		return fmt.Errorf("%w: %w", agentshim.ErrSkillsUnsupported, err)
	}
	return err
}

// DeploySkill implements agentshim.Client: packs the skill's files into an
// uncompressed tar and streams it to the skill-install verb, which replaces
// <skills_dir>/<name> wholesale.
func (c *Client) DeploySkill(ctx context.Context, name string, files map[string][]byte) error {
	if err := c.checkSkills(ctx, name); err != nil {
		return err
	}
	archive, err := buildSkillTar(files)
	if err != nil {
		return err
	}
	if _, err := c.run(ctx, bytes.NewReader(archive), "skill-install", "--name", name); err != nil {
		return skillErr(err)
	}
	return nil
}

// RemoveSkill implements agentshim.Client via the skill-remove verb.
// Removing a skill that was never deployed is not an error.
func (c *Client) RemoveSkill(ctx context.Context, name string) error {
	if err := c.checkSkills(ctx, name); err != nil {
		return err
	}
	if _, err := c.run(ctx, nil, "skill-remove", "--name", name); err != nil {
		return skillErr(err)
	}
	return nil
}

// buildSkillTar renders files (paths relative to the skill root) as a
// deterministic tar: entries sorted by path, with explicit directory entries
// for every intermediate directory. Files are 0644, or 0755 when they start
// with a shebang.
func buildSkillTar(files map[string][]byte) ([]byte, error) {
	clean := make(map[string][]byte, len(files))
	for rel, data := range files {
		p := path.Clean(rel)
		if !agentshim.SkillPathSafe(p) {
			return nil, fmt.Errorf("invalid skill file path %q", rel)
		}
		if _, dup := clean[p]; dup {
			return nil, fmt.Errorf("duplicate skill file path %q", p)
		}
		clean[p] = data
	}

	dirs := map[string]bool{}
	for p := range clean {
		for d := path.Dir(p); d != "." && d != "/"; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	for d := range dirs {
		if _, isFile := clean[d]; isFile {
			return nil, fmt.Errorf("skill path %q is both a file and a directory", d)
		}
	}

	type entry struct {
		name string
		dir  bool
	}
	entries := make([]entry, 0, len(clean)+len(dirs))
	for d := range dirs {
		entries = append(entries, entry{name: d, dir: true})
	}
	for p := range clean {
		entries = append(entries, entry{name: p})
	}
	// Sorting by path puts every directory ahead of its contents ("a" < "a/…").
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		var hdr *tar.Header
		if e.dir {
			hdr = &tar.Header{Typeflag: tar.TypeDir, Name: e.name + "/", Mode: 0o755}
		} else {
			data := clean[e.name]
			mode := int64(0o644)
			if bytes.HasPrefix(data, []byte("#!")) {
				mode = 0o755
			}
			hdr = &tar.Header{Typeflag: tar.TypeReg, Name: e.name, Mode: mode, Size: int64(len(data))}
		}
		// A fixed epoch mtime keeps the archive deterministic and
		// representable in plain USTAR (the zero time.Time is not).
		hdr.ModTime = time.Unix(0, 0)
		hdr.Format = tar.FormatUSTAR
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("write skill archive: %w", err)
		}
		if !e.dir {
			if _, err := tw.Write(clean[e.name]); err != nil {
				return nil, fmt.Errorf("write skill archive: %w", err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("write skill archive: %w", err)
	}
	return buf.Bytes(), nil
}
