package ycode

type SourceScope string

const(
	ScopeUser 	 	SourceScope = "user"
	ScopeProject 	SourceScope = "project"
	ScopeTemporary 	SourceScope = "temporary"
)

type SourceOrigin string

const (
	OriginPackage 	SourceOrigin = "package"
	OriginTopLevel 	SourceOrigin = "top-level"
)

type PathMetadata struct {
	Source string
	Scope SourceScope
	Origin SourceOrigin
	BaseDir string
}

type SourceInfo struct {
	Path 	string `json:"path"`
	Source 	string `json:"source"`
	Scope 	string `json:"scope"`
	Origin 	SourceOrigin `json:"origin"`
	BaseDir string `json:"baseDir,omitempty"`
}

func CreateSourceInfo(path string, metadata PathMetadata) SourceInfo {
	return SourceInfo{
		Path: 	path,
		Source: metadata.Source,
		Scope:  string(metadata.Scope),
		Origin: metadata.Origin,
		BaseDir:metadata.BaseDir,
	}
}

func CreateSyntheticSourceInfo(path string, source string, scope SourceScope, origin SourceOrigin, baseDir string) SourceInfo {
	s := scope
	if s == "" {
		s = ScopeTemporary
	}
	o := origin
	if o == "" {
		o = OriginTopLevel
	}
	return SourceInfo{
		Path: path,
		Source: source,
		Scope: string(s),
		Origin: o,
		BaseDir: baseDir,
	}
}