package k8sutils

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"path/filepath"
	"sort"
	"time"

	commonapi "github.com/OT-CONTAINER-KIT/redis-operator/api/common/v1beta2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	labelLocalPVInstance = "redis.opstreelabs.in/instance"
	labelLocalPVNS       = "redis.opstreelabs.in/namespace"
	labelLocalPVSTS      = "redis.opstreelabs.in/sts"
	labelLocalPVIndex    = "redis.opstreelabs.in/replica-index"
	labelRedisOperator   = "redis.opstreelabs.in"
)

// ShouldUseLocalPV returns true when the CR requests local PV storage and either
// no default StorageClass exists, or this CR already owns local PVs.
func ShouldUseLocalPV(ctx context.Context, cl kubernetes.Interface, storage *commonapi.Storage, namespace, crName string) (bool, error) {
	if storage == nil || storage.LocalPath == "" {
		return false, nil
	}
	if !storageHasStorageRequest(storage) {
		return false, nil
	}
	if storage.VolumeClaimTemplate.Spec.StorageClassName != nil {
		return false, nil
	}
	existingLocalPVs, err := hasExistingLocalPVs(ctx, cl, namespace, crName)
	if err != nil {
		return false, err
	}
	if existingLocalPVs {
		return true, nil
	}
	scList, err := cl.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("list storage classes: %w", err)
	}
	for _, sc := range scList.Items {
		if isDefaultStorageClass(sc.Annotations) {
			return false, nil
		}
	}
	return true, nil
}

func isDefaultStorageClass(annotations map[string]string) bool {
	return annotations["storageclass.kubernetes.io/is-default-class"] == "true" ||
		annotations["storageclass.beta.kubernetes.io/is-default-class"] == "true"
}

func storageHasStorageRequest(storage *commonapi.Storage) bool {
	if storage == nil {
		return false
	}
	requests := storage.VolumeClaimTemplate.Spec.Resources.Requests
	if requests == nil {
		return false
	}
	storageRequest, ok := requests[corev1.ResourceStorage]
	return ok && !storageRequest.IsZero()
}

// ReconcileLocalPVs ensures local PVs exist and are healthy for all replicas.
// It handles: first-time creation, PV recreation after deletion, and
// PVC recreation when PVC is missing but PV is in Released state.
//
// nodeSelector and tolerations come from the CR spec and are used to filter
// eligible cluster nodes when dynamically assigning PVs.
//
// +kubebuilder:rbac:groups=core,resources=persistentvolumes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch
func ReconcileLocalPVs(
	ctx context.Context,
	cl kubernetes.Interface,
	namespace string,
	crName string,
	storage *commonapi.Storage,
	replicas int32,
	stsName string,
	pvcTplName string,
	nodeSelector map[string]string,
	tolerations []corev1.Toleration,
) error {
	logger := log.FromContext(ctx).WithValues("cr", crName, "sts", stsName)

	use, err := ShouldUseLocalPV(ctx, cl, storage, namespace, crName)
	if err != nil {
		return err
	}
	if !use {
		return nil
	}

	logger.Info("LocalPV mode active", "localPath", storage.LocalPath, "replicas", replicas)

	// Determine which replica indices need new PVs.
	existingNodes, err := getExistingLocalPVNodes(ctx, cl, crName, stsName)
	if err != nil {
		return err
	}

	// Build the full list of (index, pvcName) pairs.
	type replicaInfo struct {
		index   int
		pvcName string
		pvName  string
	}
	allReplicas := make([]replicaInfo, replicas)
	for i := int32(0); i < replicas; i++ {
		pvcName := buildLocalPVCName(pvcTplName, stsName, int(i))
		allReplicas[i] = replicaInfo{
			index:   int(i),
			pvcName: pvcName,
			pvName:  buildLocalPVName(namespace, pvcName),
		}
	}

	// For replicas that are missing a PV, select nodes.
	var needNodes []replicaInfo
	for _, ri := range allReplicas {
		pv, err := cl.CoreV1().PersistentVolumes().Get(ctx, ri.pvName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			needNodes = append(needNodes, ri)
			continue
		}
		if err != nil {
			return fmt.Errorf("get PV %s: %w", ri.pvName, err)
		}
		// PV exists — run state machine, no node selection needed.
		_ = pv
	}

	// Select nodes for replicas that need new PVs.
	nodeAssignments := map[int]string{} // replica index → node name
	if len(needNodes) > 0 {
		selected, err := selectNodesForLocalPVs(ctx, cl, len(needNodes), nodeSelector, tolerations, existingNodes)
		if err != nil {
			return fmt.Errorf("node selection for local PV: %w", err)
		}
		for i, ri := range needNodes {
			nodeAssignments[ri.index] = selected[i]
		}
	}

	// Run the state machine for each replica.
	for _, ri := range allReplicas {
		nodeName := nodeAssignments[ri.index] // empty string if PV already existed
		if err := reconcileSingleLocalPV(ctx, cl, namespace, crName, stsName, ri.index, ri.pvName, ri.pvcName,
			nodeName, storage, nodeSelector, tolerations, existingNodes,
		); err != nil {
			return fmt.Errorf("reconcile local PV for replica %d: %w", ri.index, err)
		}
	}
	return nil
}

