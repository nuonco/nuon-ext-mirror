package mirror

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// RewriteOptions controls source-file rewriting.
type RewriteOptions struct {
	// Prefix is the same component-name prefix used by ToComponent so the
	// templated references match the generated component TOML files.
	Prefix string

	// DryRun, when true, prints unified-diff style output to stderr instead
	// of writing rewrites back to disk.
	DryRun bool
}

// RewriteSources rewrites every source file in result.Images to replace static
// image references with Nuon templating that points at the generated
// external_image component.
//
// Two YAML forms are handled:
//
//  1. single-line: `image: foo/bar:1.2`
//  2. split form: `image:\n  repository: foo/bar\n  tag: 1.2`
//
// Lines that already contain `{{` or use a digest (`@sha256:...`) are left
// untouched. Files whose path includes a `templates/` segment are skipped
// because they typically already use Helm templating; rewriting them would
// produce nested template directives.
func RewriteSources(result *ScanResult, opts RewriteOptions) error {
	fileToImages := map[string][]*ImageRef{}
	for _, img := range result.Images {
		if img.Digest != "" {
			continue
		}
		for _, src := range img.Sources {
			if isHelmTemplateFile(src) {
				continue
			}
			fileToImages[src] = append(fileToImages[src], img)
		}
	}

	for path, imgs := range fileToImages {
		if err := rewriteFile(path, imgs, opts); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", path, err)
		}
	}
	return nil
}

func isHelmTemplateFile(path string) bool {
	for _, p := range strings.Split(filepath.ToSlash(path), "/") {
		if p == "templates" {
			return true
		}
	}
	return false
}

var (
	// singleImageLineRE matches `image: <value>` on a single line. Captured
	// groups: 1=prefix incl. colon+spaces, 2=open quote, 3=value, 4=close
	// quote, 5=trailing whitespace+comment.
	singleImageLineRE = regexp.MustCompile(`(?m)^([\t ]*-?[\t ]*image:[\t ]*)(['"]?)([^'"#\n\t ]+)(['"]?)([\t ]*(?:#.*)?)$`)

	// splitRepoLineRE / splitTagLineRE match the split form sub-keys. We pair
	// these up by index proximity in rewriteFile.
	splitRepoLineRE = regexp.MustCompile(`(?m)^([\t ]+repository:[\t ]*)(['"]?)([^'"#\n\t ]+)(['"]?)([\t ]*(?:#.*)?)$`)
	splitTagLineRE  = regexp.MustCompile(`(?m)^([\t ]+tag:[\t ]*)(['"]?)([^'"#\n\t ]+)(['"]?)([\t ]*(?:#.*)?)$`)
)

func rewriteFile(path string, imgs []*ImageRef, opts RewriteOptions) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	original := append([]byte(nil), data...)

	// Build lookup tables.
	imageByFullURL := map[string]*ImageRef{}
	for _, img := range imgs {
		imageByFullURL[img.Raw] = img
		imageByFullURL[fmt.Sprintf("%s/%s:%s", img.Registry, img.Repository, img.Tag)] = img
		if img.Registry == "docker.io" {
			repoNoLib := strings.TrimPrefix(img.Repository, "library/")
			imageByFullURL[fmt.Sprintf("%s:%s", repoNoLib, img.Tag)] = img
		}
	}
	byRepoTag := map[[2]string]*ImageRef{}
	for _, img := range imgs {
		byRepoTag[[2]string{img.Repository, img.Tag}] = img
		byRepoTag[[2]string{fmt.Sprintf("%s/%s", img.Registry, img.Repository), img.Tag}] = img
		if img.Registry == "docker.io" {
			repoNoLib := strings.TrimPrefix(img.Repository, "library/")
			byRepoTag[[2]string{repoNoLib, img.Tag}] = img
		}
	}

	// 1) Rewrite single-line `image: <ref>`.
	data = singleImageLineRE.ReplaceAllFunc(data, func(line []byte) []byte {
		m := singleImageLineRE.FindSubmatch(line)
		if m == nil {
			return line
		}
		val := string(m[3])
		if strings.Contains(val, "{{") || strings.Contains(val, "@sha256:") {
			return line
		}
		img, ok := imageByFullURL[val]
		if !ok {
			return line
		}
		comp := ToComponent(img, opts.Prefix)
		full := fmt.Sprintf(
			"{{.nuon.components.%s.outputs.image.repository}}:{{.nuon.components.%s.outputs.image.tag}}",
			comp.Name, comp.Name,
		)
		return []byte(string(m[1]) + "'" + full + "'" + string(m[5]))
	})

	// 2) Rewrite split form: pair each `repository:` line with a nearby
	// `tag:` line and only rewrite if the (repo, tag) pair matches a known
	// image.
	data = rewriteSplitPairs(data, byRepoTag, opts.Prefix)

	if bytes.Equal(original, data) {
		return nil
	}

	if opts.DryRun {
		fmt.Fprintf(os.Stderr, "Would rewrite %s:\n", path)
		printDiff(original, data)
		return nil
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Rewrote %s\n", path)
	return nil
}

