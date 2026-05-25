package k8sutils

import (
	"testing"

	commonapi "github.com/OT-CONTAINER-KIT/redis-operator/api/common/v1beta2"
	rvb2 "github.com/OT-CONTAINER-KIT/redis-operator/api/redis/v1beta2"
	rcvb2 "github.com/OT-CONTAINER-KIT/redis-operator/api/rediscluster/v1beta2"
	rrvb2 "github.com/OT-CONTAINER-KIT/redis-operator/api/redisreplication/v1beta2"
	rsvb2 "github.com/OT-CONTAINER-KIT/redis-operator/api/redissentinel/v1beta2"
	"k8s.io/utils/ptr"
)

func TestBusinessVersionAppliedToStandaloneImages(t *testing.T) {
	cr := &rvb2.Redis{
		Spec: rvb2.RedisSpec{
			BusinessVersion: "v1.2.3",
			KubernetesConfig: commonapi.KubernetesConfig{
				Image: "quay.io/opstree/redis:v7.0.15",
			},
			RedisExporter: &commonapi.RedisExporter{
				Image: "quay.io/opstree/redis-exporter:v1.44.0",
			},
		},
	}

	params := generateRedisStandaloneContainerParams(cr)

	if params.Image != "quay.io/opstree/redis:v1.2.3" {
		t.Fatalf("Image = %q, want %q", params.Image, "quay.io/opstree/redis:v1.2.3")
	}
	if params.RedisExporterImage != "quay.io/opstree/redis-exporter:v1.2.3" {
		t.Fatalf("RedisExporterImage = %q, want %q", params.RedisExporterImage, "quay.io/opstree/redis-exporter:v1.2.3")
	}
}

func TestBusinessVersionLatestAppliedToReplicationImages(t *testing.T) {
	t.Setenv("OPERATOR_IMAGE_TAG", "v0.24.0")

	cr := &rrvb2.RedisReplication{
		Spec: rrvb2.RedisReplicationSpec{
			BusinessVersion: "latest",
			KubernetesConfig: commonapi.KubernetesConfig{
				Image: "quay.io/opstree/redis:v7.0.15",
			},
			RedisExporter: &commonapi.RedisExporter{
				Image: "quay.io/opstree/redis-exporter:v1.44.0",
			},
		},
	}

	params := generateRedisReplicationContainerParams(cr)

	if params.Image != "quay.io/opstree/redis:v0.24.0" {
		t.Fatalf("Image = %q, want %q", params.Image, "quay.io/opstree/redis:v0.24.0")
	}
	if params.RedisExporterImage != "quay.io/opstree/redis-exporter:v0.24.0" {
		t.Fatalf("RedisExporterImage = %q, want %q", params.RedisExporterImage, "quay.io/opstree/redis-exporter:v0.24.0")
	}
}

func TestBusinessVersionAppliedToClusterImages(t *testing.T) {
	cr := &rcvb2.RedisCluster{
		Spec: rcvb2.RedisClusterSpec{
			BusinessVersion: "v1.2.3",
			Port:            ptr.To(6379),
			KubernetesConfig: commonapi.KubernetesConfig{
				Image: "quay.io/opstree/redis:v7.0.15",
			},
			RedisExporter: &commonapi.RedisExporter{
				Image: "quay.io/opstree/redis-exporter:v1.44.0",
			},
		},
	}

	params := generateRedisClusterContainerParams(nil, nil, cr, nil, nil, nil, "leader", nil)

	if params.Image != "quay.io/opstree/redis:v1.2.3" {
		t.Fatalf("Image = %q, want %q", params.Image, "quay.io/opstree/redis:v1.2.3")
	}
	if params.RedisExporterImage != "quay.io/opstree/redis-exporter:v1.2.3" {
		t.Fatalf("RedisExporterImage = %q, want %q", params.RedisExporterImage, "quay.io/opstree/redis-exporter:v1.2.3")
	}
}

func TestBusinessVersionAppliedToSentinelImages(t *testing.T) {
	cr := &rsvb2.RedisSentinel{
		Spec: rsvb2.RedisSentinelSpec{
			BusinessVersion: "v1.2.3",
			KubernetesConfig: commonapi.KubernetesConfig{
				Image: "quay.io/opstree/redis-sentinel:v7.0.15",
			},
			RedisExporter: &commonapi.RedisExporter{
				Image: "quay.io/opstree/redis-exporter:v1.44.0",
			},
		},
	}

	params, err := generateRedisSentinelContainerParams(nil, nil, cr, nil, nil, nil)
	if err != nil {
		t.Fatalf("generateRedisSentinelContainerParams() error = %v", err)
	}

	if params.Image != "quay.io/opstree/redis-sentinel:v1.2.3" {
		t.Fatalf("Image = %q, want %q", params.Image, "quay.io/opstree/redis-sentinel:v1.2.3")
	}
	if params.RedisExporterImage != "quay.io/opstree/redis-exporter:v1.2.3" {
		t.Fatalf("RedisExporterImage = %q, want %q", params.RedisExporterImage, "quay.io/opstree/redis-exporter:v1.2.3")
	}
}

func TestBusinessVersionDoesNotChangeSidecars(t *testing.T) {
	sidecarImage := "quay.io/opstree/custom-sidecar:v9.9.9"
	containers := generateContainerDef(
		"redis",
		containerParameters{
			Image: "quay.io/opstree/redis:v1.2.3",
			Port:  ptr.To(6379),
		},
		false,
		false,
		false,
		nil,
		nil,
		nil,
		[]commonapi.Sidecar{
			{
				Name:  "sidecar",
				Image: sidecarImage,
			},
		},
	)

	if containers[1].Image != sidecarImage {
		t.Fatalf("sidecar image = %q, want %q", containers[1].Image, sidecarImage)
	}
}
