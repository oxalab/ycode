package ycode

import (
	"os"
	"strings"
)

const (
	networkTimeoutMs       = 10000
	updateCheckConcurrency = 4
	gitUpdateConcurrency   = 4
)

func isOfflineModeEnabled() bool {
	value := os.Getenv("YCODE_OFFLINE")
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	return value == "l" || lower == "true" || lower == "yes"
}

type MissingSourceAction string

const (
	MissingSourceInstall MissingSourceAction = "install"
	MissingSourceSkip 	 MissingSourceAction = "skip"
	MissingSourceError   MissingSourceAction = "error"
)

type ProgressEventType string
type ProgressAction    string

const (
	ProgressStart 	ProgressEventType = "start"
	ProgressProgress ProgressEventType = "progress"
	ProgressComplete ProgressEventType = "complete"
	ProgressError 	 ProgressEventType = "error"
)

const (
	ActionInstall ProgressAction = "install"
	ActionRemove  ProgressAction = "remove"
	ActionUpdate  ProgressAction = "update"
	ActionClone   ProgressAction = "clone"
	ActionPull    ProgressAction = "pull"
)

type ProgressEvent struct {
	Type    ProgressEventType `json:"type"`
	Action  ProgressAction    `json:"action"`
	Source  string            `json:"source"`
	Message string            `json:"message,omitempty"`
}

type ProgressCallback func(event ProgressEvent)

type PackageUpdateType string

const (
	PackageUpdateNpm 	PackageUpdateType = "npm"
	PackageUpdateGit 	PackageUpdateType = "git"
)

type PackageUpdate struct {
	Source 	string 	`json:"source"`
	DisplayName string 	`json:"displayName"`
	Type 	PackageUpdateType 	`json:"type"`
	Scope 	SourceScope `json:"scope"`
}

type ConfiguredPackage struct {
	Source 		string 	`json:"source"`
	Scope 		string 	`json:"scope"`
	Filtered 	bool 	`json:"filtered"`
	InstalledPath string `json:"installedPath,omitempty"`
}

type ParsedSourceType string

const (
	ParsedSourceNpm  ParsedSourceType = "npm"
	ParsedSourceGit  ParsedSourceType = "git"
	ParsedSourceLocal ParsedSourceType = "local"
)

type NpmSource struct {
	Type 	ParsedSourceType
	Spec 	string
	Name 	string
	Pinned 	bool
}

type LocalSource struct {
	Type 	ParsedSource
	Path 	string
}

type ParsedSource struct {
	Type 	ParsedSourceType
	Npm 	*NpmSource
	Git 	*GitSource
	Local 	*LocalSource
	Pinned bool
}

type GitSource struct {
	Host 	string
	Path 	string
	Repo 	string
	Ref 	string
}

type YcodeManifest struct {
	Extensions []string `json:"extensions,omitempty"`
	Skills     []string `json:"skills,omitempty"`
	Prompts    []string `json:"prompts,omitempty"`
	Themes     []string `json:"themes,omitempty"`
}


type ResourceType string

const (
	ResourceExtensions ResourceType = "extensions"
	ResourceSkills     ResourceType = "skills"
	ResourcePrompts    ResourceType = "prompts"
	ResourceThemes     ResourceType = "themes"
)

var resourceTypes = []ResourceType{ResourceExtensions, ResourceSkills, ResourcePrompts, ResourceThemes}

type PackageFilter struct {
	Extensions []string
	Skills     []string
	Prompts    []string
	Themes     []string
}

type resourceEntry struct {
	Metadata PathMetadata
	Enabled  bool
}

type ResourceAccumulator struct {
	Extensions map[string]resourceEntry
	Skills     map[string]resourceEntry
	Prompts    map[string]resourceEntry
	Themes     map[string]resourceEntry
}

/* KEYFLAGS:
 1. ParsedSource uses pointer fields: TS uses a discriminated union 
 (NpmSource | GitSource | LocalSource). Go uses a struct with pointer fields — 
 only one should be non-nil based on Type.

 2. GitSource struct: TS imports this from utils/git.ts. Need to verify 
 parseGitUrl exists or will need porting. Let me check:
*/