package k8s

import (
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// ClassifyTokenRequestError returns a metric-friendly reason label
// for a TokenRequest error. Uses apimachinery's typed predicates
// rather than substring matching on error messages.
func ClassifyTokenRequestError(err error) string {
	switch {
	case err == nil:
		return ""
	case apierrors.IsForbidden(err):
		return "rbac_denied"
	case apierrors.IsNotFound(err):
		return "sa_not_found"
	case apierrors.IsUnauthorized(err):
		return "unauthorized"
	default:
		return "other"
	}
}