// reconcileSingleLocalPV runs the per-replica state machine.
func reconcileSingleLocalPV(
	ctx context.Context,
	cl kubernetes.Interface,
	namespace, crName, stsName string,
	index int,
	pvName, pvcName string,
	preselectedNode string,
	storage *commonapi.Storage,
	nodeSelector map[string]string,
	tolerations []corev1.Toleration,
	existingNodes map[string]struct{},
) error {
	logger := log.FromContext(ctx).WithValues("pv", pvName, "pvc", pvcName, "index", index)

	pv, pvErr := cl.CoreV1().PersistentVolumes().Get(ctx, pvName, metav1.GetOptions{})
	pvMissing := apierrors.IsNotFound(pvErr)
	if pvErr != nil && !pvMissing {
		return fmt.Errorf("get PV: %w", pvErr)
	}

	pvc, pvcErr := cl.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, pvcName, metav1.GetOptions{})
	pvcMissing := apierrors.IsNotFound(pvcErr)
	if pvcErr != nil && !pvcMissing {
		return fmt.Errorf("get PVC: %w", pvcErr)
	}

	switch {
	case pvMissing && pvcMissing:
		// Normal first-time creation: create PV with claimRef pointing to the future PVC.
		logger.Info("Creating local PV (first time)", "node", preselectedNode)
		return createLocalPV(ctx, cl, pvName, namespace, pvcName, "", preselectedNode, index, crName, stsName, storage)

	case pvMissing && !pvcMissing:
		// PV was deleted; PVC is in Lost state. Recreate PV and rebind using PVC UID.
		logger.Info("PV missing but PVC exists (Lost), recreating PV", "pvcUID", pvc.UID)
		// Re-derive the node from spec or pick a new one.
		nodeName, err := pickNodeForRecovery(ctx, cl, preselectedNode, nodeSelector, tolerations, existingNodes)
		if err != nil {
			return err
		}
		return createLocalPV(ctx, cl, pvName, namespace, pvcName, string(pvc.UID), nodeName, index, crName, stsName, storage)

	case !pvMissing && pvcMissing:
		// PVC was deleted; PV should be in Released state. Clear claimRef and recreate PVC.
		logger.Info("PVC missing, PV exists — clearing claimRef and recreating PVC")
		if err := clearPVClaimRef(ctx, cl, pv); err != nil {
			return err
		}
		return createBoundPVC(ctx, cl, namespace, pvcName, pvName, storage)

	default:
		// Both exist — handle inconsistent states.
		if pv.Status.Phase == corev1.VolumeReleased {
			logger.Info("PV is Released (stale claimRef), clearing and recreating PVC")
			if err := clearPVClaimRef(ctx, cl, pv); err != nil {
				return err
			}
			return createBoundPVC(ctx, cl, namespace, pvcName, pvName, storage)
		}
		if pvc.Status.Phase == corev1.ClaimLost {
			logger.Info("PVC is Lost, patching PV claimRef with PVC UID to rebind")
			return patchPVClaimRefUID(ctx, cl, pv, namespace, pvcName, string(pvc.UID))
		}
		// Healthy (Bound/Available) — nothing to do.
		return nil
	}
}

