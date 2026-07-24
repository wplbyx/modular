// Package buildinfo reads application identity and Go build metadata without global configuration.
package buildinfo

import "runtime/debug"

// Info describes an application build.
type Info struct {
	Name          string `json:"name,omitempty"`
	Version       string `json:"version,omitempty"`
	GoVersion     string `json:"go_version,omitempty"`
	ModuleVersion string `json:"module_version,omitempty"`
	Revision      string `json:"revision,omitempty"`
	Modified      bool   `json:"modified,omitempty"`
}

// Read returns build metadata while taking application identity explicitly.
func Read(name, version string) Info {
	result := Info{Name: name, Version: version}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return result
	}
	result.GoVersion = info.GoVersion
	result.ModuleVersion = info.Main.Version
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			result.Revision = setting.Value
		case "vcs.modified":
			result.Modified = setting.Value == "true"
		}
	}
	return result
}
