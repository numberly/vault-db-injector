//go:build linux

package bpf

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	listersv1 "k8s.io/client-go/listers/core/v1"
)

type fakeUnwrapper struct {
	values map[string]string
	err    error
}

func (f *fakeUnwrapper) UnwrapValues(_ context.Context, _ string) (map[string]string, error) {
	return f.values, f.err
}

type recordingMapWriter struct {
	puts    map[uint64]map[string]string
	deletes []uint64
}

func (r *recordingMapWriter) PutMapping(cg uint64, m map[string]string) error {
	if r.puts == nil {
		r.puts = make(map[uint64]map[string]string)
	}
	r.puts[cg] = m
	return nil
}
func (r *recordingMapWriter) DeleteMapping(cg uint64) error {
	r.deletes = append(r.deletes, cg)
	return nil
}

// annotatedPod builds a single-container pod with the BPF annotation.
func annotatedPod(podUID, nodeName, containerID string, mapping k8s.BPFMapping) *corev1.Pod {
	annot, _ := json.Marshal(mapping)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p", Namespace: "default", UID: types.UID(podUID),
			Annotations: map[string]string{
				k8s.ANNOTATION_BPF_MAPPING: string(annot),
			},
		},
		Spec: corev1.PodSpec{
			NodeName:   nodeName,
			Containers: []corev1.Container{{Name: "c"}},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:        "c",
				ContainerID: containerID,
			}},
		},
	}
}

// multiContainerPod builds a pod with two regular containers.
func multiContainerPod(podUID, nodeName string, containerIDs []string, mapping k8s.BPFMapping) *corev1.Pod {
	annot, _ := json.Marshal(mapping)
	var containers []corev1.Container
	var statuses []corev1.ContainerStatus
	for i, cid := range containerIDs {
		name := "c" + string(rune('0'+i))
		containers = append(containers, corev1.Container{Name: name})
		statuses = append(statuses, corev1.ContainerStatus{Name: name, ContainerID: cid})
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p", Namespace: "default", UID: types.UID(podUID),
			Annotations: map[string]string{
				k8s.ANNOTATION_BPF_MAPPING: string(annot),
			},
		},
		Spec: corev1.PodSpec{
			NodeName:   nodeName,
			Containers: containers,
		},
		Status: corev1.PodStatus{
			ContainerStatuses: statuses,
		},
	}
}

func makeRunner(nodeName string, unwrap unwrapper, mw *recordingMapWriter, dir string, resolver cgroupResolver) *runner {
	return &runner{
		nodeName:  nodeName,
		log:       logrus.New(),
		unwrapper: unwrap,
		mapWriter: mw,
		persister: NewPersister(dir),
		resolveCG: resolver,
		processed: make(map[string]struct{}),
	}
}

