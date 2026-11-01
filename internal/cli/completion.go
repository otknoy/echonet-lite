package cli

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

func positionalValues(values ...string) cobra.CompletionFunc {
	return func(_ *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var result []string
		for _, value := range values {
			if strings.HasPrefix(value, prefix) {
				result = append(result, value)
			}
		}
		return result, cobra.ShellCompDirectiveNoFileComp
	}
}

func completeTargets(cmd *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
	path, err := cmd.Flags().GetString("config")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	doc, err := loadConfig(path, cmd.Flags().Changed("config"))
	if err != nil || doc == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(doc.Config.Targets)+len(doc.Config.Groups))
	for name := range doc.Config.Targets {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	for name := range doc.Config.Groups {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, cobra.ShellCompDirectiveNoFileComp
}
