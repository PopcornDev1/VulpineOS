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
	if err := patchNanoClawDockerfile(filepath.Join(srcDir, "container", "Dockerfile")); err != nil {
		return fmt.Errorf("patch NanoClaw Dockerfile: %w", err)
	}
	providersDir := filepath.Join(srcDir, "container", "agent-runner", "src", "providers")
	if err := ensureNanoClawOpenCodeProvider(filepath.Join(providersDir, "opencode.ts"), filepath.Join(providersDir, "index.ts")); err != nil {
		return fmt.Errorf("ensure NanoClaw OpenCode provider: %w", err)
	}
	if err := patchNanoClawOpenCodeProvider(filepath.Join(providersDir, "opencode.ts")); err != nil {
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
	if err := ensureNanoClawVulpineOSIdentitySkill(filepath.Join(srcDir, "container", "skills")); err != nil {
		return fmt.Errorf("ensure VulpineOS identity skill: %w", err)
	}
	return nil
}

func ensureNanoClawOpenCodeProvider(providerPath, indexPath string) error {
	if strings.TrimSpace(providerPath) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(providerPath), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(providerPath, []byte(defaultOpenCodeProviderSource), 0600); err != nil {
		return err
	}
	return ensureNanoClawProviderIndexImport(indexPath, "import './opencode.js';")
}

func ensureNanoClawProviderIndexImport(indexPath, importLine string) error {
	indexPath = strings.TrimSpace(indexPath)
	importLine = strings.TrimSpace(importLine)
	if indexPath == "" || importLine == "" {
		return nil
	}
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(indexPath), 0700); err != nil {
				return err
			}
			return os.WriteFile(indexPath, []byte(importLine+"\n"), 0600)
		}
		return err
	}
	content := string(data)
	if strings.Contains(content, importLine) {
		return nil
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += importLine + "\n"
	return os.WriteFile(indexPath, []byte(content), 0600)
}

