package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/databus23/helm-diff/v3/diff"
	"github.com/databus23/helm-diff/v3/manifest"
)

type Local struct {
	Chart1              string
	Chart2              string
	Release             string
	Namespace           string
	DetailedExitCode    bool
	IncludeTests        bool
	IncludeCRDs         bool
	NormalizeManifests  bool
	EnableDNS           bool
	ValueFiles          valueFiles
	Values              []string
	StringValues        []string
	StringLiteralValues []string
	JsonValues          []string
	FileValues          []string
	PostRenderer        string
	PostRendererArgs    []string
	ExtraAPIs           []string
	KubeVersion         string
	diff.Options
}

const localCmdLongUsage = `
This command compares the manifests of two local chart directories.

It renders both charts using 'helm template' and shows the differences
between the resulting manifests.

This is useful for:
 - Comparing different versions of a chart
 - Previewing changes before committing
 - Validating chart modifications
`

func localCmd() *cobra.Command {
	diff := &Local{
		Release: "release",
	}

	localCmd := &cobra.Command{
		Use:   "local [flags] CHART1 CHART2",
		Short: "Shows diff between two local chart directories",
		Long:  localCmdLongUsage,
		Example: strings.Join([]string{
			"  helm diff local ./chart-v1 ./chart-v2",
			"  helm diff local ./chart-v1 ./chart-v2 -f values.yaml",
			"  helm diff local /path/to/chart-a /path/to/chart-b --set replicas=3",
		}, "\n"),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Suppress the command usage on error. See #77 for more info
			cmd.SilenceUsage = true

			if v, _ := cmd.Flags().GetBool("version"); v {
				fmt.Println(Version)
				return nil
			}

			if err := checkArgsLength(len(args), "chart1 path", "chart2 path"); err != nil {
				return err
			}

			ProcessDiffOptions(cmd.Flags(), &diff.Options)

			diff.Chart1 = args[0]
			diff.Chart2 = args[1]

			if diff.Namespace == "" {
				diff.Namespace = os.Getenv("HELM_NAMESPACE")
			}

			return diff.Run()
		},
	}

	addChartRenderFlags(localCmd.Flags(), diff)

	AddDiffOptions(localCmd.Flags(), &diff.Options)

	localCmd.SuggestionsMinimumDistance = 1

	return localCmd
}

func addChartRenderFlags(f *pflag.FlagSet, l *Local) {
	f.StringVar(&l.Release, "release", "release", "release name to use for template rendering")
	f.StringVar(&l.Namespace, "namespace", "", "namespace to use for template rendering")
	f.BoolVar(&l.DetailedExitCode, "detailed-exitcode", false, "return a non-zero exit code when there are changes")
	f.BoolVar(&l.IncludeTests, "include-tests", false, "enable the diffing of the helm test hooks")
	f.BoolVar(&l.IncludeCRDs, "include-crds", false, "include CRDs in the diffing")
	f.BoolVar(&l.NormalizeManifests, "normalize-manifests", false, "normalize manifests before running diff to exclude style differences from the output")
	f.BoolVar(&l.EnableDNS, "enable-dns", false, "enable DNS lookups when rendering templates")
	f.VarP(&l.ValueFiles, "values", "f", "specify values in a YAML file (can specify multiple)")
	f.StringArrayVar(&l.Values, "set", []string{}, "set values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)")
	f.StringArrayVar(&l.StringValues, "set-string", []string{}, "set STRING values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)")
	f.StringArrayVar(&l.StringLiteralValues, "set-literal", []string{}, "set STRING literal values on the command line")
	f.StringArrayVar(&l.JsonValues, "set-json", []string{}, "set JSON values on the command line (can specify multiple or separate values with commas: key1=jsonval1,key2=jsonval2)")
	f.StringArrayVar(&l.FileValues, "set-file", []string{}, "set values from respective files specified via the command line (can specify multiple or separate values with commas: key1=path1,key2=path2)")
	f.StringVar(&l.PostRenderer, "post-renderer", "", "the path to an executable to be used for post rendering. If it exists in $PATH, the binary will be used, otherwise it will try to look for the executable at the given path")
	f.StringArrayVar(&l.PostRendererArgs, "post-renderer-args", []string{}, "an argument to the post-renderer (can specify multiple)")
	f.StringArrayVarP(&l.ExtraAPIs, "api-versions", "a", []string{}, "Kubernetes api versions used for Capabilities.APIVersions")
	f.StringVar(&l.KubeVersion, "kube-version", "", "Kubernetes version used for Capabilities.KubeVersion")
}

