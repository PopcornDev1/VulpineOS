package nanoclaw

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelaxBunFrozenLockfile(t *testing.T) {
	in := "RUN --mount=type=cache,target=/root/.bun/install/cache \\\n    bun install --frozen-lockfile\n"

	got, ok := relaxBunFrozenLockfile(in)
	if !ok {
		t.Fatal("relaxBunFrozenLockfile() ok = false, want true")
	}
	if strings.Contains(got, "--frozen-lockfile") {
		t.Fatalf("relaxed Dockerfile still contains frozen lockfile flag:\n%s", got)
	}
	if !strings.Contains(got, "bun install") {
		t.Fatalf("relaxed Dockerfile lost bun install command:\n%s", got)
	}
}

func TestRelaxBunFrozenLockfileNoop(t *testing.T) {
	in := "FROM node:22-slim\n"

	got, ok := relaxBunFrozenLockfile(in)
	if ok {
		t.Fatal("relaxBunFrozenLockfile() ok = true, want false")
	}
	if got != in {
		t.Fatalf("relaxBunFrozenLockfile() changed Dockerfile without target")
	}
}

func TestPatchNanoClawDockerfileInstallsRipgrep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Dockerfile")
	in := "RUN apt-get update && apt-get install -y --no-install-recommends \\\n        curl \\\n        git \\\n        tini \\\n    && rm -rf /var/lib/apt/lists/*\n"
	if err := os.WriteFile(path, []byte(in), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := patchNanoClawDockerfile(path); err != nil {
		t.Fatalf("patchNanoClawDockerfile: %v", err)
	}
	if err := patchNanoClawDockerfile(path); err != nil {
		t.Fatalf("patchNanoClawDockerfile second run: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched Dockerfile: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "        ripgrep \\\n") {
		t.Fatalf("patched Dockerfile missing ripgrep:\n%s", got)
	}
	if strings.Count(got, "ripgrep") != 1 {
		t.Fatalf("patch should be idempotent, got:\n%s", got)
	}
}

func TestPatchNanoClawSourceRuntimeCreatesOpenCodeProviderWhenMissing(t *testing.T) {
	srcDir := t.TempDir()
	providersDir := filepath.Join(srcDir, "container", "agent-runner", "src", "providers")
	if err := os.MkdirAll(providersDir, 0700); err != nil {
		t.Fatalf("mkdir providers: %v", err)
	}
	indexPath := filepath.Join(providersDir, "index.ts")
	if err := os.WriteFile(indexPath, []byte("import './claude.js';\nimport './mock.js';\n"), 0600); err != nil {
		t.Fatalf("write index: %v", err)
	}

	if err := patchNanoClawSourceRuntime(srcDir); err != nil {
		t.Fatalf("patchNanoClawSourceRuntime: %v", err)
	}
	if err := patchNanoClawSourceRuntime(srcDir); err != nil {
		t.Fatalf("patchNanoClawSourceRuntime second run: %v", err)
	}

	provider, err := os.ReadFile(filepath.Join(providersDir, "opencode.ts"))
	if err != nil {
		t.Fatalf("opencode provider was not created: %v", err)
	}
	for _, want := range []string{
		"registerProvider('opencode'",
		"OPENCODE_PROVIDER",
		"OPENCODE_MODEL",
		"OPENCODE_API_KEY",
		"OPENCODE_FALLBACK_MODELS",
		"DEFAULT_FALLBACK_MODELS",
		"openrouter",
		"Switched to",
		"res.status === 429 && i < models.length - 1",
	} {
		if !strings.Contains(string(provider), want) {
			t.Fatalf("created opencode provider missing %q:\n%s", want, provider)
		}
	}

	index, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read provider index: %v", err)
	}
	if strings.Count(string(index), "import './opencode.js';") != 1 {
		t.Fatalf("provider index should import opencode once:\n%s", index)
	}
}

func TestEnsureNanoClawOpenCodeProviderAlwaysOverwrites(t *testing.T) {
	dir := t.TempDir()
	providerPath := filepath.Join(dir, "opencode.ts")
	indexPath := filepath.Join(dir, "index.ts")
	if err := os.WriteFile(indexPath, []byte("import './claude.js';\n"), 0600); err != nil {
		t.Fatalf("write index: %v", err)
	}

	stale := "stale provider content"
	if err := os.WriteFile(providerPath, []byte(stale), 0600); err != nil {
		t.Fatalf("write stale provider: %v", err)
	}

	if err := ensureNanoClawOpenCodeProvider(providerPath, indexPath); err != nil {
		t.Fatalf("ensureNanoClawOpenCodeProvider: %v", err)
	}

	data, err := os.ReadFile(providerPath)
	if err != nil {
		t.Fatalf("read provider: %v", err)
	}
	if string(data) == stale {
		t.Fatalf("ensureNanoClawOpenCodeProvider did not overwrite existing file")
	}
	if !strings.Contains(string(data), "DEFAULT_FALLBACK_MODELS") {
		t.Fatalf("overwritten provider missing fallback logic:\n%s", data)
	}
}

