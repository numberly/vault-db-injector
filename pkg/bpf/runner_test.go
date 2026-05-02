//go:build linux

package bpf

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/numberly/vault-db-injector/pkg/k8s"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

func TestProcessPodAdded_UnwrapAndPut(t *testing.T) {
	mapping := k8s.BPFMapping{
		WrapToken:    "hvs.test",
		Placeholders: map[string]string{"__VDBI_PH_p___": "password"},
	}
	pod := annotatedPod("uid-1", "node-A", "containerd://abc", mapping)

	unwrap := &fakeUnwrapper{values: map[string]string{"password": "supersecret"}}
	mw := &recordingMapWriter{}
	persister := NewPersister(t.TempDir())
	resolver := func(podUID, containerID string) (uint64, error) { return 12345, nil }

	r := &runner{
		nodeName:  "node-A",
		unwrapper: unwrap,
		mapWriter: mw,
		persister: persister,
		resolveCG: resolver,
		processed: make(map[string]struct{}),
	}
	if err := r.processPodAdded(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if mw.puts[12345]["__VDBI_PH_p___"] != "supersecret" {
		t.Fatalf("expected substitution, got %#v", mw.puts)
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
		mapWriter: mw,
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
	r := &runner{
		nodeName:  "node-A",
		unwrapper: unwrap,
		mapWriter: mw,
		persister: NewPersister(t.TempDir()),
		resolveCG: func(string, string) (uint64, error) { return 0, nil },
		processed: make(map[string]struct{}),
	}
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
	r := &runner{
		nodeName:  "node-A",
		unwrapper: unwrap,
		mapWriter: mw,
		persister: NewPersister(t.TempDir()),
		resolveCG: func(string, string) (uint64, error) { return 1, nil },
		processed: make(map[string]struct{}),
	}
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

func TestProcessPodDeleted_RemovesEntry(t *testing.T) {
	mw := &recordingMapWriter{}
	r := &runner{
		nodeName:  "node-A",
		mapWriter: mw,
		persister: NewPersister(t.TempDir()),
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
	if len(mw.deletes) != 1 || mw.deletes[0] != 42 {
		t.Fatalf("expected delete of cgroup 42, got %#v", mw.deletes)
	}
}
