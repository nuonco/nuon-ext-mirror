package mirror

import "testing"

func TestParseImage(t *testing.T) {
	cases := []struct {
		raw      string
		ok       bool
		registry string
		repo     string
		tag      string
		kind     ImageKind
	}{
		{"nginx", false, "", "", "", ""},
		{"nginx:1.25", true, "docker.io", "library/nginx", "1.25", ImageKindPublic},
		{"docker.io/library/postgres:15", true, "docker.io", "library/postgres", "15", ImageKindPublic},
		{"quay.io/foo/bar:v1", true, "quay.io", "foo/bar", "v1", ImageKindPublic},
		{
			"123456789012.dkr.ecr.us-west-2.amazonaws.com/myapp/api:abc",
			true, "123456789012.dkr.ecr.us-west-2.amazonaws.com", "myapp/api", "abc", ImageKindECR,
		},
		{
			"us-central1-docker.pkg.dev/proj/repo/img:tag",
			true, "us-central1-docker.pkg.dev", "proj/repo/img", "tag", ImageKindGAR,
		},
		{
			"myacr.azurecr.io/myrepo/myimage:v3",
			true, "myacr.azurecr.io", "myrepo/myimage", "v3", ImageKindACR,
		},
		{"{{ .Values.image }}", false, "", "", "", ""},
		{"", false, "", "", "", ""},
	}

	for _, tc := range cases {
		got, ok := ParseImage(tc.raw)
		if ok != tc.ok {
			t.Errorf("ParseImage(%q) ok = %v, want %v", tc.raw, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.Registry != tc.registry || got.Repository != tc.repo || got.Tag != tc.tag || got.Kind != tc.kind {
			t.Errorf("ParseImage(%q) = %+v, want registry=%s repo=%s tag=%s kind=%s",
				tc.raw, got, tc.registry, tc.repo, tc.tag, tc.kind)
		}
	}
}

func TestExtractImagesFromManifest(t *testing.T) {
	manifest := []byte(`
apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - name: app
          image: nginx:1.25
        - name: side
          image: "quay.io/foo/bar:v1"
      initContainers:
        - name: init
          image: busybox
`)
	imgs := extractImages(manifest, false)
	want := map[string]bool{
		"nginx:1.25":         false,
		"quay.io/foo/bar:v1": false,
		"busybox":            false,
	}
	for _, v := range imgs {
		if _, ok := want[v]; ok {
			want[v] = true
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("expected to find image %q in extracted set %v", k, imgs)
		}
	}
}

func TestExtractImagesHelmRepoTag(t *testing.T) {
	values := []byte(`
image:
  repository: nginx
  tag: 1.25
`)
	imgs := extractImages(values, false)
	found := false
	for _, v := range imgs {
		if v == "nginx:1.25" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected nginx:1.25 in %v", imgs)
	}
}

func TestExtractImagesSkipsTemplated(t *testing.T) {
	tpl := []byte(`
spec:
  containers:
    - image: {{ .Values.image.repository }}:{{ .Values.image.tag }}
`)
	imgs := extractImages(tpl, false)
	for _, v := range imgs {
		if v != "" && (v == "{{ .Values.image.repository }}:{{ .Values.image.tag }}") {
			t.Errorf("templated image leaked through: %q", v)
		}
	}
}

func TestRenderTOMLPublic(t *testing.T) {
	img, ok := ParseImage("nginx:1.25")
	if !ok {
		t.Fatalf("parse failed")
	}
	c := ToComponent(img, "image-")
	if c.Name != "image-nginx" {
		t.Errorf("name = %q, want image-nginx", c.Name)
	}
	body, err := RenderTOML(c)
	if err != nil {
		t.Fatal(err)
	}
	want := `name = "image-nginx"
type = "external_image"

[external_image.public]
image_url = "nginx"
tag = "1.25"
`
	if body[:len(want)] != want {
		t.Errorf("body = %q, want prefix %q", body, want)
	}
}