func TestProcessPodAdded_UnwrapAndPut(t *testing.T) {
	mapping := k8s.BPFMapping{
		WrapToken:    "hvs.test",
		Placeholders: map[string]string{"__VDBI_PH_p___": "password"},
	}
	pod := annotatedPod("uid-1", "node-A", "containerd://abc", mapping)

	unwrap := &fakeUnwrapper{values: map[string]string{"password": "supersecret"}}
	mw := &recordingMapWriter{}
	r := makeRunner("node-A", unwrap, mw, t.TempDir(), func(_, _ string) (uint64, error) { return 12345, nil })

	if err := r.processPodAdded(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if mw.puts[12345]["__VDBI_PH_p___"] != "supersecret" {
		t.Fatalf("expected substitution, got %#v", mw.puts)
	}
}

func TestProcessPodAdded_MultiContainer(t *testing.T) {
	mapping := k8s.BPFMapping{
		WrapToken:    "hvs.test",
		Placeholders: map[string]string{"__VDBI_PH_p___": "password"},
	}
	pod := multiContainerPod("uid-mc", "node-A", []string{"containerd://aaa", "containerd://bbb"}, mapping)

	unwrap := &fakeUnwrapper{values: map[string]string{"password": "secret"}}
	mw := &recordingMapWriter{}
	// Resolve different cgroup IDs per container
	cgroupByID := map[string]uint64{"containerd://aaa": 100, "containerd://bbb": 200}
	r := makeRunner("node-A", unwrap, mw, t.TempDir(), func(_, cid string) (uint64, error) {
		return cgroupByID[cid], nil
	})

	if err := r.processPodAdded(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	// Both cgroup IDs must be programmed with the same mappings.
	if mw.puts[100]["__VDBI_PH_p___"] != "secret" {
		t.Fatalf("container 0 not programmed: %#v", mw.puts)
	}
	if mw.puts[200]["__VDBI_PH_p___"] != "secret" {
		t.Fatalf("container 1 not programmed: %#v", mw.puts)
	}
}

func TestProcessPodAdded_MultiContainer_PersistsBothCgroupIDs(t *testing.T) {
	mapping := k8s.BPFMapping{
		WrapToken:    "hvs.test",
		Placeholders: map[string]string{"__VDBI_PH_p___": "password"},
	}
	pod := multiContainerPod("uid-mc2", "node-A", []string{"containerd://aaa", "containerd://bbb"}, mapping)

	unwrap := &fakeUnwrapper{values: map[string]string{"password": "secret"}}
	mw := &recordingMapWriter{}
	dir := t.TempDir()
	persister := NewPersister(dir)
	cgroupByID := map[string]uint64{"containerd://aaa": 111, "containerd://bbb": 222}
	r := &runner{
		nodeName:  "node-A",
		log:       logrus.New(),
		unwrapper: unwrap,
		mapWriter: mw,
		persister: persister,
		resolveCG: func(_, cid string) (uint64, error) { return cgroupByID[cid], nil },
		processed: make(map[string]struct{}),
	}

	if err := r.processPodAdded(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	pm, err := persister.Load("uid-mc2")
	if err != nil {
		t.Fatal(err)
	}
	if len(pm.CgroupIDs) != 2 {
		t.Fatalf("expected 2 cgroup IDs persisted, got %v", pm.CgroupIDs)
	}
	// Both IDs must be present (order may vary).
	found := map[uint64]bool{}
	for _, id := range pm.CgroupIDs {
		found[id] = true
	}
	if !found[111] || !found[222] {
		t.Fatalf("persisted cgroup IDs mismatch: %v", pm.CgroupIDs)
	}
}

func TestProcessPodAdded_SkipWithoutAnnotation(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default", UID: "uid-1"},
		Spec:       corev1.PodSpec{NodeName: "node-A"},
	}
	mw := &recordingMapWriter{}
	r := &runner{
		nodeName:  "node-A",
		log:       logrus.New(),
		mapWriter: mw,
		persister: NewPersister(t.TempDir()),
		processed: make(map[string]struct{}),
	}
	if err := r.processPodAdded(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if len(mw.puts) != 0 {
		t.Fatalf("expected no map writes, got %#v", mw.puts)
	}
}

func TestProcessPodAdded_WrongNode_Ignored(t *testing.T) {
	mapping := k8s.BPFMapping{WrapToken: "x", Placeholders: map[string]string{}}
	pod := annotatedPod("uid-1", "node-B", "containerd://abc", mapping)
	unwrap := &fakeUnwrapper{values: map[string]string{}}
	mw := &recordingMapWriter{}
	r := makeRunner("node-A", unwrap, mw, t.TempDir(), func(string, string) (uint64, error) { return 0, nil })
	if err := r.processPodAdded(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if len(mw.puts) != 0 {
		t.Fatalf("wrong-node pod should be skipped: %#v", mw.puts)
	}
}

func TestProcessPodAdded_Idempotent(t *testing.T) {
	mapping := k8s.BPFMapping{
		WrapToken:    "hvs.test",
		Placeholders: map[string]string{"__VDBI_PH_p___": "password"},
	}
	pod := annotatedPod("uid-1", "node-A", "containerd://abc", mapping)

	callCount := 0
	unwrap := &unwrapCounter{counter: &callCount, values: map[string]string{"password": "s"}}
	mw := &recordingMapWriter{}
	r := makeRunner("node-A", unwrap, mw, t.TempDir(), func(string, string) (uint64, error) { return 1, nil })

	_ = r.processPodAdded(context.Background(), pod)
	_ = r.processPodAdded(context.Background(), pod)
	if callCount != 1 {
		t.Fatalf("expected 1 unwrap call, got %d", callCount)
	}
}

type unwrapCounter struct {
	counter *int
	values  map[string]string
}

func (u *unwrapCounter) UnwrapValues(_ context.Context, _ string) (map[string]string, error) {
	*u.counter++
	return u.values, nil
}

func TestProcessPodDeleted_RemovesAllEntries(t *testing.T) {
	dir := t.TempDir()
	persister := NewPersister(dir)
	// Pre-populate tmpfs with two cgroup IDs (simulating a multi-container pod).
	_ = persister.Save("uid-1", PersistedMapping{
		Mappings:  map[string]string{"__VDBI_PH_p___": "secret"},
		CgroupIDs: []uint64{42, 99},
	})

	mw := &recordingMapWriter{}
	r := &runner{
		nodeName:  "node-A",
		log:       logrus.New(),
		mapWriter: mw,
		persister: persister,
		resolveCG: func(string, string) (uint64, error) { return 42, nil },
		processed: make(map[string]struct{}),
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{UID: "uid-1"},
		Spec:       corev1.PodSpec{NodeName: "node-A"},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{ContainerID: "containerd://abc"}},
		},
	}
	r.processPodDeleted(pod)
	// Both cgroup IDs should have been deleted.
	if len(mw.deletes) != 2 {
		t.Fatalf("expected 2 deletes, got %v", mw.deletes)
	}
	deletedSet := map[uint64]bool{}
	for _, id := range mw.deletes {
		deletedSet[id] = true
	}
	if !deletedSet[42] || !deletedSet[99] {
		t.Fatalf("expected deletes for cgroup IDs 42 and 99, got %v", mw.deletes)
	}
}

func TestCollectContainerIDs_AllTypes(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			InitContainerStatuses: []corev1.ContainerStatus{
				{ContainerID: "containerd://init1"},
				{ContainerID: ""}, // empty — should be skipped
			},
			ContainerStatuses: []corev1.ContainerStatus{
				{ContainerID: "containerd://c1"},
				{ContainerID: "containerd://c2"},
			},
			EphemeralContainerStatuses: []corev1.ContainerStatus{
				{ContainerID: "containerd://eph1"},
			},
		},
	}
	ids := collectContainerIDs(pod)
	if len(ids) != 4 {
		t.Fatalf("expected 4 container IDs, got %v", ids)
	}
}

