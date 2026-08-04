package tui

import "time"

// resetConversation starts a clean conversation without disturbing the
// backend, workspace, or presentation configuration carried by the model.
// /clear is idle-only, but clearing defensive in-flight handles here keeps all
// conversation-scoped cleanup owned by this one operation.
func (m *model) resetConversation() {
	m.conversationGeneration++
	if m.sendCancel != nil {
		m.sendCancel()
		m.sendCancel = nil
	}
	if m.approval != nil {
		// A pending approval should not be reachable through the idle-only
		// /clear command. Deny it defensively so its waiter cannot be stranded.
		if m.approval.reply != nil {
			select {
			case m.approval.reply <- false:
			default:
			}
		}
		m.approval = nil
	}

	m.ag.Clear()
	m.blocks = nil
	m.busy = false
	m.busySince = time.Time{}
	m.currentTool = nil
	m.parallelGroups = nil
	m.parallelCalls = nil
	m.workflow = nil
	m.workflowVisible = false
	m.turn = turnStats{}
	m.activeTree = nil
	m.treeIndex = nil
	m.pendingSteering = nil
	m.queued = nil
	m.models = modelBrowser{}

	// The composer and its pickers are transient activity. Keep the file
	// index, which is rooted in the long-lived working directory, but discard
	// any active token or error from the old conversation.
	fileRoot := m.files.root
	fileIndex := m.files.files
	m.resetInput()
	m.files = filePicker{root: fileRoot, files: fileIndex}
	m.setDotColor(colDotThinking)
	m.refreshViewport()
}
