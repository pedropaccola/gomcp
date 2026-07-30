package tools

import "fmt"

// errEmptyBatch reports that field's batch array was empty — every
// batch-capable tool refuses a no-op call rather than silently succeeding.
func errEmptyBatch(field string) error {
	return fmt.Errorf("%s must not be empty", field)
}
