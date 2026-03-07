package alexa

import (
	"errors"
	"testing"
)

func TestErrorTypes(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		category string
		message  string
	}{
		{
			name:     "offline error",
			err:      ErrOffline,
			category: "offline",
			message:  "Device is currently offline",
		},
		{
			name:     "unauthorized error",
			err:      ErrUnauthorized,
			category: "unauthorized",
			message:  "Unable to control device",
		},
		{
			name:     "timeout error",
			err:      ErrTimeout,
			category: "timeout",
			message:  "Request timed out",
		},
		{
			name:     "invalid command",
			err:      ErrInvalidCommand,
			category: "invalid_command",
			message:  "Invalid command",
		},
		{
			name:     "not found",
			err:      ErrNotFound,
			category: "not_found",
			message:  "not found",
		},
		{
			name:     "relay disconnected",
			err:      ErrRelayDisconnected,
			category: "unknown",
			message:  "Alexa relay is not connected",
		},
		{
			name:     "nil error",
			err:      nil,
			category: "unknown",
			message:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cat := ErrorCategory(tt.err)
			if cat != tt.category {
				t.Errorf("ErrorCategory() = %v, want %v", cat, tt.category)
			}

			msg := ErrorMessage(tt.err)
			if tt.message != "" && !contains(msg, tt.message) {
				t.Errorf("ErrorMessage() = %v, want containing %v", msg, tt.message)
			}
		})
	}
}

func TestIsErrorFunctions(t *testing.T) {
	if !IsOfflineError(ErrOffline) {
		t.Error("IsOfflineError(ErrOffline) should be true")
	}
	if IsOfflineError(ErrTimeout) {
		t.Error("IsOfflineError(ErrTimeout) should be false")
	}

	if !IsAuthError(ErrUnauthorized) {
		t.Error("IsAuthError(ErrUnauthorized) should be true")
	}
	if IsAuthError(ErrOffline) {
		t.Error("IsAuthError(ErrOffline) should be false")
	}

	if !IsTimeoutError(ErrTimeout) {
		t.Error("IsTimeoutError(ErrTimeout) should be true")
	}
	if IsTimeoutError(ErrOffline) {
		t.Error("IsTimeoutError(ErrOffline) should be false")
	}

	// Test wrapped errors
	wrapped := errors.New("wrapped: " + ErrOffline.Error())
	if IsOfflineError(wrapped) {
		t.Error("IsOfflineError should only work with errors.Is, not string matching")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && s[0:len(substr)] == substr ||
			containsHelper(s, substr)))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
