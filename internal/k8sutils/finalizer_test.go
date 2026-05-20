package k8sutils

import (
	"context"
	"testing"
	"time"

	common "github.com/OT-CONTAINER-KIT/redis-operator/api/common/v1beta2"
	rvb2 "github.com/OT-CONTAINER-KIT/redis-operator/api/redis/v1beta2"
	rcvb2 "github.com/OT-CONTAINER-KIT/redis-operator/api/rediscluster/v1beta2"
	rrvb2 "github.com/OT-CONTAINER-KIT/redis-operator/api/redisreplication/v1beta2"
	rsvb2 "github.com/OT-CONTAINER-KIT/redis-operator/api/redissentinel/v1beta2"
	mockClient "github.com/OT-CONTAINER-KIT/redis-operator/mocks/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testRedisFinalizer            = "redisFinalizer"
	testRedisClusterFinalizer     = "redisClusterFinalizer"
	testRedisReplicationFinalizer = "redisReplicationFinalizer"
	testRedisSentinelFinalizer    = "redisSentinelFinalizer"
)

func TestHandleRedisClusterFinalizerRemovesFinalizerAndDeletesStorage(t *testing.T) {
	ctx := context.Background()
	cr := newDeletingRedisCluster("redis-cluster", false)
	cr.Spec.Storage.NodeConfVolume = true
	pv := newLocalPV("local-pv", cr.Name, cr.Namespace)
	cl := newFinalizerTestClient(t, cr.DeepCopy(), append(redisClusterPVCs(cr), pv)...)

	err := HandleRedisClusterFinalizer(ctx, cl, cr, testRedisClusterFinalizer)
	require.NoError(t, err)
	assert.NotContains(t, cr.Finalizers, testRedisClusterFinalizer)

	for _, pvcName := range []string{
		"redis-cluster-leader-redis-cluster-leader-0",
		"redis-cluster-follower-redis-cluster-follower-0",
		"node-conf-redis-cluster-leader-0",
		"node-conf-redis-cluster-follower-0",
	} {
		err := cl.Get(ctx, client.ObjectKey{Namespace: cr.Namespace, Name: pvcName}, &corev1.PersistentVolumeClaim{})
		assert.Truef(t, apierrors.IsNotFound(err), "expected PVC %s to be deleted, got %v", pvcName, err)
	}

	err = cl.Get(ctx, client.ObjectKey{Name: pv.GetName()}, &corev1.PersistentVolume{})
	assert.Truef(t, apierrors.IsNotFound(err), "expected local PV to be deleted, got %v", err)
}

func TestHandleRedisClusterFinalizerKeepAfterDeleteRemovesFinalizerOnly(t *testing.T) {
	ctx := context.Background()
	cr := newDeletingRedisCluster("redis-cluster", true)
	pvc := redisClusterPVCs(cr)[0]
	pv := newLocalPV("local-pv", cr.Name, cr.Namespace)
	cl := newFinalizerTestClient(t, cr.DeepCopy(), pvc, pv)

	err := HandleRedisClusterFinalizer(ctx, cl, cr, testRedisClusterFinalizer)
	require.NoError(t, err)
	assert.NotContains(t, cr.Finalizers, testRedisClusterFinalizer)

	require.NoError(t, cl.Get(ctx, client.ObjectKey{Namespace: pvc.GetNamespace(), Name: pvc.GetName()}, &corev1.PersistentVolumeClaim{}))
	require.NoError(t, cl.Get(ctx, client.ObjectKey{Name: pv.GetName()}, &corev1.PersistentVolume{}))
}

func TestHandleRedisClusterFinalizerNilStorageRemovesFinalizer(t *testing.T) {
	ctx := context.Background()
	cr := newDeletingRedisCluster("redis-cluster", true)
	cr.Spec.Storage = nil
	cl := newFinalizerTestClient(t, cr.DeepCopy())

	err := HandleRedisClusterFinalizer(ctx, cl, cr, testRedisClusterFinalizer)
	require.NoError(t, err)
	assert.NotContains(t, cr.Finalizers, testRedisClusterFinalizer)
}

func TestHandleRedisClusterFinalizerMissingPVCAndPVIsIdempotent(t *testing.T) {
	ctx := context.Background()
	cr := newDeletingRedisCluster("redis-cluster", false)
	cl := newFinalizerTestClient(t, cr.DeepCopy())

	err := HandleRedisClusterFinalizer(ctx, cl, cr, testRedisClusterFinalizer)
	require.NoError(t, err)
	assert.NotContains(t, cr.Finalizers, testRedisClusterFinalizer)
}

