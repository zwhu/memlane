package agents

import "errors"

type piProjectRPCCall struct {
	name                   string
	started, ended, result bool
}
type piProjectRPCBoundary struct{ calls map[string]*piProjectRPCCall }

func piProjectToolAllowed(name string) bool {
	switch name {
	case "memlane_project_tree", "memlane_project_source", "memlane_project_knowledge", "memlane_project_finish":
		return true
	}
	return false
}
func (b *piProjectRPCBoundary) assistant(e piRPCEvent) error {
	if b.calls == nil {
		b.calls = map[string]*piProjectRPCCall{}
	}
	found := false
	for _, part := range e.Message.Content {
		switch part.Type {
		case "text", "thinking":
		case "toolCall":
			if !piProjectToolAllowed(part.Name) || part.ID == "" || len(part.ID) > 256 || b.calls[part.ID] != nil || len(b.calls) >= 4096 {
				return errors.New("pi project tool identity or allowlist invalid")
			}
			b.calls[part.ID] = &piProjectRPCCall{name: part.Name}
			found = true
		default:
			return errors.New("pi project assistant content invalid")
		}
	}
	if !found {
		return errors.New("pi project tool turn has no declared call")
	}
	return nil
}
func (b *piProjectRPCBoundary) event(e piRPCEvent) error {
	call := b.calls[e.ToolCallID]
	if call == nil || call.name != e.ToolName {
		return errors.New("pi project undeclared tool execution")
	}
	switch e.Type {
	case "tool_execution_start":
		if call.started {
			return errors.New("pi project duplicate tool start")
		}
		call.started = true
	case "tool_execution_end":
		if !call.started || call.ended {
			return errors.New("pi project tool end without unique start")
		}
		call.ended = true
	default:
		return errors.New("pi project unexpected tool event")
	}
	return nil
}
func (b *piProjectRPCBoundary) result(e piRPCEvent) error {
	call := b.calls[e.Message.ToolCallID]
	if call == nil || call.name != e.Message.ToolName || !call.started || call.result {
		return errors.New("pi project undeclared or duplicate tool result")
	}
	call.result = true
	return nil
}
func (b *piProjectRPCBoundary) complete() error {
	if len(b.calls) == 0 {
		return errors.New("pi project has no actual tool transcript")
	}
	for _, c := range b.calls {
		if !c.started || !c.ended || !c.result {
			return errors.New("pi project tool transcript incomplete")
		}
	}
	return nil
}
