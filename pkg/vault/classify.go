package vault

import "strings"

// ClassifyLoginError returns a metric-friendly reason label for a
// Vault login failure, classified from the error message. Vault's
// API errors are not strongly typed, so substring matching is the
// best we can do — but it's bounded to a small, stable set of
// recognizable strings the Vault server emits for kubernetes auth.
func ClassifyLoginError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "audience"):
		return "audience_mismatch"
	case strings.Contains(msg, "service account name not authorized") ||
		strings.Contains(msg, "bound_service_account"):
		return "sa_not_bound"
	case strings.Contains(msg, "role") && strings.Contains(msg, "could not be found"):
		return "role_not_found"
	case strings.Contains(msg, "sealed"):
		return "vault_sealed"
	case strings.Contains(msg, "permission denied"):
		return "permission_denied"
	default:
		return "other"
	}
}