// ── PV helpers ──────────────────────────────────────────────────────────────

func createLocalPV(
	ctx context.Context,
	cl kubernetes.Interface,
	pvName, namespace, pvcName, pvcUID string,
	nodeName string,
	index int,
	crName, stsName string,
	storage *commonapi.Storage,
) error {
	hostPath := filepath.Join(storage.LocalPath, pvcName)
	capacity := storage.VolumeClaimTemplate.Spec.Resources.Requests

	// Kubernetes Local volumes require the directory to already exist on the node.
	// The operator cannot create host directories — they must be pre-created by the
	// node provisioning process or cluster administrator before the Pod is scheduled.
	log.FromContext(ctx).Info(
		"Creating local PV: ensure the directory exists on the target node before the Pod starts",
		"node", nodeName, "hostPath", hostPath,
	)

	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: pvName,
			Labels: map[string]string{
				labelRedisOperator:   "true",
				labelLocalPVInstance: crName,
				labelLocalPVNS:       namespace,
				labelLocalPVSTS:      stsName,
				labelLocalPVIndex:    fmt.Sprintf("%d", index),
			},
		},
		Spec: corev1.PersistentVolumeSpec{
			Capacity:                      capacity,
			AccessModes:                   accessModesOrDefault(storage.VolumeClaimTemplate.Spec.AccessModes),
			VolumeMode:                    volumeModeOrDefault(storage.VolumeClaimTemplate.Spec.VolumeMode),
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
			StorageClassName:              "", // intentionally empty — matches PVCs with explicit ""
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				Local: &corev1.LocalVolumeSource{
					Path: hostPath,
				},
			},
			NodeAffinity: &corev1.VolumeNodeAffinity{
				Required: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "kubernetes.io/hostname",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{nodeName},
								},
							},
						},
					},
				},
			},
			ClaimRef: buildClaimRef(namespace, pvcName, pvcUID),
		},
	}

	_, err := cl.CoreV1().PersistentVolumes().Create(ctx, pv, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create local PV %s: %w", pvName, err)
	}
	return nil
}

func buildClaimRef(namespace, pvcName, pvcUID string) *corev1.ObjectReference {
	ref := &corev1.ObjectReference{
		APIVersion: "v1",
		Kind:       "PersistentVolumeClaim",
		Namespace:  namespace,
		Name:       pvcName,
	}
	if pvcUID != "" {
		ref.UID = types.UID(pvcUID)
	}
	return ref
}

func clearPVClaimRef(ctx context.Context, cl kubernetes.Interface, pv *corev1.PersistentVolume) error {
	patch := []map[string]interface{}{{"op": "remove", "path": "/spec/claimRef"}}
	patchBytes, _ := json.Marshal(patch)
	_, err := cl.CoreV1().PersistentVolumes().Patch(ctx, pv.Name, types.JSONPatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("clear claimRef on PV %s: %w", pv.Name, err)
	}
	return nil
}

func patchPVClaimRefUID(ctx context.Context, cl kubernetes.Interface, pv *corev1.PersistentVolume, namespace, pvcName, pvcUID string) error {
	type claimRefPatch struct {
		Spec struct {
			ClaimRef *corev1.ObjectReference `json:"claimRef"`
		} `json:"spec"`
	}
	p := claimRefPatch{}
	p.Spec.ClaimRef = buildClaimRef(namespace, pvcName, pvcUID)
	patchBytes, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = cl.CoreV1().PersistentVolumes().Patch(ctx, pv.Name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("patch claimRef UID on PV %s: %w", pv.Name, err)
	}
	return nil
}