func TestHandleRedisClusterFinalizerDeleteFailureKeepsFinalizer(t *testing.T) {
	ctx := context.Background()
	cr := newDeletingRedisCluster("redis-cluster", false)
	updateCalled := false
	cl := &mockClient.MockClient{
		DeleteFn: func(context.Context, client.Object, ...client.DeleteOption) error {
			return apierrors.NewForbidden(schema.GroupResource{Group: "", Resource: "persistentvolumeclaims"}, "pvc", assert.AnError)
		},
		UpdateFn: func(context.Context, client.Object, ...client.UpdateOption) error {
			updateCalled = true
			return nil
		},
	}

	err := HandleRedisClusterFinalizer(ctx, cl, cr, testRedisClusterFinalizer)
	require.Error(t, err)
	assert.Contains(t, cr.Finalizers, testRedisClusterFinalizer)
	assert.False(t, updateCalled, "finalizer must not be removed when storage cleanup fails")
}

func TestRemoveFinalizerWithConflictRetryForAllRedisCRs(t *testing.T) {
	now := metav1.NewTime(time.Now())
	tests := []struct {
		name      string
		finalizer string
		obj       client.Object
		latest    client.Object
		resource  schema.GroupResource
	}{
		{
			name:      "Redis",
			finalizer: testRedisFinalizer,
			obj:       newDeletingRedis("redis", &now),
			latest:    newDeletingRedis("redis", &now),
			resource:  schema.GroupResource{Group: "redis.redis.opstreelabs.in", Resource: "redis"},
		},
		{
			name:      "RedisCluster",
			finalizer: testRedisClusterFinalizer,
			obj:       newDeletingRedisCluster("redis-cluster", true),
			latest:    newDeletingRedisCluster("redis-cluster", true),
			resource:  schema.GroupResource{Group: "redis.redis.opstreelabs.in", Resource: "redisclusters"},
		},
		{
			name:      "RedisReplication",
			finalizer: testRedisReplicationFinalizer,
			obj:       newDeletingRedisReplication("redis-replication", &now),
			latest:    newDeletingRedisReplication("redis-replication", &now),
			resource:  schema.GroupResource{Group: "redis.redis.opstreelabs.in", Resource: "redisreplications"},
		},
		{
			name:      "RedisSentinel",
			finalizer: testRedisSentinelFinalizer,
			obj:       newDeletingRedisSentinel("redis-sentinel", &now),
			latest:    newDeletingRedisSentinel("redis-sentinel", &now),
			resource:  schema.GroupResource{Group: "redis.redis.opstreelabs.in", Resource: "redissentinels"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			updateCalls := 0
			cl := &mockClient.MockClient{
				UpdateFn: func(_ context.Context, obj client.Object, _ ...client.UpdateOption) error {
					updateCalls++
					if updateCalls == 1 {
						return apierrors.NewConflict(tt.resource, obj.GetName(), assert.AnError)
					}
					tt.latest.SetFinalizers(obj.GetFinalizers())
					tt.latest.SetResourceVersion("2")
					return nil
				},
				GetFn: func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
					copyClientObject(tt.latest, obj)
					return nil
				},
			}

			err := removeFinalizerWithConflictRetry(ctx, cl, tt.obj, tt.finalizer)
			require.NoError(t, err)
			assert.Equal(t, 2, updateCalls)
			assert.NotContains(t, tt.obj.GetFinalizers(), tt.finalizer)
			assert.Equal(t, "2", tt.obj.GetResourceVersion())
		})
	}
}

func TestRemoveFinalizerWithConflictRetrySucceedsWhenLatestFinalizerAlreadyRemoved(t *testing.T) {
	ctx := context.Background()
	now := metav1.NewTime(time.Now())
	obj := newDeletingRedis("redis", &now)
	latest := newDeletingRedis("redis", &now)
	latest.Finalizers = nil
	latest.ResourceVersion = "2"
	cl := &mockClient.MockClient{
		UpdateFn: func(_ context.Context, obj client.Object, _ ...client.UpdateOption) error {
			return apierrors.NewConflict(schema.GroupResource{Group: "redis.redis.opstreelabs.in", Resource: "redis"}, obj.GetName(), assert.AnError)
		},
		GetFn: func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			copyClientObject(latest, obj)
			return nil
		},
	}

	err := removeFinalizerWithConflictRetry(ctx, cl, obj, testRedisFinalizer)
	require.NoError(t, err)
	assert.NotContains(t, obj.GetFinalizers(), testRedisFinalizer)
	assert.Equal(t, "2", obj.GetResourceVersion())
}