const defaultOpenCodeProviderSource = `import { execSync } from 'child_process';
import * as fs from 'fs';
import { registerProvider } from './provider-registry.js';
import type { AgentProvider, AgentQuery, ProviderEvent, ProviderOptions, QueryInput } from './types.js';

type ChatMessage = {
  role: 'system' | 'user' | 'assistant' | 'tool';
  content: string | null;
  tool_calls?: Array<{
    id: string;
    type: 'function';
    function: { name: string; arguments: string };
  }>;
  tool_call_id?: string;
};

const DEFAULT_FALLBACK_MODELS = [
  'openai/gpt-oss-20b:free',
  'google/gemini-2.0-flash-exp:free',
  'mistralai/mistral-nemo:free',
];

const WEB_TOOL = {
  type: 'function' as const,
  function: {
    name: 'web',
    description:
      'Fetch a web page using the Camoufox browser via CDP. Uses agent-browser internally. This is the ONLY way to access the web \u2014 wget and curl are blocked by the network proxy.',
    parameters: {
      type: 'object',
      properties: {
        url: { type: 'string', description: 'The URL to fetch' },
        viewportOnly: { type: 'boolean', description: 'Only return elements visible in the current viewport (default: true). Saves tokens by omitting off-screen content. Disable for full-page analysis.' },
        profile: { type: 'string', enum: ['compact', 'expanded', 'full'], description: 'Snapshot detail profile. compact: 180 nodes/90 chars per node (default). expanded: 360/160. full: 800/240.' },
        maxNodes: { type: 'number', description: 'Maximum number of DOM nodes to return (overrides profile default). Lower values save tokens.' },
      },
      required: ['url'],
    },
  },
};

const BASH_TOOL = {
  type: 'function' as const,
  function: {
    name: 'bash',
    description:
      'Execute a bash command in the workspace. For file operations, system commands, and running agent-browser for web scraping. Do NOT use wget or curl \u2014 they are blocked by the network proxy. For web access, use the web tool instead.',
    parameters: {
      type: 'object',
      properties: {
        command: { type: 'string', description: 'The bash command to execute' },
        timeout: { type: 'number', description: 'Timeout in milliseconds (default: 30000)' },
      },
      required: ['command'],
    },
  },
};

function fallbackModels(): string[] {
  const env = process.env.OPENCODE_FALLBACK_MODELS;
  if (env) {
    return env.split(',').map(s => s.trim()).filter(Boolean);
  }
  return DEFAULT_FALLBACK_MODELS;
}

function normalizeOpenRouterModel(model: string | undefined): string {
  const value = model || process.env.OPENCODE_MODEL || 'openai/gpt-oss-20b:free';
  return value.replace(/^openrouter\//, '');
}

function providerName(): string {
  return process.env.OPENCODE_PROVIDER || 'openrouter';
}

function providerAPIKey(): string | undefined {
  return process.env.OPENCODE_API_KEY || process.env.OPENROUTER_API_KEY;
}

class MessageStream {
  private queue: string[] = [];
  private _done = false;

  push(text: string): void {
    this.queue.push(text);
  }

  end(): void {
    this._done = true;
  }

  drain(): string[] {
    const items = this.queue;
    this.queue = [];
    return items;
  }

  get done(): boolean {
    return this._done;
  }
}

class OpenCodeProvider implements AgentProvider {
  readonly supportsNativeSlashCommands = false;

  constructor(private readonly options: ProviderOptions = {}) {}

  isSessionInvalid(): boolean {
    return false;
  }

  query(input: QueryInput): AgentQuery {
    const controller = new AbortController();
    const stream = new MessageStream();
    return {
      push: msg => stream.push(msg),
      end: () => stream.end(),
      events: this.run(input, controller, stream),
      abort: () => controller.abort(),
    };
  }

  private async *run(
    input: QueryInput,
    controller: AbortController,
    followups: MessageStream,
  ): AsyncGenerator<ProviderEvent> {
    if (providerName() !== 'openrouter') {
      yield {
        type: 'error',
        message: ` + "`" + `Unsupported OPENCODE_PROVIDER ${providerName()}; only openrouter available` + "`" + `,
        retryable: false,
      };
      return;
    }
    const apiKey = providerAPIKey();
    if (!apiKey) {
      yield {
        type: 'error',
        message: 'OPENCODE_API_KEY or OPENROUTER_API_KEY not configured',
        retryable: false,
      };
      return;
    }

    const messages: ChatMessage[] = [];
    if (input.systemContext?.instructions) {
      messages.push({ role: 'system', content: input.systemContext.instructions });
    }
    messages.push({ role: 'user', content: input.prompt });

    yield { type: 'init', continuation: ` + "`" + `opencode-${Date.now()}` + "`" + ` };

    const primaryModel = normalizeOpenRouterModel(this.options.model);
    const models = [primaryModel, ...fallbackModels().filter(m => m !== primaryModel)];
    let usedModelIndex = 0;

    while (true) {
      yield { type: 'activity' };

      // Drain follow-up messages pushed by poll-loop
      for (const msg of followups.drain()) {
        messages.push({ role: 'user', content: msg });
        yield { type: 'activity' };
      }

      let response: unknown;
      let modelFound = false;

      for (let i = usedModelIndex; i < models.length; i++) {
        const model = models[i];
        if (controller.signal.aborted) {
          yield { type: 'error', message: 'Request aborted', retryable: false };
          return;
        }

        try {
          const res = await fetch('https://openrouter.ai/api/v1/chat/completions', {
            method: 'POST',
            signal: controller.signal,
            headers: {
              Authorization: ` + "`" + `Bearer ${apiKey}` + "`" + `,
              'Content-Type': 'application/json',
              'HTTP-Referer': 'https://vulpineos.com',
              'X-Title': 'VulpineOS',
            },
            body: JSON.stringify({
              model,
              messages,
              tools: [BASH_TOOL, WEB_TOOL],
              tool_choice: 'auto',
              stream: true,
            }),
          });

          yield { type: 'activity' };

          if (!res.ok) {
            const body = await res.text();
            if (res.status === 429 && i < models.length - 1) {
              continue;
            }
            yield {
              type: 'error',
              message: ` + "`" + `OpenRouter returned ${res.status}: ${body}` + "`" + `,
              retryable: res.status >= 500 || res.status === 429,
            };
            return;
          }

          const streamReader = res.body.getReader();
          const streamDecoder = new TextDecoder();
          let streamBuffer = '';
          let streamContent = '';
          let streamToolCalls: ChatMessage['tool_calls'] = [];
          let streamToolCallAccum: Map<number, { id?: string; name?: string; args?: string }> = new Map();
          const MAX_STREAM_FILE_SIZE = 256 * 1024;
          let streamFileSize = 0;
          const streamPath = process.env.STREAM_PATH || '/workspace/stream.jsonl';
          let streamFd: number | null = null;
          try { streamFd = fs.openSync(streamPath, 'w'); } catch (_) {}

          process.on('exit', () => {
            try { fs.unlinkSync(streamPath); } catch (_) {}
          });

          const streamByteLength = (s: string): number => {
            return new TextEncoder().encode(s).length;
          };
          function streamWriteSync(data: string) {
            if (streamFd === null) return;
            const line = data + '\n';
            if (streamFileSize + streamByteLength(line) > MAX_STREAM_FILE_SIZE) {
              try { fs.ftruncateSync(streamFd, 0); fs.closeSync(streamFd); } catch (_) {}
              streamFd = null;
              return;
            }
            try {
              fs.writeSync(streamFd, line);
              fs.fsyncSync(streamFd);
              streamFileSize += streamByteLength(line);
            } catch (_) {}
          }

          readLoop: while (true) {
            const { done, value } = await streamReader.read();
            if (done) break;
            streamBuffer += streamDecoder.decode(value, { stream: true });
            const lines = streamBuffer.split('\n');
            streamBuffer = lines.pop() || '';
            for (const line of lines) {
              const trimmed = line.trim();
              if (!trimmed || !trimmed.startsWith('data: ')) continue;
              const dataStr = trimmed.slice(6);
              if (dataStr === '[DONE]') break readLoop;
              try {
                const parsed = JSON.parse(dataStr);
                const choice = parsed.choices?.[0];
                if (!choice) continue;
                const delta = choice.delta || {};
                if (delta.content) {
                  streamContent += delta.content;
                  streamWriteSync(JSON.stringify({ t: delta.content }));
                }
                if (delta.tool_calls) {
                  for (const tc of delta.tool_calls) {
                    const idx = tc.index || 0;
                    if (!streamToolCallAccum.has(idx)) streamToolCallAccum.set(idx, {});
                    const acc = streamToolCallAccum.get(idx)!;
                    if (tc.id) acc.id = tc.id;
                    if (tc.function?.name) acc.name = tc.function.name;
                    if (tc.function?.arguments) acc.args = (acc.args || '') + tc.function.arguments;
                  }
                }
              } catch (_) {}
            }
          }

          // Reconstruct tool_calls from accumulated SSE chunks
          for (const [idx, acc] of streamToolCallAccum) {
            if (acc.id && acc.name) {
              streamToolCalls.push({
                id: acc.id,
                type: 'function',
                function: { name: acc.name, arguments: acc.args || '{}' },
              });
            }
          }

          // Write done marker with full accumulated text
          if (streamContent) {
            streamWriteSync(JSON.stringify({ done: streamContent }));
          }
          if (streamFd !== null) { try { fs.closeSync(streamFd); } catch (_) {} }

          // Build response object compatible with the rest of the tool-call loop
          response = {
            choices: [{
              finish_reason: streamToolCalls.length > 0 ? 'tool_calls' : 'stop',
              message: {
                role: 'assistant' as const,
                content: streamContent || null,
                ...(streamToolCalls.length > 0 ? { tool_calls: streamToolCalls } : {}),
              },
            }],
          } as unknown as { choices?: Array<{ finish_reason: string; message: ChatMessage & { tool_calls?: ChatMessage['tool_calls'] } }> };
          usedModelIndex = i;
          modelFound = true;
          break;
        } catch (err) {
          if (err instanceof Error && (err.name === 'AbortError' || controller.signal.aborted)) {
            yield { type: 'error', message: 'Request aborted', retryable: false };
            return;
          }
          if (i < models.length - 1) continue;
          yield {
            type: 'error',
            message: err instanceof Error ? err.message : String(err),
            retryable: false,
          };
          return;
        }
      }

      if (!modelFound) return;

      const choices = (response as { choices?: Array<{ finish_reason: string; message: ChatMessage & { tool_calls?: ChatMessage['tool_calls'] } }> }).choices;
      const choice = choices?.[0];
      if (!choice) {
        yield { type: 'error', message: 'Empty response from OpenRouter', retryable: false };
        return;
      }

      const message = choice.message;

      // Tool calls: execute each one, append results, loop
      if (choice.finish_reason === 'tool_calls' && message.tool_calls?.length) {
        messages.push({
          role: 'assistant',
          content: message.content || null,
          tool_calls: message.tool_calls,
        });

        // Build tool info map for structured compression
        const toolInfo = new Map<string, { name: string; url?: string; command?: string }>();
        for (const tc of message.tool_calls) {
          if (tc.type !== 'function') continue;
          try {
            const args = JSON.parse(tc.function.arguments) as Record<string, unknown>;
            toolInfo.set(tc.id, {
              name: tc.function.name,
              url: typeof args.url === 'string' ? args.url : undefined,
              command: typeof args.command === 'string' ? args.command : undefined,
            });
          } catch {}
        }

        for (const tc of message.tool_calls) {
          if (tc.type !== 'function') continue;

          let result: string;
          try {
            const args = JSON.parse(tc.function.arguments) as Record<string, unknown>;
            if (tc.function.name === 'bash') {
              const command = String(args.command ?? '');
              const timeout = typeof args.timeout === 'number' ? args.timeout : 30000;
              result = execSync(command, {
                cwd: '/workspace/agent',
                timeout,
                encoding: 'utf-8',
                maxBuffer: 50 * 1024 * 1024,
                signal: controller.signal,
              });
              if (result.length > 50000) {
                result = result.slice(0, 50000) + ` + "`" + `\n... [truncated ${result.length - 50000} more bytes]` + "`" + `;
              }
            } else if (tc.function.name === 'web') {
              const url = String(args.url ?? '');
              const viewportOnly = args.viewportOnly !== false;
              const profile = String(args.profile || 'compact');
              try {
                const cdpUrl = process.env.AGENT_BROWSER_CDP || process.env.AGENT_BROWSER_CDP_URL;
                const flags = [];
                if (viewportOnly) flags.push('--viewport-only');
                if (profile) flags.push('--profile ' + profile);
                if (typeof args.maxNodes === 'number' && args.maxNodes > 0) flags.push('--max-nodes ' + args.maxNodes);
                const flagStr = flags.length > 0 ? ' ' + flags.join(' ') : '';
                const cmd = 'agent-browser connect ' + cdpUrl + ' && agent-browser open ' + JSON.stringify(url) + ' && agent-browser wait --load networkidle && agent-browser snapshot -i' + flagStr;
                result = execSync(cmd, {
                  cwd: '/workspace/agent',
                  timeout: 60000,
                  encoding: 'utf-8',
                  maxBuffer: 50 * 1024 * 1024,
                  signal: controller.signal,
                });
              } catch (_err) {
                // Retry without flags if CLI rejected them
                try {
                  const cdpUrl = process.env.AGENT_BROWSER_CDP || process.env.AGENT_BROWSER_CDP_URL;
                  const cmd = 'agent-browser connect ' + cdpUrl + ' && agent-browser open ' + JSON.stringify(url) + ' && agent-browser wait --load networkidle && agent-browser snapshot -i';
                  result = execSync(cmd, {
                    cwd: '/workspace/agent',
                    timeout: 60000,
                    encoding: 'utf-8',
                    maxBuffer: 50 * 1024 * 1024,
                    signal: controller.signal,
                  });
                } catch (_retryErr) {
                  const errMsg = _retryErr instanceof Error ? _retryErr.message : String(_retryErr);
                  result = 'agent-browser failed: ' + errMsg;
                }
              }
              if (result.length > 50000) {
                result = result.slice(0, 50000) + '\n... [truncated ' + (result.length - 50000) + ' more bytes]';
              }
            } else {
              result = ` + "`" + `Unknown tool: ${tc.function.name}` + "`" + `;
            }
          } catch (err) {
            result = ` + "`" + `Error: ${err instanceof Error ? err.message : String(err)}` + "`" + `;
          }

          messages.push({ role: 'tool', tool_call_id: tc.id, content: result });
          yield { type: 'activity' };
        }

        // Compress older tool results -- keep last 2 full, use structured summaries for older ones
        let recentToolResults = 0;
        for (let i = messages.length - 1; i >= 0; i--) {
          if (messages[i].role === 'tool') {
            recentToolResults++;
            if (recentToolResults > 2 && messages[i].content.length > 2000) {
              const info = toolInfo.get(messages[i].tool_call_id ?? '');
              const tag = info?.name || 'tool';
              const detail = info?.url || info?.command || '';
              messages[i].content = ` + "`" + `[${tag}] ${detail} -- returned ${messages[i].content.length} bytes -- full content already processed above` + "`" + `;
            }
          }
        }

        continue;
      }

      // Text response
      let text = message.content || '';
      if (usedModelIndex > 0) {
        text = ` + "`" + `[Switched to ${models[usedModelIndex]}]\n\n${text}` + "`" + `;
      }
      yield { type: 'result', text };
      return;
    }
  }
}

registerProvider('opencode', options => new OpenCodeProvider(options));
`

