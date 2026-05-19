package extensions


// --- Event Types ---
type InputSource string 

const (
	InputSourceInteractive 	InputSource = "interactive"
	InputSourceRPC		 	InputSource = "rpc"
	InputSourceExtension 	InputSource = "extension"
)

type ModelSelectSource string

const (
	ModelSelectSourceSet 	ModelSelectSource = "set"
	ModelSelectSourceCycle 	ModelSelectSource = "cycle"
	ModelSelectSourceRestore 	ModelSelectSource = "restore"
)

type WidgetPlacement string 

const (
	WidgetPlacementAboveEditor 	WidgetPlacement = "aboveEditor"
	WidgetPlacementBelowEditor	WidgetPlacement = "belowEditor"
)

// --- Context Types ---
type ContextUsage struct {
	Tokens 		*int 	`json:"tokens"`
	ContextWindow int 	`json:"contextWindow"`
	Percent 	*int 	`json:"percent"`
}

type CompactOptions struct {
	CustomInstructions 	string
	OnComplete 			func(result any)
	OnError 			func(err error)
}

// --- Event Structures ---

type BaseEvent struct {
	Type 	string 	`json:"type"`
}

type ResoucesDiscoverEvent struct {
	Type 	string 	`json:"type"`
	Cwd 	string 	`json:"cwd"`
	Reason 	string 	`json:"reason"`
}

type ResoucesDiscoverResult struct {
	SkillPaths 	[]string 	`json:"skillPaths,omitempty"`
	PromptPaths []string 	`json:"promptPaths,omitempty"`
	ThemePaths 	[]string 	`json:"themePaths,omitempty"`
}

type SessionStartEvent struct {
	Type 		string 	`json:"type"`
	Reason 		string 	`json:"reason"`
	PreviousSessionFile string 	`json:"previousSessionFile,omitempty"`
}

type SessionBeforeSwitchEvent struct {
	Type 		string 	`json:"type"`
	Reason 		string 	`json:"reason"`
	
}