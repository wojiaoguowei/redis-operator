package redisreplication

import (
	"testing"

	commonapi "github.com/OT-CONTAINER-KIT/redis-operator/api/common/v1beta2"
	rrvb2 "github.com/OT-CONTAINER-KIT/redis-operator/api/redisreplication/v1beta2"
)

func TestBusinessVersionAppliedToEmbeddedSentinelImage(t *testing.T) {
	rr := &rrvb2.RedisReplication{
		Spec: rrvb2.RedisReplicationSpec{
			BusinessVersion: "v1.2.3",
			Sentinel: &rrvb2.Sentinel{
				KubernetesConfig: commonapi.KubernetesConfig{
					Image: "quay.io/opstree/redis-sentinel:v7.0.15",
				},
			},
		},
	}

	container := buildSentinelContainer(rr)

	if container.Image != "quay.io/opstree/redis-sentinel:v1.2.3" {
		t.Fatalf("Image = %q, want %q", container.Image, "quay.io/opstree/redis-sentinel:v1.2.3")
	}
}
