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
	if err := patchNanoClawPollLoopRouting(filepath.Join(srcDir, "container", "agent-runner", "src", "poll-loop.ts")); err != nil {
		return fmt.Errorf("patch NanoClaw poll loop routing: %w", err)
	}
	if err := patchNanoClawContainerRunnerBrowserEnv(filepath.Join(srcDir, "src", "container-runner.ts")); err != nil {
		return fmt.Errorf("patch NanoClaw container runner browser env: %w", err)
	}
	if err := patchNanoClawContainerRunnerBrowserEnv(filepath.Join(srcDir, "dist", "container-runner.js")); err != nil {
		return fmt.Errorf("patch NanoClaw compiled container runner browser env: %w", err)
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
	} else {
		oldValue := "options: { apiKey: 'placeholder', baseURL: proxyUrl },"
		if strings.Contains(content, oldValue) {
			content = strings.Replace(content, oldValue, newValue, 1)
		}
	}

	createWithCWD := "const created = await client.session.create({ query: { directory: input.cwd } });"
	if !strings.Contains(content, createWithCWD) {
		content = strings.Replace(content, "const created = await client.session.create();", createWithCWD, 1)
	}

	promptWithCWD := `const promptRes = await client.session.promptAsync({
          path: { id: sessionId },
          query: { directory: input.cwd },
          body: { parts: [{ type: 'text', text }] },
        });`
	if !strings.Contains(content, "query: { directory: input.cwd },") {
		promptWithoutCWD := `const promptRes = await client.session.promptAsync({
          path: { id: sessionId },
          body: { parts: [{ type: 'text', text }] },
        });`
		content = strings.Replace(content, promptWithoutCWD, promptWithCWD, 1)
	}
	if !strings.Contains(content, "function assistantTextFromEntry") {
		helperAnchor := `export class OpenCodeProvider implements AgentProvider {
`
		helper := `type OpenCodeMessageEntry = {
  info?: {
    role?: string;
    finish?: string;
    time?: { completed?: number };
  };
  parts?: Array<{ type?: string; text?: string }>;
};

function assistantTextFromEntry(entry: OpenCodeMessageEntry): string | null {
  const info = entry.info;
  if (info?.role !== 'assistant') return null;
  if (!info.time?.completed && info.finish !== 'stop') return null;
  let text = '';
  for (const part of entry.parts ?? []) {
    if (part?.type === 'text' && typeof part.text === 'string') {
      text = part.text;
    }
  }
  return text || null;
}

async function fetchCompletedAssistantText(
  client: OpencodeClient,
  sessionId: string,
  directory: string,
  promptStartedAt: number,
): Promise<string | null> {
  try {
    const res = await client.session.messages({
      path: { id: sessionId },
      query: { directory, limit: 20 },
    });
    if (res.error) return null;
    let text: string | null = null;
    for (const entry of ((res.data ?? []) as OpenCodeMessageEntry[])) {
      const completedAt = typeof entry.info?.time?.completed === 'number' ? entry.info.time.completed : 0;
      if (completedAt > 0 && completedAt < promptStartedAt - 1000) continue;
      const candidate = assistantTextFromEntry(entry);
      if (candidate !== null) text = candidate;
    }
    return text;
  } catch (err) {
    log(` + "`Failed to fetch completed OpenCode message: ${err instanceof Error ? err.message : String(err)}`" + `);
    return null;
  }
}

function sleepMs(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

`
		if strings.Contains(content, helperAnchor) {
			content = strings.Replace(content, helperAnchor, helper+helperAnchor, 1)
		}
	}
	idleConst := "    const IDLE_TIMEOUT_MS = Number(process.env.OPENCODE_IDLE_TIMEOUT_MS) || 300_000;\n"
	if strings.Contains(content, idleConst) && !strings.Contains(content, "OPENCODE_TURN_TIMEOUT_MS") {
		content = strings.Replace(content, idleConst, idleConst+
			"    const TURN_TIMEOUT_MS = Number(process.env.OPENCODE_TURN_TIMEOUT_MS) || 900_000;\n"+
			"    const EVENT_POLL_INTERVAL_MS = Number(process.env.OPENCODE_EVENT_POLL_INTERVAL_MS) || 1000;\n", 1)
	}
	stateBlock := `        const partTextByMessageId = new Map<string, string>();
        const roleByMessageId = new Map<string, string>();
        let lastEventAt = Date.now();
        let eventTimedOut = false;
`
	if strings.Contains(content, stateBlock) && !strings.Contains(content, "let completedAssistantText: string | null = null;") {
		content = strings.Replace(content, stateBlock, `        const partTextByMessageId = new Map<string, string>();
        const roleByMessageId = new Map<string, string>();
        const promptStartedAt = Date.now();
        const turnStartedAt = Date.now();
        let lastEventAt = Date.now();
        let eventTimedOut = false;
        let completedAssistantText: string | null = null;
`, 1)
	}
	if !strings.Contains(content, "let completedAssistantText: string | null = null;") &&
		strings.Contains(content, "let eventTimedOut = false;\n") {
		content = strings.Replace(content, "let eventTimedOut = false;\n", `let eventTimedOut = false;
        const promptStartedAt = Date.now();
        const turnStartedAt = Date.now();
        let completedAssistantText: string | null = null;
`, 1)
	}
	streamNext := `const { value: ev, done } = await stream.next();`
	if strings.Contains(content, streamNext) && !strings.Contains(content, "const next = await Promise.race") {
		content = strings.Replace(content, streamNext, `if (Date.now() - turnStartedAt > TURN_TIMEOUT_MS) {
              try {
                await client.session.abort({ path: { id: sessionId }, query: { directory: input.cwd } });
              } catch (err) {
                log(`+"`Failed to abort timed-out OpenCode session: ${err instanceof Error ? err.message : String(err)}`"+`);
              }
              self.activeSessionId = undefined;
              destroySharedRuntime();
              throw new Error(`+"`OpenCode turn timeout after ${TURN_TIMEOUT_MS}ms; session aborted`"+`);
            }

            const next = await Promise.race([
              stream.next().then((result) => ({ type: 'event' as const, result })),
              sleepMs(EVENT_POLL_INTERVAL_MS).then(() => ({ type: 'poll' as const })),
            ]);
            if (next.type === 'poll') {
              completedAssistantText = await fetchCompletedAssistantText(client, sessionId, input.cwd, promptStartedAt);
              if (completedAssistantText !== null) {
                break turn;
              }
              continue;
            }

            const { value: ev, done } = next.result;
`, 1)
	}
	if strings.Contains(content, "        let resultText = '';\n") && !strings.Contains(content, "let resultText = completedAssistantText ?? '';") {
		content = strings.Replace(content, "        let resultText = '';\n", "        let resultText = completedAssistantText ?? '';\n", 1)
	}
	return os.WriteFile(path, []byte(content), 0600)
}

