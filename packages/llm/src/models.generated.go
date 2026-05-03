package llm

// This file contains minimal model definitions for each provider.
// This is a small set. More models will be added as per requirements.

var Models = map[string]map[string]*Model[Api]{
	"anthropic": {
		"claude-3-5-haiku-20241022":{
			ID: 		"claude-3-5-haiku-20241022",
			Name: 		"Claude 3.5 Haiku",
			Api: 		"anthropic",
			Provider: 	"anthropic",
			BaseURL: 	"https://api.anthropic.com",
			Reasoning: 	false,
			Input:		[]InputType{InputText, InputImage},
			Cost:      	ModelCost{Input: 0.8, Output: 4.0, CacheRead: 0.08, CacheWrite: 0.24},
			ContextWindow: 200000,
			MaxTokens: 	8192,
		},
		"claude-3-5-sonnet-20241022": {
			ID: 		"claude-3-5-sonnet-20241022",
			Name: 		"Claude 3.5 Sonnet",
			Api: 		"anthropic",
			Provider: 	"anthropic",
			BaseURL: 	"https://api.anthropic.com",
			Reasoning: 	false,
			Input: 		[]InputType{InputText, InputImage},
			Cost: 		ModelCost{Input: 3.0, Output: 15.0, CacheRead: 0.30, CacheWrite: 0.90},
			ContextWindow: 200000,
			MaxTokens: 	8192,
		},
	},
}