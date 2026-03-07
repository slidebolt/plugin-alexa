package alexa

import "errors"

// Error types for standardized error reporting
var (
	// ErrOffline indicates the device is not responding or unreachable
	ErrOffline = errors.New("device is offline")

	// ErrUnauthorized indicates authentication or authorization failure
	ErrUnauthorized = errors.New("unauthorized access")

	// ErrTimeout indicates a communication timeout
	ErrTimeout = errors.New("communication timeout")

	// ErrInvalidCommand indicates the command is invalid or not supported
	ErrInvalidCommand = errors.New("invalid command")

	// ErrNotFound indicates the requested resource was not found
	ErrNotFound = errors.New("resource not found")

	// ErrRelayDisconnected indicates the relay is not connected
	ErrRelayDisconnected = errors.New("relay not connected")
)

// Sync status constants for standardized sync_status field values
const (
	// SyncStatusSynced indicates entity state is synchronized with the device
	SyncStatusSynced = "synced"

	// SyncStatusPending indicates a command is in progress and awaiting device response
	SyncStatusPending = "pending"

	// SyncStatusFailed indicates the last operation failed
	SyncStatusFailed = "failed"

	// SyncStatusEmpty indicates unknown or initial state (empty string)
	SyncStatusEmpty = ""
)

// IsOfflineError checks if an error indicates the device is offline
func IsOfflineError(err error) bool {
	return errors.Is(err, ErrOffline)
}

// IsAuthError checks if an error is an authentication/authorization error
func IsAuthError(err error) bool {
	return errors.Is(err, ErrUnauthorized)
}

// IsTimeoutError checks if an error is a timeout error
func IsTimeoutError(err error) bool {
	return errors.Is(err, ErrTimeout)
}

// ErrorCategory maps errors to user-friendly categories
func ErrorCategory(err error) string {
	switch {
	case IsOfflineError(err):
		return "offline"
	case IsAuthError(err):
		return "unauthorized"
	case IsTimeoutError(err):
		return "timeout"
	case errors.Is(err, ErrInvalidCommand):
		return "invalid_command"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	default:
		return "unknown"
	}
}

// ErrorMessage returns a user-friendly error message
func ErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case IsOfflineError(err):
		return "Device is currently offline. Please check the device connection."
	case IsAuthError(err):
		return "Unable to control device: authentication failed."
	case IsTimeoutError(err):
		return "Request timed out. The device may be unresponsive."
	case errors.Is(err, ErrInvalidCommand):
		return "Invalid command. The requested action is not supported."
	case errors.Is(err, ErrNotFound):
		return "Device or resource not found."
	case errors.Is(err, ErrRelayDisconnected):
		return "Alexa relay is not connected. Please check your configuration."
	default:
		return "An unexpected error occurred."
	}
}
