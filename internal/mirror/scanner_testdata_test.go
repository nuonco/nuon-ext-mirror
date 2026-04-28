package mirror

import (
	"path/filepath"
	"testing"
)

// TestScanTestdata runs the scanner against the repo's testdata directory and
// verifies that every image we expect to be discovered is in fact found, and
// classified correctly. The expected list intentionally only covers concrete
// image references — fully-templated values like
// `{{ .Values.image.repository }}:{{ .Values.image.tag }}` must be skipped.
func TestScanTestdata(t *testing.T) {
	root, err := filepath.Abs("../../testdata")
	if err != nil {
		t.Fatal(err)
	}

	scanner := NewScanner(ScannerOptions{})
	result, err := scanner.Scan(root)
	if err != nil {
		t.Fatalf("Scan(%s): %v", root, err)
	}

	type want struct {
		raw  string
		kind ImageKind
	}

	wants := []want{
		// charts/sample-app/values.yaml
		{"nginx:1.25", ImageKindPublic},
		{"quay.io/jetstack/cert-manager-controller:v1.14.0", ImageKindPublic},
		{"ghcr.io/example/worker:v2.3.4", ImageKindPublic},

		// charts/sample-app/templates/deployment.yaml
		{"prom/prometheus:v2.48.0", ImageKindPublic},
		{"otel/opentelemetry-collector-contrib:0.96.0", ImageKindPublic},

		// charts/sample-app/templates/cronjob.yaml
		{"postgres:15.5-alpine", ImageKindPublic},

		// manifests/redis.yaml
		{"redis:7.2.4", ImageKindPublic},
		{"oliver006/redis_exporter:v1.55.0", ImageKindPublic},

		// manifests/multi-registry.yaml
		{"123456789012.dkr.ecr.us-west-2.amazonaws.com/myorg/api:abc123", ImageKindECR},
		{"us-central1-docker.pkg.dev/my-gcp-project/my-repo/my-image:v2", ImageKindGAR},
		{"mycompany.azurecr.io/myteam/myapp:1.2.3", ImageKindACR},
	}

	rawSet := map[string]*ImageRef{}
	for _, img := range result.Images {
		rawSet[img.Raw] = img
	}

	for _, w := range wants {
		got, ok := rawSet[w.raw]
		if !ok {
			t.Errorf("expected to find image %q in scan result; got %d images: %v",
				w.raw, len(rawSet), keys(rawSet))
			continue
		}
		if got.Kind != w.kind {
			t.Errorf("image %q classified as %q, want %q", w.raw, got.Kind, w.kind)
		}
		if len(got.Sources) == 0 {
			t.Errorf("image %q has no sources", w.raw)
		}
	}

	// Templated refs must NOT appear.
	for raw := range rawSet {
		if containsAny(raw, "{{", "}}") {
			t.Errorf("templated image leaked into result: %q", raw)
		}
	}
}

func keys(m map[string]*ImageRef) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
