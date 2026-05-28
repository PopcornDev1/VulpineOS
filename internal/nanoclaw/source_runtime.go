package nanoclaw

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"vulpineos/internal/config"
)

func configuredNanoClawSourceDir() string {
	for _, candidate := range []string{
		os.Getenv("VULPINE_NANOCLAW_SRC"),
		filepath.Join(config.Dir(), "nanoclaw-src"),
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(candidate, "package.json")); err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(candidate, "container", "Dockerfile")); err != nil {
			continue
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		return abs
	}
	return ""
}

func ensureNanoClawSourceAssets(srcDir, profileDir string) error {
	srcDir = strings.TrimSpace(srcDir)
	profileDir = strings.TrimSpace(profileDir)
	if srcDir == "" || profileDir == "" {
		return nil
	}
	containerSrc := filepath.Join(srcDir, "container")
	if _, err := os.Stat(filepath.Join(containerSrc, "Dockerfile")); err != nil {
		return nil
	}
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return fmt.Errorf("create NanoClaw profile dir: %w", err)
	}
	if err := patchNanoClawSourceRuntime(srcDir); err != nil {
		return err
	}
	containerDst := filepath.Join(profileDir, "container")
	if err := replaceSymlink(containerDst, containerSrc); err != nil {
		return fmt.Errorf("link NanoClaw container assets: %w", err)
	}
	return nil
}

func patchNanoClawSourceRuntime(srcDir string) error {
	if err := patchNanoClawOpenCodeProvider(filepath.Join(srcDir, "container", "agent-runner", "src", "providers", "opencode.ts")); err != nil {
		return fmt.Errorf("patch NanoClaw OpenCode provider: %w", err)
	}
	return nil
}

func patchNanoClawOpenCodeProvider(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	content := string(data)
	newValue := "options: { apiKey: process.env.OPENCODE_API_KEY || 'placeholder', baseURL: proxyUrl },"
	if strings.Contains(content, newValue) {
		return nil
	}
	oldValue := "options: { apiKey: 'placeholder', baseURL: proxyUrl },"
	if !strings.Contains(content, oldValue) {
		return nil
	}
	content = strings.Replace(content, oldValue, newValue, 1)
	return os.WriteFile(path, []byte(content), 0600)
}

func replaceSymlink(dst, target string) error {
	if existing, err := os.Readlink(dst); err == nil {
		if existing == target {
			return nil
		}
		if err := os.Remove(dst); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
	}
	return os.Symlink(target, dst)
}

func prepareNanoClawSourceRuntime(profileDir string) error {
	srcDir := configuredNanoClawSourceDir()
	if srcDir == "" {
		return nil
	}
	if err := ensureNanoClawSourceAssets(srcDir, profileDir); err != nil {
		return err
	}
	if strings.TrimSpace(os.Getenv("VULPINE_NANOCLAW_SKIP_IMAGE_BUILD")) == "1" {
		return nil
	}
	image := nanoClawAgentImageName(profileDir)
	if image == "" {
		return nil
	}
	if dockerImageExists(image) {
		return nil
	}
	return buildNanoClawAgentImage(profileDir, image)
}

func nanoClawAgentImageName(profileDir string) string {
	profileDir = strings.TrimSpace(profileDir)
	if profileDir == "" {
		return ""
	}
	sum := sha1.Sum([]byte(profileDir))
	return "nanoclaw-agent-v2-" + hex.EncodeToString(sum[:])[:8] + ":latest"
}

func dockerImageExists(image string) bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	cmd := exec.Command("docker", "image", "inspect", image)
	return cmd.Run() == nil
}

func buildNanoClawAgentImage(profileDir, image string) error {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return fmt.Errorf("docker is required to build the NanoClaw agent image: %w", err)
	}
	containerDir := filepath.Join(profileDir, "container")
	if _, err := os.Stat(filepath.Join(containerDir, "Dockerfile")); err != nil {
		return fmt.Errorf("NanoClaw container assets are missing: %w", err)
	}
	logPath := filepath.Join(profileDir, "data", "nanoclaw-image-build.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		return fmt.Errorf("create NanoClaw image build log dir: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("open NanoClaw image build log: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(dockerPath, "build", "-t", image, ".")
	if err := runDockerBuild(cmd, containerDir, logFile); err != nil {
		_, _ = io.WriteString(logFile, "\nVulpineOS: docker build failed: "+err.Error()+"\n")
		if retryErr := buildNanoClawAgentImageWithRelaxedLockfile(dockerPath, containerDir, image, logFile); retryErr == nil {
			return nil
		} else {
			_, _ = io.WriteString(logFile, "VulpineOS: relaxed-lockfile retry failed: "+retryErr.Error()+"\n")
			return fmt.Errorf("build NanoClaw agent image %s: %w (see %s)", image, err, logPath)
		}
	}
	return nil
}

func runDockerBuild(cmd *exec.Cmd, dir string, output io.Writer) error {
	cmd.Dir = dir
	cmd.Stdout = output
	cmd.Stderr = output
	return cmd.Run()
}

func buildNanoClawAgentImageWithRelaxedLockfile(dockerPath, containerDir, image string, logFile *os.File) error {
	_, _ = io.WriteString(logFile, "VulpineOS: retrying NanoClaw image build with relaxed Bun lockfile\n")
	tmpDir, err := os.MkdirTemp("", "vulpineos-nanoclaw-container-*")
	if err != nil {
		return fmt.Errorf("create relaxed NanoClaw build context: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := copyDirectory(containerDir, tmpDir); err != nil {
		return fmt.Errorf("copy relaxed NanoClaw build context: %w", err)
	}
	dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return fmt.Errorf("read relaxed NanoClaw Dockerfile: %w", err)
	}
	relaxed, ok := relaxBunFrozenLockfile(string(data))
	if !ok {
		return fmt.Errorf("Dockerfile does not contain frozen Bun install command")
	}
	if err := os.WriteFile(dockerfilePath, []byte(relaxed), 0600); err != nil {
		return fmt.Errorf("write relaxed NanoClaw Dockerfile: %w", err)
	}
	cmd := exec.Command(dockerPath, "build", "-t", image, ".")
	return runDockerBuild(cmd, tmpDir, logFile)
}

func relaxBunFrozenLockfile(dockerfile string) (string, bool) {
	relaxed := strings.ReplaceAll(dockerfile, "bun install --frozen-lockfile", "bun install")
	return relaxed, relaxed != dockerfile
}

func copyDirectory(src, dst string) error {
	if resolved, err := filepath.EvalSymlinks(src); err == nil {
		src = resolved
	}
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