type splitLine struct {
	idx    int
	prefix string // up to and including `<key>: `
	quote  string
	value  string
	suffix string // closing quote + trailing whitespace/comment
	key    string // "repository" or "tag"
}

func parseSplitLine(line []byte) (splitLine, bool) {
	if m := splitRepoLineRE.FindSubmatch(line); m != nil {
		return splitLine{
			prefix: string(m[1]),
			quote:  string(m[2]),
			value:  string(m[3]),
			suffix: string(m[5]),
			key:    "repository",
		}, true
	}
	if m := splitTagLineRE.FindSubmatch(line); m != nil {
		return splitLine{
			prefix: string(m[1]),
			quote:  string(m[2]),
			value:  string(m[3]),
			suffix: string(m[5]),
			key:    "tag",
		}, true
	}
	return splitLine{}, false
}

// rewriteSplitPairs finds `repository:` lines and pairs them with a nearby
// `tag:` line (within ±3 lines). When the pair matches a known image, both
// lines are rewritten to use Nuon templating.
func rewriteSplitPairs(data []byte, byRepoTag map[[2]string]*ImageRef, prefix string) []byte {
	lines := bytes.Split(data, []byte("\n"))
	parsed := make([]splitLine, len(lines))
	have := make([]bool, len(lines))
	for i, ln := range lines {
		if sl, ok := parseSplitLine(ln); ok {
			sl.idx = i
			parsed[i] = sl
			have[i] = true
		}
	}

	for i := range lines {
		if !have[i] || parsed[i].key != "repository" {
			continue
		}
		repo := parsed[i].value
		if strings.Contains(repo, "{{") {
			continue
		}
		// Look for the nearest tag line within ±3 lines.
		tagIdx := -1
		for j := i - 3; j <= i+3; j++ {
			if j == i || j < 0 || j >= len(lines) {
				continue
			}
			if have[j] && parsed[j].key == "tag" && !strings.Contains(parsed[j].value, "{{") {
				tagIdx = j
				break
			}
		}
		if tagIdx < 0 {
			continue
		}
		tag := parsed[tagIdx].value
		img, ok := byRepoTag[[2]string{repo, tag}]
		if !ok {
			continue
		}
		comp := ToComponent(img, prefix)
		repoTpl := fmt.Sprintf("{{.nuon.components.%s.outputs.image.repository}}", comp.Name)
		tagTpl := fmt.Sprintf("{{.nuon.components.%s.outputs.image.tag}}", comp.Name)
		lines[i] = []byte(parsed[i].prefix + "'" + repoTpl + "'" + parsed[i].suffix)
		lines[tagIdx] = []byte(parsed[tagIdx].prefix + "'" + tagTpl + "'" + parsed[tagIdx].suffix)
		// Mark consumed so we don't pair the same tag with another repo.
		have[i] = false
		have[tagIdx] = false
	}
	return bytes.Join(lines, []byte("\n"))
}

// printDiff prints a minimal line-level diff to stderr.
func printDiff(a, b []byte) {
	aLines := strings.Split(string(a), "\n")
	bLines := strings.Split(string(b), "\n")
	n := len(aLines)
	if len(bLines) > n {
		n = len(bLines)
	}
	for i := 0; i < n; i++ {
		var av, bv string
		if i < len(aLines) {
			av = aLines[i]
		}
		if i < len(bLines) {
			bv = bLines[i]
		}
		if av == bv {
			continue
		}
		if av != "" {
			fmt.Fprintf(os.Stderr, "  - %s\n", av)
		}
		if bv != "" {
			fmt.Fprintf(os.Stderr, "  + %s\n", bv)
		}
	}
}
