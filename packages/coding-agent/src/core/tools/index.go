package tools

import (
	"fmt"

	"ycode/packages/agent-sdk/src"
	"ycode/packages/coding-agent/src/core/extensions"
)

type ToolName string

const (
	ToolNameRead  ToolName = "read"
	ToolNameBash  ToolName = "bash"
	ToolNameEdit  ToolName = "edit"
	ToolNameWrite ToolName = "write"
	ToolNameGrep  ToolName = "grep"
	ToolNameFind  ToolName = "find"
	ToolNameLs    ToolName = "ls"
)

var AllToolNames = map[ToolName]bool{
	ToolNameRead:  true,
	ToolNameBash:  true,
	ToolNameEdit:  true,
	ToolNameWrite: true,
	ToolNameGrep:  true,
	ToolNameFind:  true,
	ToolNameLs:    true,
}

type ToolsOptions struct {
	Read  *ReadToolOptions
	Bash  *BashToolOptions
	Write *WriteToolOptions
	Edit  *EditToolOptions
	Grep  *GrepToolOptions
	Find  *FindToolOptions
	Ls    *LsToolOptions
}

func CreateToolDefinition(name ToolName, cwd string, opts *ToolsOptions) *extensions.ToolDefinition {
	switch name {
	case ToolNameRead:
		return CreateReadToolDefinition(cwd, opts.Read)
	case ToolNameBash:
		return CreateBashToolDefinition(cwd, opts.Bash)
	case ToolNameEdit:
		return CreateEditToolDefinition(cwd, opts.Edit)
	case ToolNameWrite:
		return CreateWriteToolDefinition(cwd, opts.Write)
	case ToolNameGrep:
		return CreateGrepToolDefinition(cwd, opts.Grep)
	case ToolNameFind:
		return CreateFindToolDefinition(cwd, opts.Find)
	case ToolNameLs:
		return CreateLsToolDefinition(cwd, opts.Ls)
	default:
		panic(fmt.Sprintf("unknown tool name: %s", name))
	}
}

func CreateTool(name ToolName, cwd string, opts *ToolsOptions) *sdk.AgentTool[any] {
	switch name {
	case ToolNameRead:
		return CreateReadTool(cwd, opts.Read)
	case ToolNameBash:
		return CreateBashTool(cwd, opts.Bash)
	case ToolNameEdit:
		return CreateEditTool(cwd, opts.Edit)
	case ToolNameWrite:
		return CreateWriteTool(cwd, opts.Write)
	case ToolNameGrep:
		return CreateGrepTool(cwd, opts.Grep)
	case ToolNameFind:
		return CreateFindTool(cwd, opts.Find)
	case ToolNameLs:
		return CreateLsTool(cwd, opts.Ls)
	default:
		panic(fmt.Sprintf("unknown tool name: %s", name))
	}
}

func CreateCodingToolDefinitions(cwd string, opts *ToolsOptions) []*extensions.ToolDefinition {
	return []*extensions.ToolDefinition{
		CreateReadToolDefinition(cwd, opts.Read),
		CreateBashToolDefinition(cwd, opts.Bash),
		CreateEditToolDefinition(cwd, opts.Edit),
		CreateWriteToolDefinition(cwd, opts.Write),
	}
}

func CreateReadOnlyToolDefinitions(cwd string, opts *ToolsOptions) []*extensions.ToolDefinition {
	return []*extensions.ToolDefinition{
		CreateReadToolDefinition(cwd, opts.Read),
		CreateGrepToolDefinition(cwd, opts.Grep),
		CreateFindToolDefinition(cwd, opts.Find),
		CreateLsToolDefinition(cwd, opts.Ls),
	}
}

func CreateAllToolDefinitions(cwd string, opts *ToolsOptions) map[ToolName]*extensions.ToolDefinition {
	if opts == nil {
    	opts = &ToolsOptions{}
	}
	// Ensure no field is nil — each Create* function gets a valid struct
    if opts.Read == nil {
        opts.Read = &ReadToolOptions{}
    }
    if opts.Bash == nil {
        opts.Bash = &BashToolOptions{}
    }
    if opts.Write == nil {
        opts.Write = &WriteToolOptions{}
    }
    if opts.Edit == nil {
        opts.Edit = &EditToolOptions{}
    }
    if opts.Grep == nil {
        opts.Grep = &GrepToolOptions{}
    }
    if opts.Find == nil {
        opts.Find = &FindToolOptions{}
    }
    if opts.Ls == nil {
        opts.Ls = &LsToolOptions{}
    }
	return map[ToolName]*extensions.ToolDefinition{
		ToolNameRead:  CreateReadToolDefinition(cwd, opts.Read),
		ToolNameBash:  CreateBashToolDefinition(cwd, opts.Bash),
		ToolNameEdit:  CreateEditToolDefinition(cwd, opts.Edit),
		ToolNameWrite: CreateWriteToolDefinition(cwd, opts.Write),
		ToolNameGrep:  CreateGrepToolDefinition(cwd, opts.Grep),
		ToolNameFind:  CreateFindToolDefinition(cwd, opts.Find),
		ToolNameLs:    CreateLsToolDefinition(cwd, opts.Ls),
	}
}

func CreateCodingTools(cwd string, opts *ToolsOptions) []*sdk.AgentTool[any] {
	return []*sdk.AgentTool[any]{
		CreateReadTool(cwd, opts.Read),
		CreateBashTool(cwd, opts.Bash),
		CreateEditTool(cwd, opts.Edit),
		CreateWriteTool(cwd, opts.Write),
	}
}

func CreateReadOnlyTools(cwd string, opts *ToolsOptions) []*sdk.AgentTool[any] {
	return []*sdk.AgentTool[any]{
		CreateReadTool(cwd, opts.Read),
		CreateGrepTool(cwd, opts.Grep),
		CreateFindTool(cwd, opts.Find),
		CreateLsTool(cwd, opts.Ls),
	}
}

func CreateAllTools(cwd string, opts *ToolsOptions) map[ToolName]*sdk.AgentTool[any] {
	return map[ToolName]*sdk.AgentTool[any]{
		ToolNameRead:  CreateReadTool(cwd, opts.Read),
		ToolNameBash:  CreateBashTool(cwd, opts.Bash),
		ToolNameEdit:  CreateEditTool(cwd, opts.Edit),
		ToolNameWrite: CreateWriteTool(cwd, opts.Write),
		ToolNameGrep:  CreateGrepTool(cwd, opts.Grep),
		ToolNameFind:  CreateFindTool(cwd, opts.Find),
		ToolNameLs:    CreateLsTool(cwd, opts.Ls),
	}
}