func patchNanoClawPollLoopRouting(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	content := string(data)
	if !strings.Contains(content, "function isVulpineReplyAlias") {
		oldValue := `    const dest = findByName(toName);
    if (!dest) {
      log(` + "`Unknown destination in <message to=\"${toName}\">, dropping block`" + `);
      scratchpadParts.push(` + "`[dropped: unknown destination \"${toName}\"] ${body}`" + `);
      continue;
    }
    sendToDestination(dest, body, routing);
    sent++;
`
		newValue := `    const dest = findByName(toName);
    if (!dest) {
      if (isVulpineReplyAlias(toName) && sendToCurrentRouting(body, routing)) {
        sent++;
        continue;
      }
      log(` + "`Unknown destination in <message to=\"${toName}\">, dropping block`" + `);
      scratchpadParts.push(` + "`[dropped: unknown destination \"${toName}\"] ${body}`" + `);
      continue;
    }
    sendToDestination(dest, body, routing);
    sent++;
`
		if strings.Contains(content, oldValue) {
			content = strings.Replace(content, oldValue, newValue, 1)
		}

		anchor := `function sendToDestination(dest: DestinationEntry, body: string, routing: RoutingContext): void {
`
		insert := `function isVulpineReplyAlias(name: string): boolean {
  const normalized = name.trim().toLowerCase();
  return normalized === 'vulpine' || normalized === 'vulpineos' || normalized === 'user' || normalized === 'operator';
}

function sendToCurrentRouting(body: string, routing: RoutingContext): boolean {
  if (!routing.channelType || !routing.platformId) return false;
  writeMessageOut({
    id: generateId(),
    in_reply_to: routing.inReplyTo,
    kind: 'chat',
    platform_id: routing.platformId,
    channel_type: routing.channelType,
    thread_id: routing.threadId,
    content: JSON.stringify({ text: body }),
  });
  return true;
}

`
		if strings.Contains(content, anchor) {
			content = strings.Replace(content, anchor, insert+anchor, 1)
		}
	}
	bareReplyOld := `  const hasUnwrapped = sent === 0 && !!scratchpad;
  if (hasUnwrapped) {
    log(` + "`WARNING: agent output had no <message to=\"...\"> blocks — nothing was sent`" + `);
  }
  return { sent, hasUnwrapped };
`
	if strings.Contains(content, bareReplyOld) && !strings.Contains(content, "sendToCurrentRouting(scratchpad, routing)") {
		bareReplyNew := `  let hasUnwrapped = sent === 0 && !!scratchpad;
  if (hasUnwrapped && sendToCurrentRouting(scratchpad, routing)) {
    sent++;
    hasUnwrapped = false;
  }
  if (hasUnwrapped) {
    log(` + "`WARNING: agent output had no <message to=\"...\"> blocks — nothing was sent`" + `);
  }
  return { sent, hasUnwrapped };
`
		content = strings.Replace(content, bareReplyOld, bareReplyNew, 1)
	}
	return os.WriteFile(path, []byte(content), 0600)
}