// makePodLister builds a PodLister backed by an in-memory indexer.
func makePodLister(pods ...*corev1.Pod) listersv1.PodLister {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	for _, p := range pods {
		_ = indexer.Add(p)
	}
	return listersv1.NewPodLister(indexer)
}

func TestRestoreBPFMaps_ReprogramsRunningPod(t *testing.T) {
	dir := t.TempDir()
	persister := NewPersister(dir)
	_ = persister.Save("uid-restore", PersistedMapping{
		Mappings:  map[string]string{"__VDBI_PH_p___": "secret"},
		CgroupIDs: []uint64{55, 66},
	})

	mw := &recordingMapWriter{}
	r := &runner{
		nodeName:  "node-A",
		log:       logrus.New(),
		mapWriter: mw,
		persister: persister,
		processed: map[string]struct{}{"uid-restore": {}},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default", UID: "uid-restore"},
		Spec:       corev1.PodSpec{NodeName: "node-A"},
	}
	lister := makePodLister(pod)

	if err := r.restoreBPFMaps(lister); err != nil {
		t.Fatal(err)
	}
	// Both cgroup IDs must have been re-programmed.
	if mw.puts[55]["__VDBI_PH_p___"] != "secret" {
		t.Fatalf("cgroup 55 not programmed: %#v", mw.puts)
	}
	if mw.puts[66]["__VDBI_PH_p___"] != "secret" {
		t.Fatalf("cgroup 66 not programmed: %#v", mw.puts)
	}
}

func TestRestoreBPFMaps_DeletesStaleTmpfsEntry(t *testing.T) {
	dir := t.TempDir()
	persister := NewPersister(dir)
	_ = persister.Save("uid-gone", PersistedMapping{
		Mappings:  map[string]string{"__VDBI_PH_p___": "secret"},
		CgroupIDs: []uint64{77},
	})

	mw := &recordingMapWriter{}
	r := &runner{
		nodeName:  "node-A",
		log:       logrus.New(),
		mapWriter: mw,
		persister: persister,
		processed: map[string]struct{}{"uid-gone": {}},
	}

	// Empty lister — the pod is no longer on this node.
	lister := makePodLister()

	if err := r.restoreBPFMaps(lister); err != nil {
		t.Fatal(err)
	}
	// BPF map must NOT have been programmed.
	if len(mw.puts) != 0 {
		t.Fatalf("expected no BPF puts for gone pod, got %#v", mw.puts)
	}
	// Tmpfs entry must have been deleted.
	if _, err := persister.Load("uid-gone"); err == nil {
		t.Fatal("expected tmpfs entry to be deleted for gone pod")
	}
	// processed map must no longer contain the UID.
	r.mu.Lock()
	_, stillPresent := r.processed["uid-gone"]
	r.mu.Unlock()
	if stillPresent {
		t.Fatal("expected uid-gone removed from processed map")
	}
}
