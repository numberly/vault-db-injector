package vault

import (
	"errors"
	"testing"
)

func TestClassifyLoginError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "nil error returns empty string",
			err:      nil,
			expected: "",
		},
		{
			name:     "audience mismatch",
			err:      errors.New("error validating token: audience claim does not match"),
			expected: "audience_mismatch",
		},
		{
			name:     "service account not authorized",
			err:      errors.New("service account name not authorized"),
			expected: "sa_not_bound",
		},
		{
			name:     "bound_service_account substring",
			err:      errors.New("bound_service_account_names does not include my-sa"),
			expected: "sa_not_bound",
		},
		{
			name:     "role not found",
			err:      errors.New("role my-role could not be found"),
			expected: "role_not_found",
		},
		{
			name:     "vault sealed",
			err:      errors.New("error making API request: vault is sealed"),
			expected: "vault_sealed",
		},
		{
			name:     "permission denied",
			err:      errors.New("1 error occurred: permission denied"),
			expected: "permission_denied",
		},
		{
			name:     "unknown error falls through to other",
			err:      errors.New("unexpected internal server error"),
			expected: "other",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyLoginError(tc.err)
			if got != tc.expected {
				t.Errorf("ClassifyLoginError(%q) = %q, want %q", tc.err, got, tc.expected)
			}
		})
	}
}
