package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

type gitDiff struct {
	chartPath   string
	remote      string
	localDiffer *Local
}

const gitCmdLongUsage = `
This command compares Helm charts at different git references without
modifying the working tree.

The chart path is relative to the git repository root, not the current
working directory. This ensures consistency when comparing different refs.

Chart dependencies are automatically built for each ref using 'helm dependency build'.

When no refs are specified, the primary branch from the configured remote
is compared against the working tree. The primary branch is detected using:
  git symbolic-ref refs/remotes/<remote>/HEAD

Examples:
  # Compare primary branch vs working tree (chart at repo root)
  helm diff git

  # Compare specific ref vs working tree
  helm diff git v1.0.0

  # Compare two refs
  helm diff git v1.0.0 v2.0.0

  # Chart in subdirectory (path relative to repo root)
  helm diff git --chart-path=charts/myapp main

  # Use custom remote for primary branch detection
  helm diff git --remote=upstream

  # With values and diff options
  helm diff git -f values.yaml --set replicas=3 --context=5 main
`

func gitCmd() *cobra.Command {
	git := &gitDiff{
		chartPath: ".",
		remote:    "origin",
		localDiffer: &Local{
			Release: "release",
		},
	}

	cmd := &cobra.Command{
		Use:   "git [flags] [REF1] [REF2]",
		Short: "Shows diff between git refs of a chart",
		Long:  gitCmdLongUsage,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 2 {
				return fmt.Errorf("too many arguments (expected 0, 1, or 2)")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true

			ProcessDiffOptions(cmd.Flags(), &git.localDiffer.Options)

			if git.localDiffer.Namespace == "" {
				git.localDiffer.Namespace = os.Getenv("HELM_NAMESPACE")
			}

			return git.run(args)
		},
	}

	cmd.Flags().StringVar(&git.chartPath, "chart-path", ".", "path to chart relative to repository root")
	cmd.Flags().StringVar(&git.remote, "remote", "origin", "git remote to use for primary branch detection")

	addChartRenderFlags(cmd.Flags(), git.localDiffer)
	AddDiffOptions(cmd.Flags(), &git.localDiffer.Options)

	return cmd
}

func (g *gitDiff) run(args []string) error {
	ref1, ref2, err := g.parseRefs(args)
	if err != nil {
		return err
	}

	repoRoot, err := getRepoRoot()
	if err != nil {
		return fmt.Errorf("not in a git repository: %w", err)
	}

	debugPrint("Repository root: %s", repoRoot)
	debugPrint("Comparing %s vs %s", ref1, ref2)
	debugPrint("Chart path: %s", g.chartPath)

	fmt.Fprintf(os.Stderr, "Extracting %s...\n", ref1)
	dir1, cleanup1, err := g.extractRef(ref1, repoRoot)
	if err != nil {
		return fmt.Errorf("failed to extract ref %s: %w", ref1, err)
	}
	defer cleanup1()

	fmt.Fprintf(os.Stderr, "Extracting %s...\n", ref2)
	dir2, cleanup2, err := g.extractRef(ref2, repoRoot)
	if err != nil {
		return fmt.Errorf("failed to extract ref %s: %w", ref2, err)
	}
	defer cleanup2()

	chart1Path := filepath.Join(dir1, g.chartPath)
	chart2Path := filepath.Join(dir2, g.chartPath)

	fmt.Fprintf(os.Stderr, "Building dependencies for %s...\n", ref1)
	if err := buildChartDependencies(chart1Path); err != nil {
		return fmt.Errorf("failed to build dependencies for %s: %w", ref1, err)
	}
	fmt.Fprintf(os.Stderr, "Building dependencies for %s...\n", ref2)
	if err := buildChartDependencies(chart2Path); err != nil {
		return fmt.Errorf("failed to build dependencies for %s: %w", ref2, err)
	}

	if _, err := os.Stat(chart1Path); os.IsNotExist(err) {
		return fmt.Errorf("chart not found at path %s in ref %s", g.chartPath, ref1)
	}
	if _, err := os.Stat(chart2Path); os.IsNotExist(err) {
		return fmt.Errorf("chart not found at path %s in ref %s", g.chartPath, ref2)
	}

	g.localDiffer.Chart1 = chart1Path
	g.localDiffer.Chart2 = chart2Path

	fmt.Fprintf(os.Stderr, "Comparing...\n")
	return g.localDiffer.Run()
}

