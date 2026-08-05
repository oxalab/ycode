package ycode

import (
	"context"
	"fmt"
	"os"
	"strings"
	"ycode/packages/coding-agent/src/core/extensions"
	"ycode/packages/llm/src"
)

// ============================================================================
// System Prompt Building
// ============================================================================

func (s *AgentSession) normalizePromptSnippet(text string) string {
	if text == "" {
		return ""
	}
	oneLine := strings.ReplaceAll(text, "\r\n", " ")
	oneLine = strings.ReplaceAll(oneLine, "\n", " ")
	oneLine = strings.ReplaceAll(oneLine, "\r", " ")
	// Collapse whitespace
	for strings.Contains(oneLine, "  ") {
		oneLine = strings.ReplaceAll(oneLine, "  ", " ")
	}
	oneLine = strings.TrimSpace(oneLine)
	return oneLine
}

func (s *AgentSession) normalizePromptGuidelines(guidelines []string) []string {
	if len(guidelines) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var result []string
	for _, g := range guidelines {
		trimmed := strings.TrimSpace(g)
		if trimmed != "" && !seen[trimmed] {
			seen[trimmed] = true
			result = append(result, trimmed)
		}
	}
	return result
}

func (s *AgentSession) rebuildSystemPrompt(toolNames []string) string {
	// Filter to only registered tools
	var validToolNames []string
	for _, name := range toolNames {
		if _, ok := s.toolRegistry[name]; ok {
			validToolNames = append(validToolNames, name)
		}
	}

	toolSnippets := make(map[string]string)
	var promptGuidelines []string
	for _, name := range validToolNames {
		if snippet, ok := s.toolPromptSnippets[name]; ok && snippet != "" {
			toolSnippets[name] = snippet
		}
		if guidelines, ok := s.toolPromptGuidelines[name]; ok {
			promptGuidelines = append(promptGuidelines, guidelines...)
		}
	}

	loaderSystemPrompt := s.resourceLoader.GetSystemPrompt()
	loaderAppend := s.resourceLoader.GetAppendSystemPrompt()
	var appendSystemPrompt string
	if len(loaderAppend) > 0 {
		appendSystemPrompt = strings.Join(loaderAppend, "\n\n")
	}

	loadedSkills := s.resourceLoader.GetSkills().Skills
	loadedContextFiles := s.resourceLoader.GetAgentsFiles().AgentsFiles

	s.baseSystemPromptOptions = BuildSystemPromptOptions{
		Cwd:               s.cwd,
		Skills:            loadedSkills,
		ContextFiles:      loadedContextFiles,
		CustomPrompt:      loaderSystemPrompt,
		AppendSystemPrompt: appendSystemPrompt,
		SelectedTools:     validToolNames,
		ToolSnippets:      toolSnippets,
		PromptGuidelines:  promptGuidelines,
	}
	return BuildSystemPrompt(s.baseSystemPromptOptions)
}

// ============================================================================
// Prompt
// ============================================================================

