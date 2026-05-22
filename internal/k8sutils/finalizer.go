package k8sutils

import (
	"context"
	"fmt"

	rvb2 "github.com/OT-CONTAINER-KIT/redis-operator/api/redis/v1beta2"
	rcvb2 "github.com/OT-CONTAINER-KIT/redis-operator/api/rediscluster/v1beta2"
	rrvb2 "github.com/OT-CONTAINER-KIT/redis-operator/api/redisreplication/v1beta2"
	rsvb2 "github.com/OT-CONTAINER-KIT/redis-operator/api/redissentinel/v1beta2"
	"github.com/OT-CONTAINER-KIT/redis-operator/internal/controller/common"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/env"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// HandleRedisFinalizer finalize resource if instance is marked to be deleted
func HandleRedisFinalizer(ctx context.Context, ctrlclient client.Client, cr *rvb2.Redis, finalizer string) error {
	if cr.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(cr, finalizer) {
			if cr.Spec.Storage != nil && !cr.Spec.Storage.KeepAfterDelete {
				if err := finalizeRedisPVC(ctx, ctrlclient, cr); err != nil {
					return err
				}
				if err := deleteLocalPVArtifacts(ctx, ctrlclient, []string{
					buildLocalPVName(cr.Namespace, fmt.Sprintf("%s-%s-0", env.GetString(common.EnvOperatorSTSPVCTemplateName, cr.Name), cr.Name)),
				}, cr.Namespace); err != nil {
					return err
				}
			}
			if err := removeFinalizerWithConflictRetry(ctx, ctrlclient, cr, finalizer); err != nil {
				log.FromContext(ctx).Error(err, "Could not remove finalizer", "finalizer", finalizer)
				return err
			}
		}
	}
	return nil
}

// HandleRedisClusterFinalizer finalize resource if instance is marked to be deleted
func HandleRedisClusterFinalizer(ctx context.Context, ctrlclient client.Client, cr *rcvb2.RedisCluster, finalizer string) error {
	if cr.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(cr, finalizer) {
			if cr.Spec.Storage != nil && !cr.Spec.Storage.KeepAfterDelete {
				if err := finalizeRedisClusterPVC(ctx, ctrlclient, cr); err != nil {
					return err
				}
				if err := deleteLocalPVArtifacts(ctx, ctrlclient, redisClusterLocalPVNames(cr), cr.Namespace); err != nil {
					return err
				}
			}
			if err := removeFinalizerWithConflictRetry(ctx, ctrlclient, cr, finalizer); err != nil {
				log.FromContext(ctx).Error(err, "Could not remove finalizer "+finalizer)
				return err
			}
		}
	}
	return nil
}

func removeFinalizerWithConflictRetry(ctx context.Context, ctrlclient client.Client, obj client.Object, finalizer string) error {
	controllerutil.RemoveFinalizer(obj, finalizer)
	if err := ctrlclient.Update(ctx, obj); err != nil {
		if !errors.IsConflict(err) {
			return err
		}

		latest, ok := obj.DeepCopyObject().(client.Object)
		if !ok {
			return fmt.Errorf("deep copy object %T does not implement client.Object", obj)
		}
		if getErr := ctrlclient.Get(ctx, client.ObjectKeyFromObject(obj), latest); getErr != nil {
			if errors.IsNotFound(getErr) {
				return nil
			}
			return getErr
		}
		if !controllerutil.ContainsFinalizer(latest, finalizer) {
			obj.SetFinalizers(latest.GetFinalizers())
			obj.SetResourceVersion(latest.GetResourceVersion())
			return nil
		}

		controllerutil.RemoveFinalizer(latest, finalizer)
		if updateErr := ctrlclient.Update(ctx, latest); updateErr != nil {
			return updateErr
		}
		obj.SetFinalizers(latest.GetFinalizers())
		obj.SetResourceVersion(latest.GetResourceVersion())
	}
	return nil
}

// Handle RedisReplicationFinalizer finalize resource if instance is marked to be deleted
func HandleRedisReplicationFinalizer(ctx context.Context, ctrlclient client.Client, cr *rrvb2.RedisReplication, finalizer string) error {
	if cr.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(cr, finalizer) {
			if cr.Spec.Storage != nil && !cr.Spec.Storage.KeepAfterDelete {
				if err := finalizeRedisReplicationPVC(ctx, ctrlclient, cr); err != nil {
					return err
				}
				if err := deleteLocalPVArtifacts(ctx, ctrlclient, redisReplicationLocalPVNames(cr), cr.Namespace); err != nil {
					return err
				}
			}
			if err := removeFinalizerWithConflictRetry(ctx, ctrlclient, cr, finalizer); err != nil {
				log.FromContext(ctx).Error(err, "Could not remove finalizer "+finalizer)
				return err
			}
		}
	}
	return nil
}

// HandleRedisSentinelFinalizer finalize resource if instance is marked to be deleted
func HandleRedisSentinelFinalizer(ctx context.Context, ctrlclient client.Client, cr *rsvb2.RedisSentinel, finalizer string) error {
	if cr.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(cr, finalizer) {
			if err := removeFinalizerWithConflictRetry(ctx, ctrlclient, cr, finalizer); err != nil {
				log.FromContext(ctx).Error(err, "Could not remove finalizer "+finalizer)
				return err
			}
		}
	}
	return nil
}

// AddFinalizer add finalizer for graceful deletion
func AddFinalizer(ctx context.Context, cr client.Object, finalizer string, cl client.Client) error {
	return common.AddFinalizer(ctx, cr, finalizer, cl)
}

