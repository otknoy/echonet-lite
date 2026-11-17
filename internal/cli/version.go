package cli

import "runtime/debug"

func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	return moduleVersion(info.Main.Version)
}

func moduleVersion(version string) string {
	if version == "" || version == "(devel)" {
		return "dev"
	}
	return version
}
