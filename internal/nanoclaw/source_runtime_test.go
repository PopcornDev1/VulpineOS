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