func (s *AgentSession) Prompt(text string, opts *PromptOptions) error {
	if opts == nil {
		opts = &PromptOptions{}
	}
	expandTemplates := opts.ExpandPromptTemplates
	if !expandTemplates {
		expandTemplates = true
	}

	// Handle extension commands first (even during streaming)
	if expandTemplates && strings.HasPrefix(text, "/") {
		handled := s.tryExecuteExtensionCommand(text)
		if handled {
			if opts.PreflightResult != nil {
				opts.PreflightResult(true)
			}
			return nil
		}
	}

	// Emit input event for extension interception
	currentText := text
	var currentImages []any
	if len(opts.Images) > 0 {
		currentImages = opts.Images
	}
	if s.extensionRunner != nil && s.extensionRunner.HasHandlers("input") {
		inputResult := s.extensionRunner.EmitInput(context.Background(), currentText, currentImages, opts.Source)

		if inputResult != nil {
			if inputResult.Action == "handled" {
				if opts.PreflightResult != nil {
					opts.PreflightResult(true)
				}
				return nil
			}
			if inputResult.Action == "transform" {
				currentText = inputResult.Text
				if inputResult.Images != nil {
					currentImages = inputResult.Images
				}
			}
		}
	}

	// Expand skill commands and prompt templates
	expandedText := currentText
	if expandTemplates {
		expandedText = s.expandSkillCommand(expandedText)
		expandedText = ExpandPromptTemplate(expandedText, s.GetPromptTemplates())
	}

	// If streaming, queue via steer or followUp
	if s.IsStreaming() {
		if opts.StreamingBehavior == "" {
			return fmt.Errorf("Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message")
		}
		if opts.StreamingBehavior == "followUp" {
			s.queueFollowUp(expandedText, currentImages)
		} else {
			s.queueSteer(expandedText, currentImages)
		}
		if opts.PreflightResult != nil {
			opts.PreflightResult(true)
		}
		return nil
	}

	// Flush pending bash messages
	s.flushPendingBashMessages()

	// Validate model
	model := s.GetModel()
	if model.ID == "" {
		return fmt.Errorf("No model selected. Use /model to select one or configure a default model in settings")
	}

	if !s.modelRegistry.HasConfiguredAuth(&model) {
		return fmt.Errorf("No API key found for provider \"%s\". Run '/login %s' to authenticate", model.Provider, model.Provider)
	}

	// Check compaction for last assistant message (Phase 11.5)
	if s.lastAssistantMessage != nil {
		if am, ok := s.lastAssistantMessage.(llm.AssistantMessage); ok {
			s.checkCompaction(am, false)
		}
		s.lastAssistantMessage = nil
	}

	// Build messages array
	var messages []any

	// Add user message
	userContent := []map[string]any{
		{"type": "text", "text": expandedText},
	}
	if len(currentImages) > 0 {
		for _, img := range currentImages {
			userContent = append(userContent, img.(map[string]any))
		}
	}

	messages = append(messages, map[string]any{
		"role":     "user",
		"content":  userContent,
		"timestamp": currentTimeMillis(),
	})

	// Inject pending nextTurn messages
	for _, msg := range s.pendingNextTurnMessages {
		messages = append(messages, msg)
	}
	s.pendingNextTurnMessages = nil

	// Emit before_agent_start extension event
	if s.extensionRunner != nil {
		result, _ := s.extensionRunner.EmitBeforeAgentStart(
			context.Background(),
			expandedText,
			currentImages,
			s.baseSystemPrompt,
			s.baseSystemPromptOptions,
		)
		if result != nil {
			if result.Messages != nil {
				for _, msg := range result.Messages {
					messages = append(messages, msg)
				}
			}
			if result.SystemPrompt != nil {
				s.Agent.SetSystemPrompt(*result.SystemPrompt)
			} else {
				s.Agent.SetSystemPrompt(s.baseSystemPrompt)
			}
		}
	}

	if opts.PreflightResult != nil {
		opts.PreflightResult(true)
	}

	// Send prompt to agent
	err := s.Agent.Prompt(context.Background(), expandedText)
	return err
}

// ============================================================================
// Steer / FollowUp
// ============================================================================

func (s *AgentSession) Steer(text string, images []any) error {
	if strings.HasPrefix(text, "/") {
		s.throwIfExtensionCommand(text)
	}
	expandedText := s.expandSkillCommand(text)
	expandedText = ExpandPromptTemplate(expandedText, s.GetPromptTemplates())
	s.queueSteer(expandedText, images)
	return nil
}

func (s *AgentSession) FollowUp(text string, images []any) error {
	if strings.HasPrefix(text, "/") {
		s.throwIfExtensionCommand(text)
	}
	expandedText := s.expandSkillCommand(text)
	expandedText = ExpandPromptTemplate(expandedText, s.GetPromptTemplates())
	s.queueFollowUp(expandedText, images)
	return nil
}

func (s *AgentSession) queueSteer(text string, images []any) {
	s.steeringMessages = append(s.steeringMessages, text)
	s.emitQueueUpdate()
	s.Agent.Steer(llm.UserMessage{Role: "user", Content: []llm.ContentBlock{llm.TextContent{Text: text}}})
}

func (s *AgentSession) queueFollowUp(text string, images []any) {
	s.followUpMessages = append(s.followUpMessages, text)
	s.emitQueueUpdate()
	s.Agent.FollowUp(llm.UserMessage{Role: "user", Content: []llm.ContentBlock{llm.TextContent{Text: text}}})
}

func (s *AgentSession) throwIfExtensionCommand(text string) {
	spaceIndex := strings.Index(text, " ")
	commandName := text[1:]
	if spaceIndex != -1 {
		commandName = text[1:spaceIndex]
	}
	if s.extensionRunner != nil {
		cmd := s.extensionRunner.GetCommand(commandName)
		if cmd != nil {
			panic(fmt.Sprintf("Extension command \"/%s\" cannot be queued. Use prompt() or execute the command when not streaming.", commandName))
		}
	}
}