func patchNanoClawContainerRunnerBrowserEnv(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	content := string(data)
	if !strings.Contains(content, "function readVulpineAgentBrowserCDP") {
		anchor := "async function buildContainerArgs(\n"
		if !strings.Contains(content, anchor) {
			anchor = "async function buildContainerArgs("
		}
		helper := `function readVulpineAgentBrowserCDP(folder: string): string | undefined {
  try {
    const raw = fs.readFileSync(path.join(GROUPS_DIR, folder, 'agent-browser.json'), 'utf8');
    const cfg = JSON.parse(raw) as { cdp?: unknown; cdpUrl?: unknown };
    const value = typeof cfg.cdp === 'string' ? cfg.cdp : typeof cfg.cdpUrl === 'string' ? cfg.cdpUrl : '';
    const trimmed = value.trim();
    return trimmed || undefined;
  } catch {
    return undefined;
  }
}

`
		if strings.HasSuffix(path, ".js") {
			helper = `function readVulpineAgentBrowserCDP(folder) {
    try {
        const raw = fs.readFileSync(path.join(GROUPS_DIR, folder, 'agent-browser.json'), 'utf8');
        const cfg = JSON.parse(raw);
        const value = typeof cfg.cdp === 'string' ? cfg.cdp : typeof cfg.cdpUrl === 'string' ? cfg.cdpUrl : '';
        const trimmed = value.trim();
        return trimmed || undefined;
    }
    catch {
        return undefined;
    }
}

`
		}
		if strings.Contains(content, anchor) {
			content = strings.Replace(content, anchor, helper+anchor, 1)
		}
	}
	if !strings.Contains(content, "AGENT_BROWSER_CDP=${agentBrowserCDP}") {
		for _, oldValue := range []string{
			"  args.push('-e', `TZ=${TIMEZONE}`);\n",
			"    args.push('-e', `TZ=${TIMEZONE}`);\n",
		} {
			if !strings.Contains(content, oldValue) {
				continue
			}
			indent := oldValue[:strings.Index(oldValue, "args.push")]
			newValue := oldValue + `
` + indent + `const agentBrowserCDP = readVulpineAgentBrowserCDP(agentGroup.folder);
` + indent + `if (agentBrowserCDP) {
` + indent + `  args.push('-e', ` + "`AGENT_BROWSER_CDP=${agentBrowserCDP}`" + `);
` + indent + `  args.push('-e', ` + "`AGENT_BROWSER_CDP_URL=${agentBrowserCDP}`" + `);
` + indent + `}
`
			content = strings.Replace(content, oldValue, newValue, 1)
			break
		}
	}
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