func (l *Local) Run() error {
	manifest1, err := l.renderChart(l.Chart1)
	if err != nil {
		return fmt.Errorf("Failed to render chart %s: %w", l.Chart1, err)
	}

	manifest2, err := l.renderChart(l.Chart2)
	if err != nil {
		return fmt.Errorf("Failed to render chart %s: %w", l.Chart2, err)
	}

	excludes := []string{manifest.Helm3TestHook, manifest.Helm2TestSuccessHook}
	if l.IncludeTests {
		excludes = []string{}
	}

	specs1 := manifest.Parse(string(manifest1), l.Namespace, l.NormalizeManifests, excludes...)
	specs2 := manifest.Parse(string(manifest2), l.Namespace, l.NormalizeManifests, excludes...)

	seenAnyChanges := diff.Manifests(specs1, specs2, &l.Options, os.Stdout)

	if l.DetailedExitCode && seenAnyChanges {
		return Error{
			error: errors.New("identified at least one change, exiting with non-zero exit code (detailed-exitcode parameter enabled)"),
			Code:  2,
		}
	}

	return nil
}

func (l *Local) renderChart(chartPath string) ([]byte, error) {
	flags := []string{}

	if l.IncludeCRDs {
		flags = append(flags, "--include-crds")
	}

	if l.Namespace != "" {
		flags = append(flags, "--namespace", l.Namespace)
	}

	if l.PostRenderer != "" {
		flags = append(flags, "--post-renderer", l.PostRenderer)
	}

	for _, arg := range l.PostRendererArgs {
		flags = append(flags, "--post-renderer-args", arg)
	}

	for _, valueFile := range l.ValueFiles {
		if strings.TrimSpace(valueFile) == "-" {
			bytes, err := io.ReadAll(os.Stdin)
			if err != nil {
				return nil, err
			}

			tmpfile, err := os.CreateTemp("", "helm-diff-stdin-values")
			if err != nil {
				return nil, err
			}
			defer func() {
				_ = os.Remove(tmpfile.Name())
			}()

			if _, err := tmpfile.Write(bytes); err != nil {
				_ = tmpfile.Close()
				return nil, err
			}

			if err := tmpfile.Close(); err != nil {
				return nil, err
			}

			flags = append(flags, "--values", tmpfile.Name())
		} else {
			flags = append(flags, "--values", valueFile)
		}
	}

	for _, value := range l.Values {
		flags = append(flags, "--set", value)
	}

	for _, stringValue := range l.StringValues {
		flags = append(flags, "--set-string", stringValue)
	}

	for _, stringLiteralValue := range l.StringLiteralValues {
		flags = append(flags, "--set-literal", stringLiteralValue)
	}

	for _, jsonValue := range l.JsonValues {
		flags = append(flags, "--set-json", jsonValue)
	}

	for _, fileValue := range l.FileValues {
		flags = append(flags, "--set-file", fileValue)
	}

	if l.EnableDNS {
		flags = append(flags, "--enable-dns")
	}

	for _, a := range l.ExtraAPIs {
		flags = append(flags, "--api-versions", a)
	}

	if l.KubeVersion != "" {
		flags = append(flags, "--kube-version", l.KubeVersion)
	}

	args := []string{"template", l.Release, chartPath}
	args = append(args, flags...)

	cmd := exec.Command(helmBinary(), args...)
	return outputWithRichError(cmd)
}
