# testdata

Sample Helm charts and Kubernetes manifests used by the unit tests in
`internal/mirror` and as a playground for `nuon-ext-mirror` itself.

Layout:

- `charts/sample-app/` — a tiny Helm chart with `Chart.yaml`, `values.yaml`,
  and templates that exercise both concrete image references and templated
  ones (which the scanner must skip).
- `manifests/` — plain Kubernetes manifests that exercise multi-doc YAML and
  references to all four supported registry kinds (Docker Hub / public, AWS
  ECR, GCP GAR, Azure ACR).

You can run the extension against this directory:

```bash
go run . ./testdata
go run . ./testdata --output /tmp/components --dry-run
```

The tests in `internal/mirror/scanner_testdata_test.go` scan this same
directory and assert the expected set of images is discovered.
