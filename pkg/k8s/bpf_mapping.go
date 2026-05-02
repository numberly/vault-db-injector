package k8s

// BPFMapping is the JSON payload attached to a pod under
// ANNOTATION_BPF_MAPPING when the webhook is operating in BPF mode.
//
// The webhook produces it; the BPF runner DaemonSet consumes it via
// Vault response-wrapping unwrap. WrapToken is single-use and time-bounded;
// Placeholders maps each fixed-length placeholder string injected into env
// vars to the field name in the wrapped payload (typically "username" or
// "password").
type BPFMapping struct {
	WrapToken    string            `json:"wrap_token"`
	Placeholders map[string]string `json:"placeholders"`
}
