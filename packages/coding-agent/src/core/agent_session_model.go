package ycode

import (
	"context"
	"fmt"

	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/llm/src"
)

// ============================================================================
// Standalone Thinking Level helpers (usable before AgentSession exists)
// ============================================================================

// ClampThinkingLevel clamps the requested thinking level to one supported by
// the model's available levels. Mirrors the TS clampThinkingLevel from pi-ai.
// Falls back to "medium", then to the first available level.
func ClampThinkingLevel(level sdk.ThinkingLevel, supportedLevels []sdk.ThinkingLevel) sdk.ThinkingLevel {
	if len(supportedLevels) == 0 {
		return sdk.ThinkingOff
	}
	if containsThinkingLevel(supportedLevels, level) {
		return level
	}
	// Prefer medium
	for _, l := range supportedLevels {
		if l == sdk.ThinkingMedium {
			return l
		}
	}
	return supportedLevels[0]
}

// ============================================================================
// Model Management
// ============================================================================

func (s *AgentSession) SetModel(model llm.Model[llm.Api]) error {
	if !s.modelRegistry.HasConfiguredAuth(&model) {
		return fmt.Errorf("No API key for %s/%s", model.Provider, model.ID)
	}

	previousModel := s.GetModel()
	thinkingLevel := s.getThinkingLevelForModelSwitch(nil)
	s.Agent.SetModel(model)
	s.SessionManager.AppendModelChange(string(model.Provider), model.ID)
	s.SettingsManager.SetDefaultModelAndProvider(string(model.Provider), model.ID)

	// Re-clamp thinking level for new model's capabilities
	s.SetThinkingLevel(thinkingLevel)

	s.emitModelSelect(model, previousModel, "set")
	return nil
}

func (s *AgentSession) CycleModel(direction string) (*ModelCycleResult, error) {
	if direction == "" {
		direction = "forward"
	}
	if len(s.scopedModels) > 0 {
		return s.cycleScopedModel(direction)
	}
	return s.cycleAvailableModel(direction)
}

func (s *AgentSession) cycleScopedModel(direction string) (*ModelCycleResult, error) {
	// Filter to only models with configured auth
	var available []ScopedModelEntry
	for _, sm := range s.scopedModels {
		m := sm.Model.(llm.Model[llm.Api])
		if s.modelRegistry.HasConfiguredAuth(&m) {
			available = append(available, sm)
		}
	}
	if len(available) <= 1 {
		return nil, nil
	}

	currentModel := s.GetModel()
	currentIndex := -1
	for i, sm := range available {
		if m, ok := sm.Model.(llm.Model[llm.Api]); ok && modelsAreEqual(m, currentModel) {
			currentIndex = i
			break
		}
	}
	if currentIndex == -1 {
		currentIndex = 0
	}

	length := len(available)
	var nextIndex int
	if direction == "forward" {
		nextIndex = (currentIndex + 1) % length
	} else {
		nextIndex = (currentIndex - 1 + length) % length
	}

	next := available[nextIndex]
	var thinkingLevelPtr *sdk.ThinkingLevel
	if next.ThinkingLevel != "" {
    	tl := sdk.ThinkingLevel(next.ThinkingLevel)
    	thinkingLevelPtr = &tl
	}
	thinkingLevel := s.getThinkingLevelForModelSwitch(thinkingLevelPtr)
	nextModel := next.Model.(llm.Model[llm.Api])
	s.Agent.SetModel(nextModel)
	s.SessionManager.AppendModelChange(string(nextModel.Provider), nextModel.ID)
	s.SettingsManager.SetDefaultModelAndProvider(string(nextModel.Provider), nextModel.ID)
	s.SetThinkingLevel(thinkingLevel)

	s.emitModelSelect(nextModel, currentModel, "cycle")

	return &ModelCycleResult{
		Model:         nextModel,
		ThinkingLevel: s.GetThinkingLevel(),
		IsScoped:      true,
	}, nil
}

func (s *AgentSession) cycleAvailableModel(direction string) (*ModelCycleResult, error) {
	availableModels, err := s.modelRegistry.GetAvailable()
	if err != nil || len(availableModels) <= 1 {
		return nil, err
	}

	currentModel := s.GetModel()
	currentIndex := -1
	for i, m := range availableModels {
		if modelsAreEqual(*m, currentModel) {
			currentIndex = i
			break
		}
	}
	if currentIndex == -1 {
		currentIndex = 0
	}

	length := len(availableModels)
	var nextIndex int
	if direction == "forward" {
		nextIndex = (currentIndex + 1) % length
	} else {
		nextIndex = (currentIndex - 1 + length) % length
	}

	nextModel := availableModels[nextIndex]
	thinkingLevel := s.getThinkingLevelForModelSwitch(nil)
	s.Agent.SetModel(*nextModel)
	s.SessionManager.AppendModelChange(string(nextModel.Provider), nextModel.ID)
	s.SettingsManager.SetDefaultModelAndProvider(string(nextModel.Provider), nextModel.ID)
	s.SetThinkingLevel(thinkingLevel)

	s.emitModelSelect(*nextModel, currentModel, "cycle")

	return &ModelCycleResult{
		Model:         *nextModel,
		ThinkingLevel: s.GetThinkingLevel(),
		IsScoped:      false,
	}, nil
}

