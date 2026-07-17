package services

import corev1 "k8s.io/api/core/v1"

// RuntimePlacement is the transparent PodSpec placement pass-through used by
// dedicated runtime backends.
type RuntimePlacement struct {
	NodeSelector     RuntimeNodeSelector `json:"node_selector,omitempty"`
	RuntimeClassName string              `json:"runtime_class_name,omitempty"`
	Tolerations      []corev1.Toleration `json:"tolerations,omitempty"`
}

// RuntimeNodeSelector carries exact label matches and node affinity
// expressions. MatchLabels renders to PodSpec.nodeSelector; MatchExpressions
// renders to requiredDuringSchedulingIgnoredDuringExecution node affinity.
type RuntimeNodeSelector struct {
	MatchLabels      map[string]string                `json:"match_labels,omitempty"`
	MatchExpressions []corev1.NodeSelectorRequirement `json:"match_expressions,omitempty"`
}
