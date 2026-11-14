package cmd

import "fmt"

// ExitError ends the process with Code and no error line: the command has
// already written its full result, and the code alone carries the outcome.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// errAttention is report's exit when the report lists something that needs
// attention.
var errAttention = &ExitError{Code: 2}
