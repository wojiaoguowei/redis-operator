package k8sutils

import (
	"context"
	"testing"

	commonapi "github.com/OT-CONTAINER-KIT/redis-operator/api/common/v1beta2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestBuildGeneratePathResource(t *testing.T) {
	cr := buildGeneratePathResource("pv-0", "default", "/mnt/redis/redis-data-0", "10Gi", "node-a")

	assert.Equal(t, generatePathAPIVersion, cr.GetAPIVersion())
	assert.Equal(t, generatePathKind, cr.GetKind())
	assert.Equal(t, "pv-0", cr.GetName())
	assert.Equal(t, "default", cr.GetNamespace())
	assert.Equal(t, map[string]string{generatePathLabelKey: "pv-0"}, cr.GetLabels())

	fileDirs, found, err := unstructured.NestedSlice(cr.Object, "spec", "fileDirs")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, fileDirs, 2)

	firstDir := fileDirs[0].(map[string]interface{})
	secondDir := fileDirs[1].(map[string]interface{})
	assert.Equal(t, "/mnt/redis/redis-data-0", firstDir["path"])
	assert.Equal(t, "/mnt/redis", secondDir["path"])
	assert.Equal(t, "10Gi", firstDir["dirMaxSize"])
	assert.Equal(t, "/mnt", firstDir["mountPoint"])
	assert.Equal(t, "0750", firstDir["fileMode"])

	affinity := cr.Object["spec"].(map[string]interface{})["affinity"].(map[string]interface{})
	nodeAffinity := affinity["nodeAffinity"].(map[string]interface{})
	required := nodeAffinity["requiredDuringSchedulingIgnoredDuringExecution"].(map[string]interface{})
	terms := required["nodeSelectorTerms"].([]interface{})
	matchExpressions := terms[0].(map[string]interface{})["matchExpressions"].([]interface{})
	values := matchExpressions[0].(map[string]interface{})["values"].([]interface{})
	assert.Equal(t, []interface{}{"node-a"}, values)
}

func TestEnsureGeneratePathCreatesAndUpdatesResource(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	existing := buildGeneratePathResource("pv-0", "default", "/mnt/redis/old", "1Gi", "node-a")
	cl := ctrlfake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()

	err := ensureGeneratePath(ctx, cl, "pv-0", "default", "/mnt/redis/new", "2Gi", "node-b")
	require.NoError(t, err)

	current := &unstructured.Unstructured{}
	current.SetAPIVersion(generatePathAPIVersion)
	current.SetKind(generatePathKind)
	err = cl.Get(ctx, ctrlclient.ObjectKey{Namespace: "default", Name: "pv-0"}, current)
	require.NoError(t, err)

	fileDirs, found, err := unstructured.NestedSlice(current.Object, "spec", "fileDirs")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, fileDirs, 2)
	assert.Equal(t, "/mnt/redis/new", fileDirs[0].(map[string]interface{})["path"])
	assert.Equal(t, "/mnt/redis", fileDirs[1].(map[string]interface{})["path"])

	affinity := current.Object["spec"].(map[string]interface{})["affinity"].(map[string]interface{})
	nodeAffinity := affinity["nodeAffinity"].(map[string]interface{})
	required := nodeAffinity["requiredDuringSchedulingIgnoredDuringExecution"].(map[string]interface{})
	terms := required["nodeSelectorTerms"].([]interface{})
	matchExpressions := terms[0].(map[string]interface{})["matchExpressions"].([]interface{})
	values := matchExpressions[0].(map[string]interface{})["values"].([]interface{})
	assert.Equal(t, []interface{}{"node-b"}, values)
}

func TestCreateLocalPVCreatesGeneratePathBeforePV(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	ctrlCl := ctrlfake.NewClientBuilder().WithScheme(scheme).Build()
	k8sCl := k8sfake.NewSimpleClientset()
	storage := &commonapi.Storage{
		LocalPath: "/mnt/redis",
		VolumeClaimTemplate: corev1.PersistentVolumeClaim{
			Spec: corev1.PersistentVolumeClaimSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
				},
			},
		},
	}

	err := createLocalPV(ctx, k8sCl, ctrlCl, "default-redis-data-0", "default", "redis-data-0", "", "node-a", 0, "redis", "redis", storage)
	require.NoError(t, err)

	gp := &unstructured.Unstructured{}
	gp.SetAPIVersion(generatePathAPIVersion)
	gp.SetKind(generatePathKind)
	err = ctrlCl.Get(ctx, ctrlclient.ObjectKey{Namespace: "default", Name: "default-redis-data-0"}, gp)
	require.NoError(t, err)

	pv, err := k8sCl.CoreV1().PersistentVolumes().Get(ctx, "default-redis-data-0", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "/mnt/redis/redis-data-0", pv.Spec.Local.Path)
	assert.Equal(t, []string{"node-a"}, pv.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions[0].Values)
}

func TestReconcileLocalPVUsesPreferredNodeAssignments(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	ctrlCl := ctrlfake.NewClientBuilder().WithScheme(scheme).Build()
	k8sCl := k8sfake.NewSimpleClientset(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
	})
	storage := &commonapi.Storage{
		LocalPath: "/mnt/redis",
		VolumeClaimTemplate: corev1.PersistentVolumeClaim{
			Spec: corev1.PersistentVolumeClaimSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
				},
			},
		},
	}

	err := ReconcileLocalPVs(ctx, k8sCl, ctrlCl, "default", "redis", storage, 1, "redis-leader", "redis-data", nil, nil, map[int]string{0: "node-a"})
	require.NoError(t, err)

	pv, err := k8sCl.CoreV1().PersistentVolumes().Get(ctx, "default-redis-data-redis-leader-0", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"node-a"}, pv.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions[0].Values)
}
