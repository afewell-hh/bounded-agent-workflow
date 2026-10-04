package execution

// Read-only forwarders used by `baw run review`. They expose the unchanged
// execute validators; execute behavior does not depend on this file.

// ValidIntentReceipt strictly validates serialized execution intent bytes
// against the given run ID with the unchanged execute validator.
func ValidIntentReceipt(data []byte, id string) bool { return validIntent(data, id) }

// ValidResultReceipt strictly validates serialized execution result bytes
// against the given run ID with the unchanged execute validator.
func ValidResultReceipt(data []byte, id string) bool { return validResult(data, id) }

// PlanCommand validates one decoded plan command value (a strict decoder's
// map with json.Number values) under the unchanged execute command rules.
func PlanCommand(v any) (Command, bool) { return command(v) }

// CheckExecutable applies the unchanged execute executable checks and returns
// the resolved path. A failure carries CodeExecUnavailable.
func CheckExecutable(path string) (string, error) { return checkExecutable(path) }