func TestRemoveFinalizerWithConflictRetrySucceedsWhenLatestObjectIsGone(t *testing.T) {
	ctx := context.Background()
	now := metav1.NewTime(time.Now())
	obj := newDeletingRedis("redis", &now)
	cl := &mockClient.MockClient{
		UpdateFn: func(_ context.Context, obj client.Object, _ ...client.UpdateOption) error {
			return apierrors.NewConflict(schema.GroupResource{Group: "redis.redis.opstreelabs.in", Resource: "redis"}, obj.GetName(), assert.AnError)
		},
		GetFn: func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			return apierrors.NewNotFound(schema.GroupResource{Group: "redis.redis.opstreelabs.in", Resource: "redis"}, obj.GetName())
		},
	}

	err := removeFinalizerWithConflictRetry(ctx, cl, obj, testRedisFinalizer)
	require.NoError(t, err)
	assert.NotContains(t, obj.GetFinalizers(), testRedisFinalizer)
}

func newFinalizerTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, rvb2.AddToScheme(scheme))
	require.NoError(t, rcvb2.AddToScheme(scheme))
	require.NoError(t, rrvb2.AddToScheme(scheme))
	require.NoError(t, rsvb2.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func newDeletingRedis(name string, deletionTimestamp *metav1.Time) *rvb2.Redis {
	return &rvb2.Redis{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "default",
			Finalizers:        []string{testRedisFinalizer},
			DeletionTimestamp: deletionTimestamp,
			ResourceVersion:   "1",
		},
	}
}

func newDeletingRedisCluster(name string, keepAfterDelete bool) *rcvb2.RedisCluster {
	now := metav1.NewTime(time.Now())
	return &rcvb2.RedisCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "default",
			Finalizers:        []string{testRedisClusterFinalizer},
			DeletionTimestamp: &now,
			ResourceVersion:   "1",
		},
		Spec: rcvb2.RedisClusterSpec{
			ClusterSize: ptr.To(int32(1)),
			Storage: &rcvb2.ClusterStorage{
				Storage: common.Storage{
					KeepAfterDelete: keepAfterDelete,
					VolumeClaimTemplate: corev1.PersistentVolumeClaim{
						Spec: corev1.PersistentVolumeClaimSpec{
							Resources: corev1.VolumeResourceRequirements{
								Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
							},
						},
					},
				},
			},
		},
	}
}

func newDeletingRedisReplication(name string, deletionTimestamp *metav1.Time) *rrvb2.RedisReplication {
	return &rrvb2.RedisReplication{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "default",
			Finalizers:        []string{testRedisReplicationFinalizer},
			DeletionTimestamp: deletionTimestamp,
			ResourceVersion:   "1",
		},
		Spec: rrvb2.RedisReplicationSpec{
			Size: ptr.To(int32(1)),
			Storage: &common.Storage{
				KeepAfterDelete: true,
			},
		},
	}
}

func newDeletingRedisSentinel(name string, deletionTimestamp *metav1.Time) *rsvb2.RedisSentinel {
	return &rsvb2.RedisSentinel{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "default",
			Finalizers:        []string{testRedisSentinelFinalizer},
			DeletionTimestamp: deletionTimestamp,
			ResourceVersion:   "1",
		},
		Spec: rsvb2.RedisSentinelSpec{
			Size: ptr.To(int32(1)),
		},
	}
}

func copyClientObject(src, dst client.Object) {
	switch typed := dst.(type) {
	case *rvb2.Redis:
		src.(*rvb2.Redis).DeepCopyInto(typed)
	case *rcvb2.RedisCluster:
		src.(*rcvb2.RedisCluster).DeepCopyInto(typed)
	case *rrvb2.RedisReplication:
		src.(*rrvb2.RedisReplication).DeepCopyInto(typed)
	case *rsvb2.RedisSentinel:
		src.(*rsvb2.RedisSentinel).DeepCopyInto(typed)
	}
}

func redisClusterPVCs(cr *rcvb2.RedisCluster) []client.Object {
	return []client.Object{
		newPVC(cr.Namespace, cr.Name+"-leader-"+cr.Name+"-leader-0"),
		newPVC(cr.Namespace, cr.Name+"-follower-"+cr.Name+"-follower-0"),
		newPVC(cr.Namespace, "node-conf-"+cr.Name+"-leader-0"),
		newPVC(cr.Namespace, "node-conf-"+cr.Name+"-follower-0"),
	}
}

func newPVC(namespace, name string) client.Object {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
}

func newLocalPV(name, crName, namespace string) client.Object {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				labelLocalPVInstance: crName,
				labelLocalPVNS:       namespace,
			},
		},
	}
}