// ============================================================================
// Extension Command Execution
// ============================================================================

func (s *AgentSession) tryExecuteExtensionCommand(text string) bool {
	if s.extensionRunner == nil {
		return false
	}
	spaceIndex := strings.Index(text, " ")
	commandName := text[1:]
	args := ""
	if spaceIndex != -1 {
		commandName = text[1:spaceIndex]
		args = text[spaceIndex+1:]
	}

	command := s.extensionRunner.GetCommand(commandName)
	if command == nil {
		return false
	}

	if err := command.Handler(args, context.Background()); err != nil {
		s.extensionRunner.EmitError(extensions.ExtensionError{
			ExtensionPath: "command:" + commandName,
			Event:         "command",
			Error:         err.Error(),
		})
	}
	return true
}

// ============================================================================
// Skill Command Expansion
// ============================================================================

func (s *AgentSession) expandSkillCommand(text string) string {
	if !strings.HasPrefix(text, "/skill:") {
		return text
	}

	spaceIndex := strings.Index(text, " ")
	skillName := text[7:]
	args := ""
	if spaceIndex != -1 {
		skillName = text[7:spaceIndex]
		args = strings.TrimSpace(text[spaceIndex+1:])
	}

	// Find skill by name
	skills := s.resourceLoader.GetSkills().Skills
	var found *Skill
	for i := range skills {
		if skills[i].Name == skillName {
			found = &skills[i]
			break
		}
	}
	if found == nil {
		return text // Unknown skill, pass through
	}

	content, err := os.ReadFile(found.FilePath)
	if err != nil {
		if s.extensionRunner != nil {
			s.extensionRunner.EmitError(extensions.ExtensionError{
				ExtensionPath: found.FilePath,
				Event:         "skill_expansion",
				Error:         err.Error(),
			})
		}
		return text
	}

	body := strings.TrimSpace(string(content))
	// TODO: strip frontmatter when frontmatter utility is ported

	skillBlock := fmt.Sprintf("<skill name=\"%s\" location=\"%s\">\nReferences are relative to %s.\n\n%s\n</skill>",
		found.Name, found.FilePath, found.BaseDir, body)

	if args != "" {
		return skillBlock + "\n\n" + args
	}
	return skillBlock
}

// ============================================================================
// Send Custom Message
// ============================================================================

func (s *AgentSession) SendCustomMessage(customType string, content any, display any, details any, triggerTurn bool, deliverAs string) {
	displayBool := false
	if b, ok := display.(bool); ok {
		displayBool = b
	}
	msg := CreateCustomMessage(customType, content, displayBool, details, currentTimeMillis())

	switch {
	case deliverAs == "nextTurn":
		s.pendingNextTurnMessages = append(s.pendingNextTurnMessages, msg)
	case s.IsStreaming():
		if deliverAs == "followUp" {
			s.Agent.FollowUp(msg)
		} else {
			s.Agent.Steer(msg)
		}
	case triggerTurn:
		s.Agent.Prompt(context.Background(), msg)
	default:
		// Append to agent state + session, emit start/end events
		messages := append(s.Agent.Messages(), msg)
		s.Agent.SetMessages(messages)
		s.SessionManager.AppendCustomMessageEntry(customType, content, displayBool, details)
		s.emit(AgentSessionEvent{EventType: "message_start"})
		s.emit(AgentSessionEvent{EventType: "message_end"})
	}
}

func (s *AgentSession) SendUserMessage(text string, images []any) error {
	return s.Prompt(text, &PromptOptions{
		ExpandPromptTemplates: false,
		StreamingBehavior:     "",
		Images:                images,
		Source:                extensions.InputSourceExtension,
	})
}

// ============================================================================
// Abort
// ============================================================================

func (s *AgentSession) Abort() {
	if s.compactionAbort != nil {
		s.compactionAbort()
		s.compactionAbort = nil
	}
	if s.autoCompactionAbort != nil {
		s.autoCompactionAbort()
		s.autoCompactionAbort = nil
	}
	if s.branchSummaryAbort != nil {
		s.branchSummaryAbort()
		s.branchSummaryAbort = nil
	}
	if s.bashAbort != nil {
		s.bashAbort()
		s.bashAbort = nil
	}
	s.Agent.Abort()
}


// ============================================================================
// Helpers
// ============================================================================

func currentTimeMillis() int64 {
	// Placeholder — will use args or time parsing in production
	return 0
}