func (s *AgentSession) emitModelSelect(nextModel, previousModel llm.Model[llm.Api], source string) {
	if modelsAreEqual(previousModel, nextModel) {
		return
	}
	if s.extensionRunner != nil {
		s.extensionRunner.Emit(
			context.Background(),
			map[string]any{
			"type":          "model_select",
			"model":         nextModel,
			"previousModel": previousModel,
			"source":        source,
		})
	}
}

// ============================================================================
// Thinking Level Management
// ============================================================================

func (s *AgentSession) SetThinkingLevel(level sdk.ThinkingLevel) {
	availableLevels := s.GetAvailableThinkingLevels()
	effectiveLevel := level
	if !containsThinkingLevel(availableLevels, level) {
		effectiveLevel = s.clampThinkingLevel(level, availableLevels)
	}

	previousLevel := s.Agent.ThinkingLevel()
	isChanging := effectiveLevel != previousLevel

	s.Agent.SetThinkingLevel(effectiveLevel)

	if isChanging {
		s.SessionManager.AppendThinkingLevelChange(string(effectiveLevel))
		if s.SupportsThinking() || effectiveLevel != sdk.ThinkingOff {
			s.SettingsManager.SetDefaultThinkingLevel(string(effectiveLevel))
		}
		s.emit(AgentSessionEvent{
			EventType:     "thinking_level_changed",
			ThinkingLevel: effectiveLevel,
		})
		if s.extensionRunner != nil {
			s.extensionRunner.Emit(
				context.Background(),
				map[string]any{
				"type":          "thinking_level_select",
				"level":         effectiveLevel,
				"previousLevel": previousLevel,
			})
		}
	}
}

func (s *AgentSession) CycleThinkingLevel() *sdk.ThinkingLevel {
	if !s.SupportsThinking() {
		return nil
	}

	levels := s.GetAvailableThinkingLevels()
	currentIndex := indexOfThinkingLevel(levels, s.GetThinkingLevel())
	nextIndex := (currentIndex + 1) % len(levels)
	nextLevel := levels[nextIndex]

	s.SetThinkingLevel(nextLevel)
	return &nextLevel
}

func (s *AgentSession) GetAvailableThinkingLevels() []sdk.ThinkingLevel {
	model := s.GetModel()
	if model.ID == "" || !model.Reasoning {
		return []sdk.ThinkingLevel{sdk.ThinkingOff}
	}
	return ThinkingLevels
}

func (s *AgentSession) SupportsThinking() bool {
	model := s.GetModel()
	return model.Reasoning
}

func (s *AgentSession) getThinkingLevelForModelSwitch(explicitLevel *sdk.ThinkingLevel) sdk.ThinkingLevel {
	if explicitLevel != nil {
		return *explicitLevel
	}
	if !s.SupportsThinking() {
		defaultLevel := s.SettingsManager.GetDefaultThinkingLevel()
		if defaultLevel != "" {
			return sdk.ThinkingLevel(defaultLevel)
		}
		return sdk.ThinkingLevel(DefaultThinkingLevel)
	}
	return s.GetThinkingLevel()
}

func (s *AgentSession) clampThinkingLevel(level sdk.ThinkingLevel, availableLevels []sdk.ThinkingLevel) sdk.ThinkingLevel {
	return ClampThinkingLevel(level, availableLevels)
}

// ============================================================================
// Queue Mode Management
// ============================================================================

func (s *AgentSession) SetSteeringMode(mode string) {
	s.Agent.SetSteeringMode(sdk.QueueMode(mode))
	s.SettingsManager.SetSteeringMode(mode)
}

func (s *AgentSession) SetFollowUpMode(mode string) {
	s.Agent.SetFollowUpMode(sdk.QueueMode(mode))
	s.SettingsManager.SetFollowUpMode(mode)
}

// ============================================================================
// Helpers
// ============================================================================

func modelsAreEqual(a, b llm.Model[llm.Api]) bool {
	if a.ID == "" || b.ID == "" {
		return false
	}
	return a.Provider == b.Provider && a.ID == b.ID
}

func containsThinkingLevel(levels []sdk.ThinkingLevel, target sdk.ThinkingLevel) bool {
	for _, l := range levels {
		if l == target {
			return true
		}
	}
	return false
}

func indexOfThinkingLevel(levels []sdk.ThinkingLevel, target sdk.ThinkingLevel) int {
	for i, l := range levels {
		if l == target {
			return i
		}
	}
	return 0
}
