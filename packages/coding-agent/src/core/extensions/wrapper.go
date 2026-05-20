package extensions 

import (
	"context"
	sdk "ycode/packages/agent-sdk/src"
)

func WrapRegisteredTool(registeredTool *RegisteredTool, createCtx func() any) *sdk.AgentTool[any] {
	def := registeredTool.Definition
	return &sdk.AgentTool[any]{
		Name:        def.Name,
		Label:       def.Label,
		Description: def.Description,
		Parameters:  nil,
		Execute: func(toolCallID string, params any, ctx context.Context, onUpdate sdk.AgentToolUpdateCallback[any]) (sdk.AgentToolResult[any], error) {
			if def.Execute != nil {
				_, err := def.Execute(nil, nil)
				if err != nil {
					return sdk.AgentToolResult[any]{}, err
				}
				return sdk.AgentToolResult[any]{Content: nil}, nil
			}
			return sdk.AgentToolResult[any]{}, nil
		},
	}
}

func WrapRegisteredTools(registeredTools []*RegisteredTool, createCtx func() any) []*sdk.AgentTool[any] {
	result := make([]*sdk.AgentTool[any], len(registeredTools))
	for i, tool := range registeredTools {
		result[i] = WrapRegisteredTool(tool, createCtx)
	}
	return result
}