// createBoundPVC creates a replacement PVC explicitly bound to the given PV.
func createBoundPVC(ctx context.Context, cl kubernetes.Interface, namespace, pvcName, pvName string, storage *commonapi.Storage) error {
	emptyStr := ""
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      accessModesOrDefault(storage.VolumeClaimTemplate.Spec.AccessModes),
			VolumeMode:       volumeModeOrDefault(storage.VolumeClaimTemplate.Spec.VolumeMode),
			Resources:        storage.VolumeClaimTemplate.Spec.Resources,
			StorageClassName: &emptyStr, // must match PV's empty storageClassName
			VolumeName:       pvName,    // explicit binding
		},
	}
	_, err := cl.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create bound PVC %s: %w", pvcName, err)
	}
	return nil
}

// ── Node selection ───────────────────────────────────────────────────────────

// getExistingLocalPVNodes returns the set of node names already used by this
// CR's StatefulSet's local PVs. Used to enforce strong anti-affinity.
func getExistingLocalPVNodes(ctx context.Context, cl kubernetes.Interface, crName, stsName string) (map[string]struct{}, error) {
	pvList, err := cl.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s,%s=%s", labelLocalPVInstance, crName, labelLocalPVSTS, stsName),
	})
	if err != nil {
		return nil, fmt.Errorf("list existing local PVs: %w", err)
	}
	nodes := make(map[string]struct{})
	for _, pv := range pvList.Items {
		if pv.Spec.NodeAffinity != nil &&
			pv.Spec.NodeAffinity.Required != nil &&
			len(pv.Spec.NodeAffinity.Required.NodeSelectorTerms) > 0 {
			for _, expr := range pv.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions {
				if expr.Key == "kubernetes.io/hostname" {
					for _, v := range expr.Values {
						nodes[v] = struct{}{}
					}
				}
			}
		}
	}
	return nodes, nil
}

func hasExistingLocalPVs(ctx context.Context, cl kubernetes.Interface, namespace, crName string) (bool, error) {
	pvList, err := cl.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s,%s=%s", labelLocalPVInstance, crName, labelLocalPVNS, namespace),
	})
	if err != nil {
		return false, fmt.Errorf("list existing local PVs: %w", err)
	}
	return len(pvList.Items) > 0, nil
}

// selectNodesForLocalPVs picks `count` eligible nodes for new local PVs.
// Nodes are filtered by: schedulable, nodeSelector match, toleration coverage.
// Then anti-affinity (already used nodes) is excluded.
// Finally nodes are sorted by pod count ascending and randomly shuffled within
// equal buckets before selecting the top `count`.
func selectNodesForLocalPVs(
	ctx context.Context,
	cl kubernetes.Interface,
	count int,
	nodeSelector map[string]string,
	tolerations []corev1.Toleration,
	excludedNodes map[string]struct{},
) ([]string, error) {
	nodeList, err := cl.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}

	var eligible []string
	for _, node := range nodeList.Items {
		if node.Spec.Unschedulable {
			continue
		}
		if !nodeSelectorMatches(node.Labels, nodeSelector) {
			continue
		}
		if !nodeTolerationsSatisfied(node.Spec.Taints, tolerations) {
			continue
		}
		if _, excluded := excludedNodes[node.Name]; excluded {
			continue
		}
		eligible = append(eligible, node.Name)
	}

	if len(eligible) < count {
		return nil, fmt.Errorf(
			"insufficient schedulable nodes for local PV: need %d eligible nodes, got %d (after taint/selector/anti-affinity filtering)",
			count, len(eligible),
		)
	}

	// Score each node by its current pod count; sort ascending then shuffle within ties.
	type scored struct {
		name     string
		podCount int
	}
	scores := make([]scored, 0, len(eligible))
	for _, name := range eligible {
		pods, err := cl.CoreV1().Pods("").List(ctx, metav1.ListOptions{
			FieldSelector: fields.OneTermEqualSelector("spec.nodeName", name).String(),
		})
		podCount := 0
		if err == nil {
			podCount = len(pods.Items)
		}
		scores = append(scores, scored{name: name, podCount: podCount})
	}

	// Stable sort by pod count, then randomise within equal-count buckets.
	rng := rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec
	sort.SliceStable(scores, func(i, j int) bool {
		return scores[i].podCount < scores[j].podCount
	})
	// Shuffle equal-count buckets.
	start := 0
	for start < len(scores) {
		end := start + 1
		for end < len(scores) && scores[end].podCount == scores[start].podCount {
			end++
		}
		rng.Shuffle(end-start, func(i, j int) {
			scores[start+i], scores[start+j] = scores[start+j], scores[start+i]
		})
		start = end
	}

	result := make([]string, count)
	for i := 0; i < count; i++ {
		result[i] = scores[i].name
	}
	return result, nil
}