func (g *gitDiff) parseRefs(args []string) (ref1, ref2 string, err error) {
	switch len(args) {
	case 0:
		ref1, err = detectPrimaryBranch(g.remote)
		if err != nil {
			return "", "", fmt.Errorf("cannot auto-detect primary branch: %w\nPlease specify refs explicitly", err)
		}
		ref2 = "WORKTREE"
	case 1:
		ref1 = args[0]
		ref2 = "WORKTREE"
	case 2:
		ref1 = args[0]
		ref2 = args[1]
	}
	return ref1, ref2, nil
}

func (g *gitDiff) extractRef(ref, repoRoot string) (string, func(), error) {
	if ref == "WORKTREE" {
		return copyWorkingTree(repoRoot)
	}
	return extractGitRef(ref, repoRoot)
}

func detectPrimaryBranch(remote string) (string, error) {
	cmd := exec.Command(gitBinary(), "symbolic-ref", fmt.Sprintf("refs/remotes/%s/HEAD", remote))
	output, err := outputWithRichError(cmd)
	if err != nil {
		return "", fmt.Errorf("failed to detect primary branch from remote '%s': %w", remote, err)
	}

	fullRef := strings.TrimSpace(string(output))
	prefix := "refs/remotes/"
	return strings.TrimPrefix(fullRef, prefix), nil
}

func extractGitRef(ref, repoRoot string) (string, func(), error) {
	verifyCmd := exec.Command(gitBinary(), "rev-parse", "--verify", ref)
	verifyCmd.Dir = repoRoot
	if err := verifyCmd.Run(); err != nil {
		return "", nil, fmt.Errorf("git ref '%s' not found", ref)
	}

	tmpDir, err := os.MkdirTemp("", "helm-diff-git-")
	if err != nil {
		return "", nil, err
	}

	cleanup := func() {
		_ = os.RemoveAll(tmpDir)
	}

	archiveCmd := exec.Command(gitBinary(), "archive", ref)
	archiveCmd.Dir = repoRoot

	tarCmd := exec.Command("tar", "-x", "-C", tmpDir)

	pipe, err := archiveCmd.StdoutPipe()
	if err != nil {
		cleanup()
		return "", nil, err
	}
	tarCmd.Stdin = pipe

	if err := archiveCmd.Start(); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := tarCmd.Start(); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := archiveCmd.Wait(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("git archive failed: %w", err)
	}
	if err := tarCmd.Wait(); err != nil {
		cleanup()
		return "", nil, err
	}

	debugPrint("Extracted ref %s to %s", ref, tmpDir)
	return tmpDir, cleanup, nil
}

func copyWorkingTree(repoRoot string) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "helm-diff-git-worktree-")
	if err != nil {
		return "", nil, err
	}

	cleanup := func() {
		_ = os.RemoveAll(tmpDir)
	}

	destDir := filepath.Join(tmpDir, filepath.Base(repoRoot))

	if err := copyDir(repoRoot, destDir); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("failed to copy working tree: %w", err)
	}

	debugPrint("Copied working tree to %s", destDir)
	return destDir, cleanup, nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		if relPath == ".git" {
			return filepath.SkipDir
		}

		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		return copyFile(path, dstPath, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	return out.Chmod(mode)
}

func getRepoRoot() (string, error) {
	cmd := exec.Command(gitBinary(), "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func buildChartDependencies(chartPath string) error {
	chartYaml := filepath.Join(chartPath, "Chart.yaml")
	if _, err := os.Stat(chartYaml); os.IsNotExist(err) {
		return nil
	}

	debugPrint("Building dependencies for %s", chartPath)
	cmd := exec.Command(helmBinary(), "dependency", "build", chartPath)
	_, err := outputWithRichError(cmd)
	return err
}
