package mirror

import (
	"fmt"
	"regexp"
	"strings"
)

// Component is a Nuon external_image component to be rendered as TOML.
type Component struct {
	Name  string
	Image *ImageRef
}

// ToComponent turns an ImageRef into a Component with a sanitized name.
func ToComponent(img *ImageRef, prefix string) *Component {
	return &Component{
		Name:  componentName(prefix, img),
		Image: img,
	}
}

var nameSanitizeRE = regexp.MustCompile(`[^a-z0-9-]+`)

// componentName builds a Nuon-friendly component name from an image
// reference. e.g. "library/nginx" -> "<prefix>nginx".
func componentName(prefix string, img *ImageRef) string {
	// Use the last segment of the repository as the base name.
	base := img.Repository
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.ToLower(base)
	base = nameSanitizeRE.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "image"
	}
	return prefix + base
}

// RenderTOML renders a component as a Nuon component TOML file body.
func RenderTOML(c *Component) (string, error) {
	if c == nil || c.Image == nil {
		return "", fmt.Errorf("nil component")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "name = %q\n", c.Name)
	fmt.Fprintf(&b, "type = %q\n", "external_image")
	b.WriteString("\n")

	switch c.Image.Kind {
	case ImageKindECR:
		region := c.Image.ECRRegion()
		if region == "" {
			region = "us-east-1"
		}
		b.WriteString("[external_image.aws_ecr]\n")
		fmt.Fprintf(&b, "iam_role_arn = %q\n", "arn:aws:iam::REPLACE_ME:role/REPLACE_ME")
		fmt.Fprintf(&b, "region = %q\n", region)
		fmt.Fprintf(&b, "image_url = %q\n", c.Image.FullURL())
		fmt.Fprintf(&b, "tag = %q\n", tagOrLatest(c.Image))
	case ImageKindGAR:
		region := c.Image.GARRegion()
		if region == "" {
			region = "us-central1"
		}
		project := c.Image.GARProjectID()
		if project == "" {
			project = "REPLACE_ME"
		}
		b.WriteString("[external_image.gcp_gar]\n")
		fmt.Fprintf(&b, "gcp_project_id = %q\n", project)
		fmt.Fprintf(&b, "region = %q\n", region)
		fmt.Fprintf(&b, "image_url = %q\n", c.Image.FullURL())
		fmt.Fprintf(&b, "tag = %q\n", tagOrLatest(c.Image))
	case ImageKindACR:
		b.WriteString("[external_image.azure_acr]\n")
		fmt.Fprintf(&b, "registry_url = %q\n", c.Image.ACRRegistryURL())
		fmt.Fprintf(&b, "tenant_id = %q\n", "REPLACE_ME")
		fmt.Fprintf(&b, "client_id = %q\n", "REPLACE_ME")
		fmt.Fprintf(&b, "image_url = %q\n", c.Image.FullURL())
		fmt.Fprintf(&b, "tag = %q\n", tagOrLatest(c.Image))
	default:
		b.WriteString("[external_image.public]\n")
		fmt.Fprintf(&b, "image_url = %q\n", publicImageURL(c.Image))
		fmt.Fprintf(&b, "tag = %q\n", tagOrLatest(c.Image))
	}

	if len(c.Image.Sources) > 0 {
		b.WriteString("\n# Discovered in:\n")
		for _, s := range c.Image.Sources {
			fmt.Fprintf(&b, "#   - %s\n", s)
		}
	}

	return b.String(), nil
}

func tagOrLatest(img *ImageRef) string {
	if img.Tag != "" {
		return img.Tag
	}
	return "latest"
}

// publicImageURL renders the image_url for a public registry image following
// the convention used in existing Nuon app configs:
//   - Docker Hub references drop the "docker.io" host (and the "library/"
//     namespace for official images): "docker.io/library/nginx" -> "nginx",
//     "docker.io/traefik/whoami" -> "traefik/whoami".
//   - All other public registries keep the full host: "quay.io/foo/bar",
//     "ghcr.io/x/y".
func publicImageURL(img *ImageRef) string {
	if img.Registry == "docker.io" {
		repo := strings.TrimPrefix(img.Repository, "library/")
		return repo
	}
	return img.FullURL()
}
