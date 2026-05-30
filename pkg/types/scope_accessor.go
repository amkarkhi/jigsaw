package types

// SetParentScope wires `parent` as the read-fallback for `child`. This is
// called exclusively by pkg/context.Fork immediately after creating a branch
// context; it must not be called from anywhere else.
func SetParentScope(child, parent *ExecutionContext) {
	child.parentScope = parent
}

// SetCurrentTaskExec is the cross-package setter for the unexported
// currentTaskExec field. The task executor calls this immediately before
// dispatching logic for a task and again with the previous value (typically
// nil) once the task returns, so Annotate/AnnotateLink can find the right
// TaskExecution to write into.
func SetCurrentTaskExec(execCtx *ExecutionContext, te *TaskExecution) {
	execCtx.currentTaskExec = te
}

// CurrentTaskExec exposes the active TaskExecution pointer for callers that
// need to inspect it (e.g. wrapper helpers); returns nil when no task is
// active.
func CurrentTaskExec(execCtx *ExecutionContext) *TaskExecution {
	return execCtx.currentTaskExec
}
