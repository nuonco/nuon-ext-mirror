# nuon-ext-mirror

A [nuon CLI](https://github.com/nuonco/nuon) extension that scans Helm charts and Kubernetes manifests for container
image references and generates Nuon `external_image` components for mirroring those images through Nuon.

## How it works

`nuon mirror` walks a directory looking for Helm charts (`Chart.yaml`, `values.yaml`, `templates/*.yaml`) and plain
Kubernetes manifests (`*.yaml`, `*.yml`). For every concrete container image reference it finds (e.g. `nginx:1.25`,
`quay.io/foo/bar:v1`, `123456.dkr.ecr.us-east-1.amazonaws.com/svc:tag`), it produces a Nuon `external_image` component
TOML file that you can drop into your Nuon app config so the image is mirrored into your customers' clouds.

Templated values like `{{ .Values.image.repository }}` are skipped — only fully resolved image strings are emitted.

## Usage

```
USAGE:
  nuon mirror                          : scan the current directory and list images
  nuon mirror <PATH>                   : scan <PATH> and list images
  nuon mirror -o, --output <DIR>       : write external_image components into <DIR>
  nuon mirror -p, --prefix <PREFIX>    : prefix component names (default: "image-")
  nuon mirror -f, --format <FORMAT>    : output format for listing: text|json (default: text)
      --include-templated              : include image refs that look templated (best-effort)
      --rewrite-sources                : rewrite scanned files to reference the generated components via Nuon templating
      --dry-run                        : print components and proposed source-file rewrites instead of writing
  nuon mirror -h, --help               : show help
  nuon mirror -V, --version            : show version
```

## Install

```bash
nuon ext install mirror
```

## Getting started

Point the extension at a directory containing Helm charts or plain Kubernetes manifests:

```bash
nuon mirror ./charts
```

You'll see a list of every image reference discovered, grouped by source file. To turn those into Nuon component files:

```bash
nuon mirror ./charts --output ./components
```

This writes one `*.toml` file per unique image into `./components`, each one declaring an `external_image` component.
For example, `nginx:1.25` becomes:

```toml
# components/image-nginx.toml
name = "image-nginx"
type = "external_image"

[external_image.public]
image_url = "nginx"
tag = "1.25"
```

Docker Hub references drop the `docker.io` host (and the `library/` namespace for official images), matching the
convention used in existing Nuon app configs. Other public registries keep their full host (e.g. `quay.io/foo/bar`,
`ghcr.io/x/y`).

ECR (`*.dkr.ecr.*.amazonaws.com/...`), GAR (`*-docker.pkg.dev/...`), and ACR (`*.azurecr.io/...`) registries are
detected automatically and emit the appropriate `aws_ecr` / `gcp_gar` / `azure_acr` blocks with placeholder values
you must fill in.

## Rewriting sources

Once the components are generated, the original Helm `values.yaml` / Kubernetes manifest files still hard-code the
image references. Pass `--rewrite-sources` to update those files in place so they pull the image coordinates from the
generated components via Nuon templating:

```bash
# Preview the rewrites without touching the filesystem.
nuon mirror . --rewrite-sources --dry-run

# Apply them.
nuon mirror . -o ./components --rewrite-sources
```

`--rewrite-sources` requires either `-o <DIR>` (so the components the rewritten files reference actually exist) or
`--dry-run` (preview only). Running it on its own is rejected, since rewriting without generating would leave you with
dangling `{{.nuon.components.<name>...}}` references.

For a `values.yaml` like:

```yaml
image:
  repository: traefik/whoami
  tag: latest
```

`--rewrite-sources` produces:

```yaml
image:
  repository: '{{.nuon.components.image-whoami.outputs.image.repository}}'
  tag: '{{.nuon.components.image-whoami.outputs.image.tag}}'
```

It also handles single-line `image: foo:bar` form. Lines that already contain `{{ }}` and digest references
(`@sha256:...`) are left untouched, and files under a `templates/` directory are skipped (those typically already use
Helm templating).

For Nuon to resolve those `{{.nuon...}}` references at deploy time, the rewritten file must be wired into a Helm
component as a values file with Nuon templating enabled. See the
[Nuon Helm component docs](https://docs.nuon.co/) for details.

## Building

```bash
make build
```

## Testing

```bash
make test
```
