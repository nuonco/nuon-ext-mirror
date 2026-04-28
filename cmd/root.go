package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/nuonco/nuon-ext-mirror/internal/mirror"
	"github.com/spf13/cobra"
)

var BuildVersion = "dev"

func NewRootCmd() *cobra.Command {
	var (
		output           string
		prefix           string
		format           string
		dryRun           bool
		includeTemplated bool
		rewriteSources   bool
		showVersion      bool
	)

	cmd := &cobra.Command{
		Use:   "nuon-ext-mirror [PATH]",
		Short: "Mirror container images from Helm charts and Kubernetes manifests as Nuon external_image components",
		Long: `Scan a directory of Helm charts and/or Kubernetes manifests for container
image references and generate Nuon external_image component TOML files that
can be added to a Nuon app config to mirror those images into customer clouds.

If no PATH is given, the current directory is scanned.`,
		Example: strings.Join([]string{
			"  nuon mirror                          : scan the current directory and list images",
			"  nuon mirror <PATH>                   : scan <PATH> and list images",
			"  nuon mirror -o, --output <DIR>       : write external_image components into <DIR>",
			"  nuon mirror -p, --prefix <PREFIX>    : prefix component names (default: \"image-\")",
			"  nuon mirror -f, --format <FORMAT>    : output format for listing: text|json (default: text)",
			"      --include-templated              : include image refs that look templated (best-effort)",
			"      --dry-run                        : print components to stdout instead of writing files",
			"      --rewrite-sources                : rewrite scanned files to reference the generated components via Nuon templating",
		}, "\n"),
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				fmt.Println(BuildVersion)
				return nil
			}

			path := "."
			if len(args) == 1 {
				path = args[0]
			}

			scanner := mirror.NewScanner(mirror.ScannerOptions{
				IncludeTemplated: includeTemplated,
			})

			result, err := scanner.Scan(path)
			if err != nil {
				return err
			}

			// Rewriting sources without also generating components leaves
			// the rewritten files pointing at components that don't exist.
			// Require either an output dir or --dry-run.
			if rewriteSources && output == "" && !dryRun {
				return fmt.Errorf("--rewrite-sources requires either --output <DIR> (so the referenced components exist) or --dry-run")
			}

			if output != "" || dryRun {
				if err := generate(result, output, prefix, dryRun); err != nil {
					return err
				}
			} else {
				if err := list(result, format); err != nil {
					return err
				}
			}

			if rewriteSources {
				return mirror.RewriteSources(result, mirror.RewriteOptions{
					Prefix: prefix,
					DryRun: dryRun,
				})
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "", "Directory to write external_image component TOML files into")
	cmd.Flags().StringVarP(&prefix, "prefix", "p", "image-", "Prefix to use for generated component names")
	cmd.Flags().StringVarP(&format, "format", "f", "text", "Output format for listing: text|json")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print generated components and proposed source-file rewrites instead of writing")
	cmd.Flags().BoolVar(&includeTemplated, "include-templated", false, "Include image refs that look templated (best-effort)")
	cmd.Flags().BoolVar(&rewriteSources, "rewrite-sources", false, "Rewrite scanned files to reference the generated components via Nuon templating")
	cmd.Flags().BoolVarP(&showVersion, "version", "V", false, "Print the extension version")

	return cmd
}

func list(result *mirror.ScanResult, format string) error {
	switch format {
	case "json":
		return printJSON(result)
	case "text", "":
		return printText(result)
	default:
		return fmt.Errorf("unknown format %q (want text|json)", format)
	}
}

func printText(result *mirror.ScanResult) error {
	if len(result.Images) == 0 {
		fmt.Fprintln(os.Stderr, "No image references found.")
		return nil
	}

	fmt.Fprintf(os.Stderr, "Found %d unique image reference(s) in %d file(s):\n\n", len(result.Images), len(result.Files))

	imgs := make([]*mirror.ImageRef, 0, len(result.Images))
	for _, img := range result.Images {
		imgs = append(imgs, img)
	}
	sort.Slice(imgs, func(i, j int) bool { return imgs[i].Raw < imgs[j].Raw })

	for _, img := range imgs {
		fmt.Printf("%s  (%s)\n", img.Raw, img.Kind)
		sources := append([]string(nil), img.Sources...)
		sort.Strings(sources)
		for _, s := range sources {
			fmt.Printf("    %s\n", s)
		}
	}
	return nil
}

func printJSON(result *mirror.ScanResult) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

func generate(result *mirror.ScanResult, outputDir, prefix string, dryRun bool) error {
	if len(result.Images) == 0 {
		fmt.Fprintln(os.Stderr, "No image references found; nothing to generate.")
		return nil
	}

	if !dryRun {
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return fmt.Errorf("unable to create output directory: %w", err)
		}
	}

	imgs := make([]*mirror.ImageRef, 0, len(result.Images))
	for _, img := range result.Images {
		imgs = append(imgs, img)
	}
	sort.Slice(imgs, func(i, j int) bool { return imgs[i].Raw < imgs[j].Raw })

	for _, img := range imgs {
		comp := mirror.ToComponent(img, prefix)
		body, err := mirror.RenderTOML(comp)
		if err != nil {
			return fmt.Errorf("rendering %s: %w", comp.Name, err)
		}

		if dryRun {
			fmt.Printf("# %s.toml\n%s\n", comp.Name, body)
			continue
		}

		path := fmt.Sprintf("%s/%s.toml", strings.TrimRight(outputDir, "/"), comp.Name)
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		fmt.Fprintf(os.Stderr, "Wrote %s\n", path)
	}
	return nil
}
