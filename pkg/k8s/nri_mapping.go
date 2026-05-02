package k8s

// NRIMapping is the JSON payload attached to a pod under
// ANNOTATION_NRI_MAPPING when the webhook is operating in NRI mode.
//
// The webhook produces it; the NRI plugin DaemonSet consumes it via
// Vault response-wrapping unwrap. WrapToken is single-use and time-bounded;
// Placeholders maps each placeholder string injected into env
// vars to the field name in the wrapped payload (typically "username" or
// "password").
type NRIMapping struct {
	WrapToken    string            `json:"wrap_token"`
	Placeholders map[string]string `json:"placeholders"`
}
