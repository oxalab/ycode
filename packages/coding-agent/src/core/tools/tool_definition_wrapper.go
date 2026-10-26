package tools

import (
	"context"
	"encoding/json"
	sdk "ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/extensions"
	"ycode/packages/llm/src"
)

// toAgentToolResult converts a ToolDefinition result into an AgentToolResult.
// Used for both the final return and streaming partials (ExecuteUpdate).
func toAgentToolResult(result any) sdk.AgentToolResult[any] {
	switch v := result.(type) {
	case *ToolExecuteResult:
		return sdk.AgentToolResult[any]{Content: v.Content, Details: v.Details, Terminate: v.Terminate}
	case []llm.ContentBlock:
		return sdk.AgentToolResult[any]{Content: v}
	default:
		return sdk.AgentToolResult[any]{}
	}
}

// WrapToolDefinition wraps a tool definition into an AgentTool for the core runtime.
// ctxFactory supplies the ExtensionContext at execute time (TS
// tool-definition-wrapper.ts passes ctxFactory?.() as the 5th execute arg);
// preference order matches the session bridge: ExecuteUpdate > ExecuteContext
// (factory present) > Execute.
func WrapToolDefinition(def *extensions.ToolDefinition, ctxFactory func() extensions.ExtensionContext) *sdk.AgentTool[any] {
	var params map[string]any
	if def.Parameters != nil {
		json.Unmarshal(def.Parameters, &params)
	}
	return &sdk.AgentTool[any] {
		Name: 			def.Name,
		Label: 			def.Label,
		Description: 	def.Description,
		Parameters: 	params,
		ExecutionMode:  sdk.ToolExecutionMode(def.ExecutionMode),
		PrepareArguments: def.PrepareArguments,
		Execute: func(toolCallID string, params any, signal context.Context, onUpdate sdk.AgentToolUpdateCallback[any]) (sdk.AgentToolResult[any], error) {
    		args, _ := json.Marshal(params)
    		if def.ExecuteUpdate != nil {
        		result, err := def.ExecuteUpdate(signal, args, func(partial any) {
            		if onUpdate != nil {
                		onUpdate(toAgentToolResult(partial))
            		}
        		})
        		if err != nil {
            		return sdk.AgentToolResult[any]{}, err
        		}
        		return toAgentToolResult(result), nil
    		}
    		if def.ExecuteContext != nil && ctxFactory != nil {
        		result, err := def.ExecuteContext(signal, args, ctxFactory())
        		if err != nil {
            		return sdk.AgentToolResult[any]{}, err
        		}
        		return toAgentToolResult(result), nil
    		}
    		if def.ExecuteContext != nil && ctxFactory != nil {
        		result, err := def.ExecuteContext(signal, args, ctxFactory())
        		if err != nil {
            		return sdk.AgentToolResult[any]{}, err
        		}
        		return toAgentToolResult(result), nil
    		}
    		result, err := def.Execute(signal, args)
    		if err != nil {
        		return sdk.AgentToolResult[any]{}, err
    		}
    		return toAgentToolResult(result), nil
		},
	}
}


// WrapToolDefinitions: Wraps multiple ToolDefinitions into AgentTools for the core runtime.
func WrapToolDefinitions(defs []*extensions.ToolDefinition, ctxFactory func() extensions.ExtensionContext) []*sdk.AgentTool[any] {
	result := make([]*sdk.AgentTool[any], len(defs))
	for i, def := range defs {
		result[i] = WrapToolDefinition(def, ctxFactory)
	}
	return result
}

/*
CreateToolDefinitionFromAgentTool: Synthesizes a minimal ToolDefinition from an AgentTool.
 - This keeps the internal registry definition-first even when a caller provides plain
 AgentTool overrides that do not inlcude prompt metadata or renderers.
*/
func CreateToolDefinitionFromAgentTool(tool *sdk.AgentTool[any]) *extensions.ToolDefinition {
	var params json.RawMessage
	if tool.Parameters != nil {
		params, _ = json.Marshal(tool.Parameters)
	}
		return &extensions.ToolDefinition{
			Name:          tool.Name,
			Label:         tool.Label,
			Description:   tool.Description,
			Parameters:    params,
			ExecutionMode: extensions.ToolExecutionMode(tool.ExecutionMode),
			PrepareArguments: tool.PrepareArguments,
			ExecuteUpdate: func(ctx context.Context, args json.RawMessage, onUpdate func(partial any)) (any, error) {
				var p any
				if args != nil {
					_ = json.Unmarshal(args, &p)
				}
				var cb sdk.AgentToolUpdateCallback[any]
				if onUpdate != nil {
					cb = func(partial sdk.AgentToolResult[any]) { onUpdate(partial) }
				}
				result, err := tool.Execute("", p, ctx, cb)
				if err != nil {
					return nil, err
				}
				return result.Content, nil
			},
			Execute: func(ctx context.Context, args json.RawMessage) (any, error) {
				var p any
				if args != nil {
					_ = json.Unmarshal(args, &p)
				}
				result, err := tool.Execute("", p, ctx, nil)
				if err != nil {
					return nil, err
				}
				return result.Content, nil
			},
		}
}


/*
FOOTNOTE:
 1. ctxFactory — RESOLVED (story 24): the wrapper now passes ctxFactory?.()
 to ExecuteContext when both are set, matching TS tool-definition-wrapper.ts
 (definition.execute(toolCallId, params, signal, onUpdate, ctxFactory?.())).
 ExecuteContext-only definitions no longer run context-free through this
 wrapper. (The production session path uses buildAgentToolFromDefinition,
 which has preferred ExecuteContext since story 15.)

 2. CreateToolDefinitionFromAgentTool loses toolCallID — the reverse wrapper
 passes empty "" for it. onUpdate is no longer lost: the reverse wrapper sets
 ExecuteUpdate and forwards partials.

 3. json.Marshal(params) error silently ignored — If params can't be marshaled, 
 the tool receives nil args. This matches the TS pattern where invalid params would 
 cause an error in the tool itself.
*/