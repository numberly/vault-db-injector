package k8s

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestClassifyTokenRequestError(t *testing.T) {
	gr := schema.GroupResource{Group: "", Resource: "serviceaccounts"}

	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{"nil error", nil, ""},
		{"forbidden", apierrors.NewForbidden(gr, "myapp", nil), "rbac_denied"},
		{"not found", apierrors.NewNotFound(gr, "myapp"), "sa_not_found"},
		{"unauthorized", apierrors.NewUnauthorized("token invalid"), "unauthorized"},
		{"generic error", apierrors.NewInternalError(errors.New("internal")), "other"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, ClassifyTokenRequestError(tc.err))
		})
	}
}