// finalizeRedisPVC delete PVC
func finalizeRedisPVC(ctx context.Context, client client.Client, cr *rvb2.Redis) error {
	pvcTemplateName := env.GetString(common.EnvOperatorSTSPVCTemplateName, cr.Name)
	PVCName := fmt.Sprintf("%s-%s-0", pvcTemplateName, cr.Name)
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cr.Namespace,
			Name:      PVCName,
		},
	}
	err := client.Delete(ctx, pvc)
	if err != nil && !errors.IsNotFound(err) {
		log.FromContext(ctx).Error(err, "Could not delete Persistent Volume Claim", "PVCName", PVCName)
		return err
	}
	return nil
}

// finalizeRedisClusterPVC delete PVCs
func finalizeRedisClusterPVC(ctx context.Context, client client.Client, cr *rcvb2.RedisCluster) error {
	for _, role := range []string{"leader", "follower"} {
		for i := 0; i < int(cr.Spec.GetReplicaCounts(role)); i++ {
			pvcTemplateName := env.GetString(common.EnvOperatorSTSPVCTemplateName, cr.Name+"-"+role)
			PVCName := fmt.Sprintf("%s-%s-%s-%d", pvcTemplateName, cr.Name, role, i)
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: cr.Namespace,
					Name:      PVCName,
				},
			}
			err := client.Delete(ctx, pvc)
			if err != nil && !errors.IsNotFound(err) {
				log.FromContext(ctx).Error(err, "Could not delete Persistent Volume Claim "+PVCName)
				return err
			}
		}
		if cr.Spec.Storage.NodeConfVolume {
			for i := 0; i < int(cr.Spec.GetReplicaCounts(role)); i++ {
				PVCName := fmt.Sprintf("%s-%s-%s-%d", "node-conf", cr.Name, role, i)
				pvc := &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: cr.Namespace,
						Name:      PVCName,
					},
				}
				err := client.Delete(ctx, pvc)
				if err != nil && !errors.IsNotFound(err) {
					log.FromContext(ctx).Error(err, "Could not delete Persistent Volume Claim "+PVCName)
					return err
				}
			}
		}
	}
	return nil
}

// finalizeRedisReplicationPVC delete PVCs
func finalizeRedisReplicationPVC(ctx context.Context, client client.Client, cr *rrvb2.RedisReplication) error {
	for i := 0; i < int(cr.Spec.GetReplicationCounts("replication")); i++ {
		pvcTemplateName := env.GetString(common.EnvOperatorSTSPVCTemplateName, cr.Name)
		PVCName := fmt.Sprintf("%s-%s-%d", pvcTemplateName, cr.Name, i)
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: cr.Namespace,
				Name:      PVCName,
			},
		}
		err := client.Delete(ctx, pvc)
		if err != nil && !errors.IsNotFound(err) {
			log.FromContext(ctx).Error(err, "Could not delete Persistent Volume Claim "+PVCName)
			return err
		}
	}
	return nil
}

// deleteLocalPVArtifacts deletes the operator-managed local PV plus its paired
// GeneratePath object. Only called when keepAfterDelete == false.
func deleteLocalPVArtifacts(ctx context.Context, cl client.Client, pvNames []string, namespace string) error {
	logger := log.FromContext(ctx)
	for _, pvName := range pvNames {
		pv := &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: pvName}}
		if err := cl.Delete(ctx, pv); err != nil && !errors.IsNotFound(err) {
			logger.Error(err, "Could not delete local PV", "pv", pvName)
			return err
		}
		logger.Info("Deleted local PV", "pv", pvName)
		if err := cl.Delete(ctx, generatePathObject(namespace, pvName)); err != nil && !errors.IsNotFound(err) {
			logger.Error(err, "Could not delete GeneratePath", "generatePath", pvName)
			return err
		}
		logger.Info("Deleted GeneratePath", "generatePath", pvName)
	}
	return nil
}

func generatePathObject(namespace, name string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion(generatePathAPIVersion)
	obj.SetKind(generatePathKind)
	obj.SetNamespace(namespace)
	obj.SetName(name)
	return obj
}

func redisReplicationLocalPVNames(cr *rrvb2.RedisReplication) []string {
	pvcTemplateName := env.GetString(common.EnvOperatorSTSPVCTemplateName, cr.Name)
	pvNames := make([]string, 0, cr.Spec.GetReplicationCounts("replication"))
	for i := 0; i < int(cr.Spec.GetReplicationCounts("replication")); i++ {
		pvcName := fmt.Sprintf("%s-%s-%d", pvcTemplateName, cr.Name, i)
		pvNames = append(pvNames, buildLocalPVName(cr.Namespace, pvcName))
	}
	return pvNames
}

func redisClusterLocalPVNames(cr *rcvb2.RedisCluster) []string {
	var pvNames []string
	for _, role := range []string{"leader", "follower"} {
		statefulSetName := fmt.Sprintf("%s-%s", cr.Name, role)
		replicas := cr.Spec.GetReplicaCounts(role)
		if cr.Spec.PersistenceEnabled != nil && *cr.Spec.PersistenceEnabled {
			pvcTemplateName := env.GetString(common.EnvOperatorSTSPVCTemplateName, statefulSetName)
			for i := 0; i < int(replicas); i++ {
				pvcName := buildLocalPVCName(pvcTemplateName, statefulSetName, i)
				pvNames = append(pvNames, buildLocalPVName(cr.Namespace, pvcName))
			}
		}
		if cr.Spec.Storage.NodeConfVolume {
			for i := 0; i < int(replicas); i++ {
				pvcName := buildLocalPVCName("node-conf", statefulSetName, i)
				pvNames = append(pvNames, buildLocalPVName(cr.Namespace, pvcName))
			}
		}
	}
	return pvNames
}
