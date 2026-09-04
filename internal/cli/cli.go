// Package cli holds types shared by pni's commands.
package cli

import "strconv"

// ExitError carries a child process's exit code up to main so pni can exit
// with the same status the package manager did.
type ExitError struct {
	Code int
}

// Error implements error.
func (e *ExitError) Error() string {
	return "exit status " + strconv.Itoa(e.Code)
}

// Exit returns an ExitError for code, or nil when code is 0.
func Exit(code int) error {
	if code == 0 {
		return nil
	}
	return &ExitError{Code: code}
}