// pickNodeForRecovery is used when a PV needs to be recreated (PV was deleted).
// If a preselected node is already known, it is used directly.
// Otherwise a new node is selected dynamically.
func pickNodeForRecovery(
	ctx context.Context,
	cl kubernetes.Interface,
	preselectedNode string,
	nodeSelector map[string]string,
	tolerations []corev1.Toleration,
	existingNodes map[string]struct{},
) (string, error) {
	if preselectedNode != "" {
		return preselectedNode, nil
	}
	nodes, err := selectNodesForLocalPVs(ctx, cl, 1, nodeSelector, tolerations, existingNodes)
	if err != nil {
		return "", err
	}
	return nodes[0], nil
}

// ── Naming helpers ───────────────────────────────────────────────────────────

// buildLocalPVCName returns the PVC name that a StatefulSet will create for a replica.
// Format: <pvcTplName>-<stsName>-<index>
func buildLocalPVCName(pvcTplName, stsName string, index int) string {
	return fmt.Sprintf("%s-%s-%d", pvcTplName, stsName, index)
}

// buildLocalPVName returns the cluster-scoped PV name for a given PVC.
// Format: <namespace>-<pvcName>
func buildLocalPVName(namespace, pvcName string) string {
	return fmt.Sprintf("%s-%s", namespace, pvcName)
}

// ── k8s spec helpers ─────────────────────────────────────────────────────────

func accessModesOrDefault(modes []corev1.PersistentVolumeAccessMode) []corev1.PersistentVolumeAccessMode {
	if len(modes) > 0 {
		return modes
	}
	return []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
}

func volumeModeOrDefault(mode *corev1.PersistentVolumeMode) *corev1.PersistentVolumeMode {
	if mode != nil {
		return mode
	}
	fs := corev1.PersistentVolumeFilesystem
	return &fs
}

// derefTolerations safely dereferences a *[]corev1.Toleration, returning nil if the pointer is nil.
func derefTolerations(t *[]corev1.Toleration) []corev1.Toleration {
	if t == nil {
		return nil
	}
	return *t
}

// nodeSelectorMatches returns true when all key=value pairs in selector exist in nodeLabels.
func nodeSelectorMatches(nodeLabels, selector map[string]string) bool {
	for k, v := range selector {
		if nodeLabels[k] != v {
			return false
		}
	}
	return true
}

// nodeTolerationsSatisfied returns true when the pod's tolerations cover every
// NoSchedule and NoExecute taint on the node.
func nodeTolerationsSatisfied(taints []corev1.Taint, tolerations []corev1.Toleration) bool {
	for _, taint := range taints {
		if taint.Effect != corev1.TaintEffectNoSchedule && taint.Effect != corev1.TaintEffectNoExecute {
			continue
		}
		if !tolerationCoversThisTaint(tolerations, taint) {
			return false
		}
	}
	return true
}

// tolerationCoversThisTaint checks whether any toleration in the list covers the given taint.
func tolerationCoversThisTaint(tolerations []corev1.Toleration, taint corev1.Taint) bool {
	for _, t := range tolerations {
		// Tolerate-all shortcut.
		if t.Operator == corev1.TolerationOpExists && t.Key == "" {
			return true
		}
		if t.Key != taint.Key {
			continue
		}
		// Effect must match or toleration must cover all effects.
		if t.Effect != "" && t.Effect != taint.Effect {
			continue
		}
		if t.Operator == corev1.TolerationOpExists {
			return true
		}
		if t.Operator == corev1.TolerationOpEqual && t.Value == taint.Value {
			return true
		}
	}
	return false
}