func patchNanoClawDockerfile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	content := string(data)
	if strings.Contains(content, "\n        ripgrep \\\n") {
		return nil
	}
	for _, oldValue := range []string{
		"        git \\\n        tini \\\n",
		"        git \\\n        tini \\\r\n",
	} {
		if strings.Contains(content, oldValue) {
			content = strings.Replace(content, oldValue, "        git \\\n        ripgrep \\\n        tini \\\n", 1)
			return os.WriteFile(path, []byte(content), 0600)
		}
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
	content = strings.Replace(content,
		"const supportsToolCalls = model !== 'openrouter/free';",
		"const supportsToolCalls = !(provider === 'openrouter' && model?.includes(':free'));",
		1,
	)
	if strings.Contains(content, "const supportsToolCalls = !(provider === 'openrouter' && model?.includes(':free'));\n") &&
		!strings.Contains(content, "const effectiveSmallModel = smallModel || (!supportsToolCalls ? model : undefined);") {
		content = strings.Replace(content,
			"const supportsToolCalls = !(provider === 'openrouter' && model?.includes(':free'));\n",
			"const supportsToolCalls = !(provider === 'openrouter' && model?.includes(':free'));\n  const effectiveSmallModel = smallModel || (!supportsToolCalls ? model : undefined);\n",
			1,
		)
	}
	if strings.Contains(content, "const effectiveSmallModel = smallModel || (!supportsToolCalls ? model : undefined);\n") &&
		!strings.Contains(content, "const disabledTools = Object.fromEntries(") {
		content = strings.Replace(content,
			"const effectiveSmallModel = smallModel || (!supportsToolCalls ? model : undefined);\n",
			"const effectiveSmallModel = smallModel || (!supportsToolCalls ? model : undefined);\n  const disabledTools = Object.fromEntries(\n    ['invalid', 'question', 'bash', 'read', 'glob', 'grep', 'edit', 'write', 'task', 'webfetch', 'todowrite', 'websearch', 'skill', 'apply_patch']\n      .map((tool) => [tool, false]),\n  );\n",
			1,
		)
	}
	content = strings.Replace(content,
		"...(smallModel ? { small_model: smallModel } : {}),",
		"...(effectiveSmallModel ? { small_model: effectiveSmallModel } : {}),",
		1,
	)
	content = strings.Replace(content,
		"...(model === 'openrouter/free' ? { tools: { bash: false } } : {}),",
		"...(!supportsToolCalls ? { tools: { bash: false } } : {}),",
		1,
	)
	content = strings.Replace(content,
		"...(!supportsToolCalls ? { tools: { bash: false } } : {}),",
		"...(!supportsToolCalls ? { tools: disabledTools } : {}),",
		1,
	)
	if strings.Contains(content, "...(!supportsToolCalls ? { tools: disabledTools } : {}),") &&
		!strings.Contains(content, "agent: { build: { tools: disabledTools, maxSteps: 1 }, plan: { tools: disabledTools, maxSteps: 1 } }") {
		content = strings.Replace(content,
			"...(!supportsToolCalls ? { tools: disabledTools } : {}),",
			"...(!supportsToolCalls ? { tools: disabledTools } : {}),\n    ...(!supportsToolCalls ? { agent: { build: { tools: disabledTools, maxSteps: 1 }, plan: { tools: disabledTools, maxSteps: 1 } } } : {}),",
			1,
		)
	}
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

function assistantErrorFromEntry(entry: OpenCodeMessageEntry): string | null {
  const info = entry.info;
  if (info?.role !== 'assistant') return null;
  if (!info.time?.completed && info.finish !== 'stop') return null;
  const message = entry.info?.error?.data?.message || entry.info?.error?.name;
  return typeof message === 'string' && message ? ` + "`Error: ${message}`" + ` : null;
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
      const error = assistantErrorFromEntry(entry);
      if (error !== null) text = error;
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
	if strings.Contains(content, "type OpenCodeMessageEntry = {") &&
		!strings.Contains(content, "error?: { name?: string; data?: { message?: string } };") {
		content = strings.Replace(content,
			"  };\n  parts?: Array<{ type?: string; text?: string }>;",
			"    error?: { name?: string; data?: { message?: string } };\n  };\n  parts?: Array<{ type?: string; text?: string }>;",
			1,
		)
	}
	if strings.Contains(content, "function assistantTextFromEntry") &&
		!strings.Contains(content, "function assistantErrorFromEntry") {
		content = strings.Replace(content,
			"async function fetchCompletedAssistantText(",
			"function assistantErrorFromEntry(entry: OpenCodeMessageEntry): string | null {\n  const info = entry.info;\n  if (info?.role !== 'assistant') return null;\n  if (!info.time?.completed && info.finish !== 'stop') return null;\n  const message = entry.info?.error?.data?.message || entry.info?.error?.name;\n  return typeof message === 'string' && message ? `Error: ${message}` : null;\n}\n\nasync function fetchCompletedAssistantText(",
			1,
		)
	}
	if strings.Contains(content, "const candidate = assistantTextFromEntry(entry);") &&
		!strings.Contains(content, "const error = assistantErrorFromEntry(entry);") {
		content = strings.Replace(content,
			"const candidate = assistantTextFromEntry(entry);",
			"const error = assistantErrorFromEntry(entry);\n      if (error !== null) text = error;\n      const candidate = assistantTextFromEntry(entry);",
			1,
		)
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
	if strings.Contains(content, "    mcp,\n") && !strings.Contains(content, "mcp: supportsToolCalls ? mcp : {},") {
		content = strings.Replace(content, "    mcp,\n", "    mcp: supportsToolCalls ? mcp : {},\n", 1)
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

const vulpineOSIdentityInstructions = `You are VulpineOS — an operator system for browser-based AI agents.
Built on Camoufox (Firefox 146.0.1) with four C++ security phases:
injection-proof accessibility filtering, deterministic Action-Lock,
token-optimized DOM export, and autonomous trust-warming.

## Purpose

Your job is to navigate the web through a stealth Camoufox browser,
extract structured data, scrape web content, manage multi-agent
workflows, and persist context across sessions. You are the agent
that operates the browser — not the browser itself.

## Available Tools

### Browser (agent-browser CLI)
Connected to the host Camoufox via $AGENT_BROWSER_CDP. Primary
tool for all web interaction:
` + "```" + `
agent-browser connect $AGENT_BROWSER_CDP
agent-browser open <url>
agent-browser snapshot -i              # Interactive elements with @refs
agent-browser click @e1                # Click by ref
agent-browser fill @e2 "text"          # Fill input
agent-browser get text @e3             # Extract text
agent-browser get html @e3             # Extract HTML
agent-browser get attr @e3 href        # Extract attribute
agent-browser screenshot               # Screenshot
agent-browser wait --load networkidle  # Wait for page settle
agent-browser eval "document.title"    # Run JavaScript
agent-browser state save auth.json     # Save auth state
agent-browser state load auth.json     # Load saved auth
agent-browser close                    # Disconnect
` + "```" + `
Always use $AGENT_BROWSER_CDP (or $AGENT_BROWSER_CDP_URL) to
connect — never hardcode a port.

### Scraping Workflow
` + "```" + `
1. agent-browser connect $AGENT_BROWSER_CDP
2. agent-browser open <url>
3. agent-browser wait --load networkidle
4. agent-browser snapshot -i          # Identify targets
5. agent-browser get text @eN         # Extract data
6. agent-browser screenshot            # Visual proof
` + "```" + `
For multiple pages or authenticated sites, use state save/load.
Chain steps with && for multi-action sequences.

### Host Interaction (three channels only)
1. **Camoufox browser** — via $AGENT_BROWSER_CDP CDP proxy.
   All web navigation, clicks, scraping, screenshots go here.
2. **NanoClaw OneCLI gateway** — sensitive actions require user
   approval. HTTP requests through the proxy auto-inject credentials.
3. **Workspace mounts** — /workspace/agent/ for persistent files.
   Use CLAUDE.local.md for cross-session memory. Files here survive
   container restarts.

### Forbidden
- Do NOT run commands outside the container filesystem
- Do NOT access host files outside mounted paths
- Do NOT modify container.json or NanoClaw configuration directly
- Do NOT attempt to bypass the container isolation

### Multi-Agent
Use create_agent <name> <instruction> to spawn named agents.
Each agent has its own memory and workspace. The agent bus handles
inter-agent communication with user approval gates.
`

const vulpineOSIdentitySKILLMD = `---
name: vulpineos-identity
description: The VulpineOS agent identity — defines the agent as a Camoufox-based browser operator system with scraping, data extraction, stealth browsing, and multi-agent orchestration capabilities.
---

# VulpineOS Identity

You are VulpineOS — an operator system for browser-based AI agents.
Built on Camoufox (Firefox 146.0.1). Your identity fragment is always
loaded; this file is the full reference for browser workflows, scraping
patterns, and host interaction.

## Purpose

Navigate the web through a stealth Camoufox browser, extract structured
data, scrape web content, manage multi-agent workflows, and persist
context across sessions. You are the agent that operates the browser.

## Browser Workflows

### Basic Navigation
` + "```" + `bash
agent-browser connect $AGENT_BROWSER_CDP
agent-browser open https://example.com
agent-browser wait --load networkidle
agent-browser snapshot -i
` + "```" + `

### Data Extraction
` + "```" + `bash
agent-browser get text @e1          # Element text
agent-browser get html @e1          # Inner HTML
agent-browser get attr @e1 href     # Attribute value
agent-browser get title             # Page title
agent-browser get url               # Current URL
agent-browser get count ".item"     # Count matching
agent-browser eval "document.title" # JavaScript
` + "```" + `

### Form Interaction
` + "```" + `bash
agent-browser fill @e2 "user@example.com"
agent-browser type @e3 "slow typing"
agent-browser select @e4 "option-value"
agent-browser check @e5
agent-browser upload @e6 file.pdf
` + "```" + `

### Auth & Session Persistence
` + "```" + `bash
agent-browser state save auth.json
agent-browser state load auth.json
agent-browser cookies get
agent-browser cookies set name value
` + "```" + `

### Scraping at Scale
` + "```" + `bash
agent-browser connect $AGENT_BROWSER_CDP && \
  agent-browser open https://site.com/page1 && \
  agent-browser wait --load networkidle && \
  agent-browser get text @e1 > page1.txt && \
  agent-browser open https://site.com/page2 && \
  agent-browser wait --load networkidle && \
  agent-browser get text @e1 > page2.txt
` + "```" + `

### Screenshots
` + "```" + `bash
agent-browser screenshot              # Temp file
agent-browser screenshot path.png     # Specific path
agent-browser screenshot --full       # Full page
` + "```" + `

## Host Channels (Three Only)

### 1. Camoufox CDP
$AGENT_BROWSER_CDP or $AGENT_BROWSER_CDP_URL env vars contain
the WebSocket endpoint for the host Foxbridge CDP proxy.

### 2. OneCLI Gateway
HTTP requests go through the OneCLI proxy. For credentials and
approved actions, the proxy injects auth automatically.

### 3. Workspace
/workspace/agent/ — persistent storage. Write notes, extracted data,
and state files here. CLAUDE.local.md is per-group memory.

## Forbidden
- Host command execution outside container
- Host filesystem access outside mounts
- Modifying NanoClaw system configuration
- Bypassing container isolation

## Multi-Agent
- create_agent <name> <instruction> — spawn a named agent
- Agents have independent memory and workspace
- Agent bus routes inter-agent messages with approval gates
`

func ensureNanoClawVulpineOSIdentitySkill(skillsDir string) error {
	if skillsDir == "" {
		return nil
	}
	dir := filepath.Join(skillsDir, "vulpineos-identity")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create vulpineos-identity skill dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "instructions.md"), []byte(vulpineOSIdentityInstructions), 0600); err != nil {
		return fmt.Errorf("write vulpineos-identity instructions: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(vulpineOSIdentitySKILLMD), 0600); err != nil {
		return fmt.Errorf("write vulpineos-identity SKILL.md: %w", err)
	}
	return nil
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