func TestPatchNanoClawOpenCodeProviderUsesInjectedAPIKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.ts")
	in := `const providerOptions = { opencode: { options: { apiKey: 'placeholder', baseURL: proxyUrl }, models: {} } };
const created = await client.session.create();
const promptRes = await client.session.promptAsync({
          path: { id: sessionId },
          body: { parts: [{ type: 'text', text }] },
        });
`
	if err := os.WriteFile(path, []byte(in), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := patchNanoClawOpenCodeProvider(path); err != nil {
		t.Fatalf("patchNanoClawOpenCodeProvider: %v", err)
	}
	if err := patchNanoClawOpenCodeProvider(path); err != nil {
		t.Fatalf("patchNanoClawOpenCodeProvider second run: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched fixture: %v", err)
	}
	got := string(data)
	want := "apiKey: process.env.OPENCODE_API_KEY || 'placeholder'"
	if !strings.Contains(got, want) {
		t.Fatalf("patched provider missing %q:\n%s", want, got)
	}
	if strings.Count(got, "process.env.OPENCODE_API_KEY") != 1 {
		t.Fatalf("patch should be idempotent, got:\n%s", got)
	}
	for _, want := range []string{
		"client.session.create({ query: { directory: input.cwd } })",
		"query: { directory: input.cwd },",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("patched provider missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "query: { directory: input.cwd }") != 2 {
		t.Fatalf("cwd patch should be idempotent, got:\n%s", got)
	}
}

func TestPatchNanoClawOpenCodeProviderAddsCompletedMessageFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.ts")
	in := `function sessionErrorMessage(props: { error?: unknown }): string {
  return JSON.stringify(props.error) || 'OpenCode session error';
}

export class OpenCodeProvider implements AgentProvider {
  query(input: QueryInput): AgentQuery {
    const self = this;
    const IDLE_TIMEOUT_MS = Number(process.env.OPENCODE_IDLE_TIMEOUT_MS) || 300_000;
    async function* gen(): AsyncGenerator<ProviderEvent> {
      const { client, stream } = await ensureSharedRuntime(self.options);
      const sessionId = 's1';
      const promptRes = await client.session.promptAsync({
          path: { id: sessionId },
          body: { parts: [{ type: 'text', text }] },
        });
      if (promptRes.error) throw new Error('bad');
      const partTextByMessageId = new Map<string, string>();
      const roleByMessageId = new Map<string, string>();
      let lastEventAt = Date.now();
      let eventTimedOut = false;
      const timeoutCheck = setInterval(() => {
        if (Date.now() - lastEventAt > IDLE_TIMEOUT_MS) eventTimedOut = true;
      }, 5000);
      try {
        turn: while (true) {
          if (eventTimedOut) {
            throw new Error(` + "`OpenCode event timeout (${IDLE_TIMEOUT_MS}ms)`" + `);
          }
          const { value: ev, done } = await stream.next();
          if (done) {
            throw new Error('OpenCode SSE stream ended unexpectedly');
          }
          if (!ev?.type || ev.type === 'server.connected' || ev.type === 'server.heartbeat') continue;
          lastEventAt = Date.now();
          yield { type: 'activity' };
          switch (ev.type) {
            case 'session.idle': {
              const sid = (ev.properties as { sessionID?: string }).sessionID;
              if (sid === sessionId) {
                break turn;
              }
              break;
            }
          }
        }
      } finally {
        clearInterval(timeoutCheck);
      }
      let resultText = '';
      for (const [msgId, role] of roleByMessageId) {
        if (role === 'assistant') {
          resultText = partTextByMessageId.get(msgId) ?? resultText;
        }
      }
      yield { type: 'result', text: resultText || null };
    }
  }
}
`
	if err := os.WriteFile(path, []byte(in), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := patchNanoClawOpenCodeProvider(path); err != nil {
		t.Fatalf("patchNanoClawOpenCodeProvider: %v", err)
	}
	if err := patchNanoClawOpenCodeProvider(path); err != nil {
		t.Fatalf("patchNanoClawOpenCodeProvider second run: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched fixture: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		"function assistantTextFromEntry",
		"client.session.messages",
		"OPENCODE_TURN_TIMEOUT_MS",
		"client.session.abort",
		"Promise.race",
		"completedAssistantText",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("patched provider missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "function assistantTextFromEntry") != 1 {
		t.Fatalf("patch should be idempotent, got:\n%s", got)
	}
}

func TestPatchNanoClawOpenCodeProviderHandlesOpenRouterFreeModelErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.ts")
	in := `function buildOpenCodeConfig(options: ProviderOptions): Record<string, unknown> {
  const provider = process.env.OPENCODE_PROVIDER || 'anthropic';
  const model = process.env.OPENCODE_MODEL;
  const smallModel = process.env.OPENCODE_SMALL_MODEL;
  const supportsToolCalls = model !== 'openrouter/free';
  const mcp = mcpServersToOpenCodeConfig(options.mcpServers);
  return {
    ...(smallModel ? { small_model: smallModel } : {}),
    ...(model === 'openrouter/free' ? { tools: { bash: false } } : {}),
    mcp,
  };
}

type OpenCodeMessageEntry = {
  info?: {
    role?: string;
    finish?: string;
    time?: { completed?: number };
    error?: { name?: string; data?: { message?: string } };
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
`
	if err := os.WriteFile(path, []byte(in), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := patchNanoClawOpenCodeProvider(path); err != nil {
		t.Fatalf("patchNanoClawOpenCodeProvider: %v", err)
	}
	if err := patchNanoClawOpenCodeProvider(path); err != nil {
		t.Fatalf("patchNanoClawOpenCodeProvider second run: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched fixture: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		"!(provider === 'openrouter' && model?.includes(':free'))",
		"const effectiveSmallModel = smallModel || (!supportsToolCalls ? model : undefined);",
		"const disabledTools = Object.fromEntries(",
		"['invalid', 'question', 'bash', 'read', 'glob', 'grep', 'edit', 'write', 'task', 'webfetch', 'todowrite', 'websearch', 'skill', 'apply_patch']",
		"...(effectiveSmallModel ? { small_model: effectiveSmallModel } : {}),",
		"...(!supportsToolCalls ? { tools: disabledTools } : {}),",
		"...(!supportsToolCalls ? { agent: { build: { tools: disabledTools, maxSteps: 1 }, plan: { tools: disabledTools, maxSteps: 1 } } } : {}),",
		"mcp: supportsToolCalls ? mcp : {},",
		"function assistantErrorFromEntry",
		"entry.info?.error?.data?.message",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("patched provider missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "function assistantErrorFromEntry") != 1 {
		t.Fatalf("patch should be idempotent, got:\n%s", got)
	}
}

func TestPatchNanoClawPollLoopRoutesVulpineAliasToCurrentChat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "poll-loop.ts")
	in := `    const dest = findByName(toName);
    if (!dest) {
      log(` + "`Unknown destination in <message to=\"${toName}\">, dropping block`" + `);
      scratchpadParts.push(` + "`[dropped: unknown destination \"${toName}\"] ${body}`" + `);
      continue;
    }
    sendToDestination(dest, body, routing);
    sent++;

function sendToDestination(dest: DestinationEntry, body: string, routing: RoutingContext): void {
}
`
	if err := os.WriteFile(path, []byte(in), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := patchNanoClawPollLoopRouting(path); err != nil {
		t.Fatalf("patchNanoClawPollLoopRouting: %v", err)
	}
	if err := patchNanoClawPollLoopRouting(path); err != nil {
		t.Fatalf("patchNanoClawPollLoopRouting second run: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched fixture: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		"isVulpineReplyAlias(toName)",
		"sendToCurrentRouting(body, routing)",
		"function isVulpineReplyAlias",
		"function sendToCurrentRouting",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("patched poll-loop missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "function isVulpineReplyAlias") != 1 {
		t.Fatalf("patch should be idempotent, got:\n%s", got)
	}
}

func TestPatchNanoClawPollLoopRoutesBareResultToCurrentChat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "poll-loop.ts")
	in := `  const scratchpad = stripInternalTags(scratchpadParts.join(''));

  if (scratchpad) {
    log(` + "`[scratchpad] ${scratchpad.slice(0, 500)}${scratchpad.length > 500 ? '…' : ''}`" + `);
  }

  const hasUnwrapped = sent === 0 && !!scratchpad;
  if (hasUnwrapped) {
    log(` + "`WARNING: agent output had no <message to=\"...\"> blocks — nothing was sent`" + `);
  }
  return { sent, hasUnwrapped };
}
`
	if err := os.WriteFile(path, []byte(in), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := patchNanoClawPollLoopRouting(path); err != nil {
		t.Fatalf("patchNanoClawPollLoopRouting: %v", err)
	}
	if err := patchNanoClawPollLoopRouting(path); err != nil {
		t.Fatalf("patchNanoClawPollLoopRouting second run: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched fixture: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		"sendToCurrentRouting(scratchpad, routing)",
		"WARNING: agent output had no <message",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("patched poll-loop missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "sendToCurrentRouting(scratchpad, routing)") != 1 {
		t.Fatalf("patch should be idempotent, got:\n%s", got)
	}
}

func TestPatchNanoClawContainerRunnerInjectsAgentBrowserCDPEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "container-runner.ts")
	in := `async function buildContainerArgs(
  mounts: VolumeMount[],
  containerName: string,
  agentGroup: AgentGroup,
): Promise<string[]> {
  const args: string[] = ['run', '--rm', '--name', containerName, '--label', CONTAINER_INSTALL_LABEL];

  // Environment — only vars read by code we don't own.
  // Everything NanoClaw-specific is in container.json (read by runner at startup).
  args.push('-e', ` + "`TZ=${TIMEZONE}`" + `);

  return args;
}
`
	if err := os.WriteFile(path, []byte(in), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := patchNanoClawContainerRunnerBrowserEnv(path); err != nil {
		t.Fatalf("patchNanoClawContainerRunnerBrowserEnv: %v", err)
	}
	if err := patchNanoClawContainerRunnerBrowserEnv(path); err != nil {
		t.Fatalf("patchNanoClawContainerRunnerBrowserEnv second run: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched fixture: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		"agent-browser.json",
		"AGENT_BROWSER_CDP",
		"AGENT_BROWSER_CDP_URL",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("patched container runner missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "agent-browser.json") != 1 {
		t.Fatalf("patch should be idempotent, got:\n%s", got)
	}
}

func TestPatchNanoClawCompiledContainerRunnerInjectsAgentBrowserCDPEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "container-runner.js")
	in := `async function buildContainerArgs(mounts, containerName, agentGroup, containerConfig, provider, providerContribution, agentIdentifier) {
    const args = ['run', '--rm', '--name', containerName, '--label', CONTAINER_INSTALL_LABEL];
    // Environment — only vars read by code we don't own.
    // Everything NanoClaw-specific is in container.json (read by runner at startup).
    args.push('-e', ` + "`TZ=${TIMEZONE}`" + `);
    return args;
}
`
	if err := os.WriteFile(path, []byte(in), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := patchNanoClawContainerRunnerBrowserEnv(path); err != nil {
		t.Fatalf("patchNanoClawContainerRunnerBrowserEnv: %v", err)
	}
	if err := patchNanoClawContainerRunnerBrowserEnv(path); err != nil {
		t.Fatalf("patchNanoClawContainerRunnerBrowserEnv second run: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched fixture: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		"agent-browser.json",
		"AGENT_BROWSER_CDP",
		"AGENT_BROWSER_CDP_URL",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("patched compiled container runner missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "folder: string") {
		t.Fatalf("compiled JS patch should not contain TypeScript annotations:\n%s", got)
	}
	if strings.Count(got, "agent-browser.json") != 1 {
		t.Fatalf("patch should be idempotent, got:\n%s", got)
	}
}

func TestEnsureNanoClawVulpineOSIdentitySkillCreatesFiles(t *testing.T) {
	dir := t.TempDir()
	if err := ensureNanoClawVulpineOSIdentitySkill(dir); err != nil {
		t.Fatalf("ensureNanoClawVulpineOSIdentitySkill: %v", err)
	}
	skillDir := filepath.Join(dir, "vulpineos-identity")
	if _, err := os.Stat(filepath.Join(skillDir, "instructions.md")); err != nil {
		t.Fatalf("instructions.md not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md not created: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(skillDir, "instructions.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "VulpineOS") {
		t.Fatalf("instructions.md missing VulpineOS identity")
	}
	if !strings.Contains(string(data), "agent-browser") {
		t.Fatalf("instructions.md missing agent-browser reference")
	}
	if !strings.Contains(string(data), "AGENT_BROWSER_CDP") {
		t.Fatalf("instructions.md missing AGENT_BROWSER_CDP reference")
	}
}

func TestEnsureNanoClawVulpineOSIdentitySkillIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := ensureNanoClawVulpineOSIdentitySkill(dir); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := ensureNanoClawVulpineOSIdentitySkill(dir); err != nil {
		t.Fatalf("second call: %v", err)
	}
}
