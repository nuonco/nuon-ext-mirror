// Package mirror provides scanning and component-generation logic for the
// nuon-ext-mirror extension. It walks a directory tree looking for Helm chart
// values, Helm templates, and plain Kubernetes manifests, extracts container
// image references, and turns them into Nuon external_image component TOML.
package mirror

import (
	"fmt"
	"strings"
)

// ImageKind classifies an image reference by its source registry, which
// determines which external_image config block we generate.
type ImageKind string

const (
	ImageKindPublic ImageKind = "public"
	ImageKindECR    ImageKind = "aws_ecr"
	ImageKindGAR    ImageKind = "gcp_gar"
	ImageKindACR    ImageKind = "azure_acr"
)

// ImageRef is a fully-resolved container image reference parsed out of a
// scanned file.
type ImageRef struct {
	// Raw is the original "registry/repo:tag" string as it appeared in the
	// source file.
	Raw string `json:"raw"`

	// Registry is the registry host. For Docker Hub references without an
	// explicit host this is "docker.io".
	Registry string `json:"registry"`

	// Repository is the repo path (e.g. "library/nginx").
	Repository string `json:"repository"`

	// Tag is the image tag. Defaults to "latest" if not present.
	Tag string `json:"tag"`

	// Digest, if any (e.g. "sha256:abc...").
	Digest string `json:"digest,omitempty"`

	// Kind is the registry classification used to pick the external_image
	// sub-config (public/aws_ecr/gcp_gar).
	Kind ImageKind `json:"kind"`

	// Sources is the list of files this image was discovered in.
	Sources []string `json:"sources"`
}

// ScanResult is what a Scanner produces.
type ScanResult struct {
	// Files is the list of files that were scanned and contained at least
	// one image reference.
	Files []string `json:"files"`

	// Images is keyed by the canonical "<registry>/<repo>:<tag>" string.
	Images map[string]*ImageRef `json:"images"`
}

// ParseImage parses a container image string into an ImageRef. It returns
// (nil, false) when the input is empty or looks templated (contains "{{"
// or "}}").
func ParseImage(raw string) (*ImageRef, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	if strings.Contains(raw, "{{") || strings.Contains(raw, "}}") {
		return nil, false
	}
	// Reject obvious non-image strings.
	if strings.ContainsAny(raw, " \t\n") {
		return nil, false
	}
	// Must contain at least one of: '/', ':', '@'. Otherwise it's something
	// like a label value, not an image.
	if !strings.ContainsAny(raw, "/:@") {
		return nil, false
	}

	rest := raw
	digest := ""
	if i := strings.Index(rest, "@"); i >= 0 {
		digest = rest[i+1:]
		rest = rest[:i]
	}

	// Split registry from path. The registry is the first '/' segment iff it
	// looks like a hostname (contains '.' or ':' or equals "localhost").
	registry := "docker.io"
	path := rest
	if i := strings.Index(rest, "/"); i >= 0 {
		head := rest[:i]
		if strings.ContainsAny(head, ".:") || head == "localhost" {
			registry = head
			path = rest[i+1:]
		}
	}

	// Split path into repo + tag.
	tag := ""
	if i := strings.LastIndex(path, ":"); i >= 0 {
		tag = path[i+1:]
		path = path[:i]
	}
	if path == "" {
		return nil, false
	}
	if tag == "" && digest == "" {
		tag = "latest"
	}

	// Docker Hub single-segment images are implicitly under "library/".
	if registry == "docker.io" && !strings.Contains(path, "/") {
		path = "library/" + path
	}

	return &ImageRef{
		Raw:        raw,
		Registry:   registry,
		Repository: path,
		Tag:        tag,
		Digest:     digest,
		Kind:       classify(registry),
	}, true
}

func classify(registry string) ImageKind {
	switch {
	case strings.Contains(registry, ".dkr.ecr.") && strings.HasSuffix(registry, ".amazonaws.com"):
		return ImageKindECR
	case strings.HasSuffix(registry, "-docker.pkg.dev"):
		return ImageKindGAR
	case strings.HasSuffix(registry, ".azurecr.io"):
		return ImageKindACR
	default:
		return ImageKindPublic
	}
}

// Key returns the canonical map key for this image (registry + repo + tag).
func (i *ImageRef) Key() string {
	if i.Digest != "" {
		return fmt.Sprintf("%s/%s@%s", i.Registry, i.Repository, i.Digest)
	}
	return fmt.Sprintf("%s/%s:%s", i.Registry, i.Repository, i.Tag)
}

// FullURL returns the registry+repo portion (no tag), suitable for the
// external_image image_url field.
func (i *ImageRef) FullURL() string {
	return fmt.Sprintf("%s/%s", i.Registry, i.Repository)
}

// ECRRegion attempts to pull the AWS region out of an ECR registry hostname
// like "<acct>.dkr.ecr.us-east-1.amazonaws.com".
func (i *ImageRef) ECRRegion() string {
	if i.Kind != ImageKindECR {
		return ""
	}
	parts := strings.Split(i.Registry, ".")
	// expected: <acct>.dkr.ecr.<region>.amazonaws.com
	if len(parts) >= 5 && parts[1] == "dkr" && parts[2] == "ecr" {
		return parts[3]
	}
	return ""
}

// GARRegion attempts to pull the region out of a GAR registry hostname like
// "us-central1-docker.pkg.dev".
func (i *ImageRef) GARRegion() string {
	if i.Kind != ImageKindGAR {
		return ""
	}
	host := strings.TrimSuffix(i.Registry, "-docker.pkg.dev")
	if host == i.Registry {
		return ""
	}
	return host
}

// GARProjectID attempts to pull the GCP project ID out of the repository path
// (e.g. "<project>/<repo>/<image>").
func (i *ImageRef) GARProjectID() string {
	if i.Kind != ImageKindGAR {
		return ""
	}
	parts := strings.SplitN(i.Repository, "/", 2)
	if len(parts) >= 1 {
		return parts[0]
	}
	return ""
}

// ACRRegistryURL returns the Azure Container Registry URL (the host portion).
func (i *ImageRef) ACRRegistryURL() string {
	if i.Kind != ImageKindACR {
		return ""
	}
	return i.Registry
}
