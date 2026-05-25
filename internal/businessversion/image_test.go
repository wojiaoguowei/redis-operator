package businessversion

import "testing"

func TestResolveImage(t *testing.T) {
	t.Setenv("OPERATOR_IMAGE_TAG", "v0.24.0")

	tests := []struct {
		name            string
		image           string
		businessVersion string
		want            string
	}{
		{
			name:            "empty business version leaves image unchanged",
			image:           "quay.io/opstree/redis:v7.0.15",
			businessVersion: "",
			want:            "quay.io/opstree/redis:v7.0.15",
		},
		{
			name:            "explicit business version replaces tag",
			image:           "quay.io/opstree/redis:v7.0.15",
			businessVersion: "v1.2.3",
			want:            "quay.io/opstree/redis:v1.2.3",
		},
		{
			name:            "latest resolves to operator image tag",
			image:           "quay.io/opstree/redis-exporter:v1.44.0",
			businessVersion: "latest",
			want:            "quay.io/opstree/redis-exporter:v0.24.0",
		},
		{
			name:            "untagged image gets tag appended",
			image:           "quay.io/opstree/redis",
			businessVersion: "v1.2.3",
			want:            "quay.io/opstree/redis:v1.2.3",
		},
		{
			name:            "digest image is converted to tag",
			image:           "quay.io/opstree/redis@sha256:abc123",
			businessVersion: "v1.2.3",
			want:            "quay.io/opstree/redis:v1.2.3",
		},
		{
			name:            "registry port is preserved",
			image:           "registry.example.com:5000/opstree/redis:v7.0.15",
			businessVersion: "v1.2.3",
			want:            "registry.example.com:5000/opstree/redis:v1.2.3",
		},
		{
			name:            "empty image stays empty",
			image:           "",
			businessVersion: "v1.2.3",
			want:            "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveImage(tt.image, tt.businessVersion); got != tt.want {
				t.Fatalf("ResolveImage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveImageLatestWithoutOperatorImageTag(t *testing.T) {
	t.Setenv("OPERATOR_IMAGE_TAG", "")

	image := "quay.io/opstree/redis:v7.0.15"
	if got := ResolveImage(image, "latest"); got != image {
		t.Fatalf("ResolveImage() = %q, want %q", got, image)
	}
}
