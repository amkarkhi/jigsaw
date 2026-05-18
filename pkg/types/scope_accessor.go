package types

// SetParentScope wires `parent` as the read-fallback for `child`. This is
// called exclusively by pkg/context.Fork immediately after creating a branch
// context; it must not be called from anywhere else.
func SetParentScope(child, parent *ExecutionContext) {
	child.parentScope = parent
}
