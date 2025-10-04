package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitCmdArgValidation(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		expectError bool
	}{
		{
			name:        "no arguments",
			args:        []string{},
			expectError: false,
		},
		{
			name:        "one argument",
			args:        []string{"main"},
			expectError: false,
		},
		{
			name:        "two arguments",
			args:        []string{"main", "HEAD"},
			expectError: false,
		},
		{
			name:        "three arguments",
			args:        []string{"main", "HEAD", "extra"},
			expectError: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := gitCmd()
			cmd.SetArgs(tc.args)
			err := cmd.Args(cmd, tc.args)

			if tc.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tc.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

func TestGitCmdExecution(t *testing.T) {
	tmpRepo := t.TempDir()

	runGit(t, tmpRepo, "init")
	runGit(t, tmpRepo, "config", "user.name", "Test")
	runGit(t, tmpRepo, "config", "user.email", "test@example.com")

	createTestChart(t, tmpRepo, "v1")
	runGit(t, tmpRepo, "add", ".")
	runGit(t, tmpRepo, "commit", "-m", "v1")
	runGit(t, tmpRepo, "tag", "v1.0.0")

	createTestChart(t, tmpRepo, "v2")
	runGit(t, tmpRepo, "add", ".")
	runGit(t, tmpRepo, "commit", "-m", "v2")
	runGit(t, tmpRepo, "tag", "v2.0.0")

	fakeHelm := filepath.Join(tmpRepo, "helm")
	helmScript := `#!/bin/sh
cat <<EOF
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-config
  namespace: default
data:
  version: $(cat Chart.yaml | grep version | awk '{print $2}')
EOF
`
	if err := os.WriteFile(fakeHelm, []byte(helmScript), 0755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HELM_BIN", fakeHelm)

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)

	if err := os.Chdir(tmpRepo); err != nil {
		t.Fatal(err)
	}

	cmd := gitCmd()
	cmd.SetArgs([]string{"v1.0.0", "v2.0.0"})

	if err := cmd.Execute(); err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}
}

func TestGitCmdWithSubdirectory(t *testing.T) {
	tmpRepo := t.TempDir()

	runGit(t, tmpRepo, "init")
	runGit(t, tmpRepo, "config", "user.name", "Test")
	runGit(t, tmpRepo, "config", "user.email", "test@example.com")

	chartDir := filepath.Join(tmpRepo, "charts", "myapp")
	if err := os.MkdirAll(chartDir, 0755); err != nil {
		t.Fatal(err)
	}

	createTestChartAt(t, chartDir, "v1")
	runGit(t, tmpRepo, "add", ".")
	runGit(t, tmpRepo, "commit", "-m", "v1")
	runGit(t, tmpRepo, "tag", "v1.0.0")

	createTestChartAt(t, chartDir, "v2")
	runGit(t, tmpRepo, "add", ".")
	runGit(t, tmpRepo, "commit", "-m", "v2")

	fakeHelm := filepath.Join(tmpRepo, "helm")
	helmScript := `#!/bin/sh
cat <<EOF
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-config
  namespace: default
EOF
`
	if err := os.WriteFile(fakeHelm, []byte(helmScript), 0755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HELM_BIN", fakeHelm)

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)

	if err := os.Chdir(tmpRepo); err != nil {
		t.Fatal(err)
	}

	cmd := gitCmd()
	cmd.SetArgs([]string{"--chart-path=charts/myapp", "v1.0.0", "HEAD"})

	if err := cmd.Execute(); err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}
}

func TestGitCmdInvalidRef(t *testing.T) {
	tmpRepo := t.TempDir()

	runGit(t, tmpRepo, "init")
	runGit(t, tmpRepo, "config", "user.name", "Test")
	runGit(t, tmpRepo, "config", "user.email", "test@example.com")

	createTestChart(t, tmpRepo, "v1")
	runGit(t, tmpRepo, "add", ".")
	runGit(t, tmpRepo, "commit", "-m", "v1")

	fakeHelm := filepath.Join(tmpRepo, "helm")
	if err := os.WriteFile(fakeHelm, []byte("#!/bin/sh\necho 'test'"), 0755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HELM_BIN", fakeHelm)

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)

	if err := os.Chdir(tmpRepo); err != nil {
		t.Fatal(err)
	}

	cmd := gitCmd()
	cmd.SetArgs([]string{"nonexistent-ref", "HEAD"})

	err = cmd.Execute()
	if err == nil {
		t.Error("Expected error for invalid ref but got none")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\nOutput: %s", args, err, output)
	}
}

func createTestChart(t *testing.T, repoDir, version string) {
	t.Helper()
	createTestChartAt(t, repoDir, version)
}

func createTestChartAt(t *testing.T, chartDir, version string) {
	t.Helper()

	chartYaml := `apiVersion: v2
name: test-chart
description: A test chart
type: application
version: ` + version + `
appVersion: "1.0"
`

	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(chartYaml), 0644); err != nil {
		t.Fatal(err)
	}

	templatesDir := filepath.Join(chartDir, "templates")
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		t.Fatal(err)
	}

	deployment := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-deployment
spec:
  replicas: 1
  selector:
    matchLabels:
      app: test
  template:
    metadata:
      labels:
        app: test
    spec:
      containers:
      - name: test
        image: nginx:` + version + `
`

	if err := os.WriteFile(filepath.Join(templatesDir, "deployment.yaml"), []byte(deployment), 0644); err != nil {
		t.Fatal(err)
	}
}
