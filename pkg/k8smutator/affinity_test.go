package k8smutator

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

const labelKey = "vault-db-injector.numberly.io/nri-ready"

func hasNRIReadyExpr(exprs []corev1.NodeSelectorRequirement) bool {
	for _, e := range exprs {
		if e.Key == labelKey && e.Operator == corev1.NodeSelectorOpIn {
			for _, v := range e.Values {
				if v == "true" {
					return true
				}
			}
		}
	}
	return false
}

func TestRequireNRIReadyNode_EmptyAffinity(t *testing.T) {
	pod := &corev1.Pod{}
	requireNRIReadyNode(pod)
	terms := pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) != 1 {
		t.Fatalf("want 1 term, got %d", len(terms))
	}
	if !hasNRIReadyExpr(terms[0].MatchExpressions) {
		t.Fatalf("missing nri-ready requirement")
	}
}

func TestRequireNRIReadyNode_PreservesNodeSelector(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{"role": "worker"},
		},
	}
	requireNRIReadyNode(pod)
	if pod.Spec.NodeSelector["role"] != "worker" {
		t.Fatalf("nodeSelector clobbered: %v", pod.Spec.NodeSelector)
	}
	terms := pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if !hasNRIReadyExpr(terms[0].MatchExpressions) {
		t.Fatalf("missing nri-ready requirement")
	}
}

func TestRequireNRIReadyNode_MergesWithExistingTerm(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Affinity: &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{MatchExpressions: []corev1.NodeSelectorRequirement{
								{Key: "topology", Operator: corev1.NodeSelectorOpIn, Values: []string{"eu-west-1a"}},
							}},
						},
					},
				},
			},
		},
	}
	requireNRIReadyNode(pod)
	terms := pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) != 1 {
		t.Fatalf("term count must remain 1, got %d", len(terms))
	}
	if len(terms[0].MatchExpressions) != 2 {
		t.Fatalf("expected 2 MatchExpressions (existing + nri), got %d", len(terms[0].MatchExpressions))
	}
	if !hasNRIReadyExpr(terms[0].MatchExpressions) {
		t.Fatalf("missing nri-ready requirement")
	}
	// User's expression must still be there
	found := false
	for _, e := range terms[0].MatchExpressions {
		if e.Key == "topology" {
			found = true
		}
	}
	if !found {
		t.Fatalf("user topology expression dropped")
	}
}

func TestRequireNRIReadyNode_MultipleTermsAllGetExpression(t *testing.T) {
	// Three OR'd terms simulating "schedule on east OR west OR north".
	// We must AND nri-ready to each so the OR semantic preserves.
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Affinity: &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "region", Operator: corev1.NodeSelectorOpIn, Values: []string{"east"}}}},
							{MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "region", Operator: corev1.NodeSelectorOpIn, Values: []string{"west"}}}},
							{MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "region", Operator: corev1.NodeSelectorOpIn, Values: []string{"north"}}}},
						},
					},
				},
			},
		},
	}
	requireNRIReadyNode(pod)
	terms := pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) != 3 {
		t.Fatalf("term count must remain 3, got %d", len(terms))
	}
	for i, term := range terms {
		if !hasNRIReadyExpr(term.MatchExpressions) {
			t.Fatalf("term %d missing nri-ready requirement", i)
		}
	}
}

func TestRequireNRIReadyNode_PreservesPreferred(t *testing.T) {
	// preferredDuringScheduling is soft — we must NOT touch it.
	pref := []corev1.PreferredSchedulingTerm{
		{Weight: 5, Preference: corev1.NodeSelectorTerm{
			MatchExpressions: []corev1.NodeSelectorRequirement{
				{Key: "fast-disk", Operator: corev1.NodeSelectorOpExists},
			},
		}},
	}
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Affinity: &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					PreferredDuringSchedulingIgnoredDuringExecution: pref,
				},
			},
		},
	}
	requireNRIReadyNode(pod)
	if len(pod.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution) != 1 {
		t.Fatalf("preferred terms clobbered")
	}
	if pod.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution[0].Preference.MatchExpressions[0].Key != "fast-disk" {
		t.Fatalf("preferred content clobbered")
	}
}

func TestRequireNRIReadyNode_PreservesTolerations(t *testing.T) {
	tols := []corev1.Toleration{{Key: "dedicated", Operator: corev1.TolerationOpEqual, Value: "db"}}
	pod := &corev1.Pod{Spec: corev1.PodSpec{Tolerations: tols}}
	requireNRIReadyNode(pod)
	if len(pod.Spec.Tolerations) != 1 || pod.Spec.Tolerations[0].Key != "dedicated" {
		t.Fatalf("tolerations clobbered: %v", pod.Spec.Tolerations)
	}
}

func TestRequireNRIReadyNode_EmptyTerms(t *testing.T) {
	// Edge case: NodeSelector with empty Terms slice.
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Affinity: &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{},
					},
				},
			},
		},
	}
	requireNRIReadyNode(pod)
	terms := pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) != 1 || !hasNRIReadyExpr(terms[0].MatchExpressions) {
		t.Fatalf("expected single term with nri-ready, got %v", terms)
	}
}
