package mirror

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ScannerOptions tunes Scan behavior.
type ScannerOptions struct {
	// IncludeTemplated, when true, attempts to keep image refs that contain
	// Go template directives (best-effort and noisy; off by default).
	IncludeTemplated bool
}

// Scanner walks a directory looking for container image references.
type Scanner struct {
	opts ScannerOptions
}

func NewScanner(opts ScannerOptions) *Scanner {
	return &Scanner{opts: opts}
}

// Scan walks root and returns every image reference it finds.
func (s *Scanner) Scan(root string) (*ScanResult, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("unable to stat %s: %w", root, err)
	}

	result := &ScanResult{
		Images: map[string]*ImageRef{},
	}

	walk := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip vendored / hidden directories.
			name := d.Name()
			if path != root && (name == ".git" || name == "node_modules" || name == "dist" || name == "vendor" ||
				name == "charts" && filepath.Base(filepath.Dir(path)) == "charts") {
				return fs.SkipDir
			}
			return nil
		}
		if !isYAML(path) {
			return nil
		}
		if err := s.scanFile(path, result); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", path, err)
		}
		return nil
	}

	if info.IsDir() {
		if err := filepath.WalkDir(root, walk); err != nil {
			return nil, err
		}
	} else {
		if err := s.scanFile(root, result); err != nil {
			return nil, err
		}
	}

	// Sort and deduplicate file list.
	files := map[string]struct{}{}
	for _, img := range result.Images {
		for _, src := range img.Sources {
			files[src] = struct{}{}
		}
	}
	for f := range files {
		result.Files = append(result.Files, f)
	}
	sort.Strings(result.Files)

	return result, nil
}

func isYAML(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

func (s *Scanner) scanFile(path string, result *ScanResult) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	for _, raw := range extractImages(data, s.opts.IncludeTemplated) {
		img, ok := ParseImage(raw)
		if !ok {
			continue
		}
		key := img.Key()
		if existing, ok := result.Images[key]; ok {
			if !contains(existing.Sources, path) {
				existing.Sources = append(existing.Sources, path)
			}
			continue
		}
		img.Sources = []string{path}
		result.Images[key] = img
	}
	return nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// imageLineRE matches a YAML key/value pair where the key is "image" and the
// value is a single-line string. Captured group 1 is the image string with
// optional surrounding quotes already stripped via the quoteRE pass below.
var imageLineRE = regexp.MustCompile(`(?m)^[\t ]*-?[\t ]*image:[\t ]*(.+?)[\t ]*$`)

// repoTagRE matches Helm-style values blocks like:
//
//	image:
//	  repository: nginx
//	  tag: 1.25
var repoTagRE = regexp.MustCompile(`(?ms)image:\s*\n` +
	`(?:[\t ]+(?:registry|repository|tag|digest):[^\n]*\n){2,}`)

var (
	registryFieldRE   = regexp.MustCompile(`(?m)^[\t ]+registry:[\t ]*([^\n#]+)`)
	repositoryFieldRE = regexp.MustCompile(`(?m)^[\t ]+repository:[\t ]*([^\n#]+)`)
	tagFieldRE        = regexp.MustCompile(`(?m)^[\t ]+tag:[\t ]*([^\n#]+)`)
	digestFieldRE     = regexp.MustCompile(`(?m)^[\t ]+digest:[\t ]*([^\n#]+)`)
)

// extractImages scans a YAML/Helm template byte buffer for image references.
// It first tries a structured YAML walk, then falls back to a regex sweep so
// it can also pick up Helm template files containing Go template directives.
func extractImages(data []byte, includeTemplated bool) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		v = strings.Trim(v, "\"'")
		if v == "" {
			return
		}
		if !includeTemplated && (strings.Contains(v, "{{") || strings.Contains(v, "}}")) {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}

	// Structured YAML walk: handle multi-doc files.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var node yaml.Node
		if err := dec.Decode(&node); err != nil {
			break
		}
		walkYAML(&node, add)
	}

	// Regex sweep on raw bytes — picks up Helm template files where the YAML
	// parser fails because of `{{ ... }}` blocks, plus single-line `image:`
	// fields the structured walker may have missed.
	for _, m := range imageLineRE.FindAllSubmatch(data, -1) {
		val := strings.TrimSpace(string(m[1]))
		val = strings.Trim(val, "\"'")
		add(val)
	}

	// Helm-style image: { repository, tag } blocks.
	for _, block := range repoTagRE.FindAll(data, -1) {
		var repo, tag, registry, digest string
		if m := repositoryFieldRE.FindSubmatch(block); m != nil {
			repo = strings.Trim(strings.TrimSpace(string(m[1])), "\"'")
		}
		if m := tagFieldRE.FindSubmatch(block); m != nil {
			tag = strings.Trim(strings.TrimSpace(string(m[1])), "\"'")
		}
		if m := registryFieldRE.FindSubmatch(block); m != nil {
			registry = strings.Trim(strings.TrimSpace(string(m[1])), "\"'")
		}
		if m := digestFieldRE.FindSubmatch(block); m != nil {
			digest = strings.Trim(strings.TrimSpace(string(m[1])), "\"'")
		}
		if repo == "" {
			continue
		}
		joined := repo
		if registry != "" {
			joined = registry + "/" + repo
		}
		switch {
		case digest != "":
			add(joined + "@" + digest)
		case tag != "":
			add(joined + ":" + tag)
		default:
			add(joined)
		}
	}

	return out
}

// walkYAML recursively walks a yaml.Node tree adding any "image" mapping
// values it finds.
func walkYAML(n *yaml.Node, add func(string)) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			walkYAML(c, add)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			v := n.Content[i+1]
			if k.Kind == yaml.ScalarNode && k.Value == "image" && v.Kind == yaml.ScalarNode {
				add(v.Value)
			}
			walkYAML(v, add)
		}
	}
}
