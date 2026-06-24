package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestLoadPromptTemplates_EmbeddedYAML(t *testing.T) {
	templates := loadPromptTemplates(t.TempDir())
	if !strings.Contains(templates.Identity.Document, "Act like a helpful assistant") {
		t.Fatalf("embedded identity template not loaded: %q", templates.Identity.Document)
	}
	if templates.DynamicContext.SenderLineBoth == "" {
		t.Fatal("embedded dynamic context sender template should be loaded")
	}
}

func TestLoadPromptTemplates_WorkspaceYAMLOverride(t *testing.T) {
	workspace := t.TempDir()
	writePromptTestFile(t, workspace, "prompt_templates.yaml", minimalPromptTemplateYAML("workspace yaml identity"))

	templates := loadPromptTemplates(workspace)
	if templates.Identity.Document != "workspace yaml identity" {
		t.Fatalf("Identity.Document = %q, want workspace yaml override", templates.Identity.Document)
	}
}

func TestLoadPromptTemplates_WorkspaceJSONOverrideCompatibility(t *testing.T) {
	workspace := t.TempDir()
	writePromptTestFile(t, workspace, "prompt_templates.json", minimalPromptTemplateJSON(t, "workspace json identity"))

	templates := loadPromptTemplates(workspace)
	if templates.Identity.Document != "workspace json identity" {
		t.Fatalf("Identity.Document = %q, want workspace json override", templates.Identity.Document)
	}
}

func TestPromptRegistry_RejectsRegisteredSourceWrongPlacement(t *testing.T) {
	registry := NewPromptRegistry()
	if err := registry.RegisterSource(PromptSourceDescriptor{
		ID:      "test:source",
		Owner:   "test",
		Allowed: []PromptPlacement{{Layer: PromptLayerCapability, Slot: PromptSlotTooling}},
	}); err != nil {
		t.Fatalf("RegisterSource() error = %v", err)
	}

	err := registry.ValidatePart(PromptPart{
		ID:      "wrong.placement",
		Layer:   PromptLayerContext,
		Slot:    PromptSlotRuntime,
		Source:  PromptSource{ID: "test:source"},
		Content: "runtime text",
	})
	if err == nil {
		t.Fatal("ValidatePart() error = nil, want placement error")
	}
}

func TestPromptRegistry_AllowsUnregisteredSourceInCompatibilityMode(t *testing.T) {
	registry := NewPromptRegistry()

	err := registry.ValidatePart(PromptPart{
		ID:      "unregistered.part",
		Layer:   PromptLayerCapability,
		Slot:    PromptSlotMCP,
		Source:  PromptSource{ID: "mcp:dynamic-server"},
		Content: "dynamic MCP prompt",
	})
	if err != nil {
		t.Fatalf("ValidatePart() error = %v, want nil for unregistered source", err)
	}
}

func TestRenderPromptPartsLegacy_UsesLayerAndSlotOrder(t *testing.T) {
	parts := []PromptPart{
		{
			ID:      "context.runtime",
			Layer:   PromptLayerContext,
			Slot:    PromptSlotRuntime,
			Source:  PromptSource{ID: PromptSourceRuntime},
			Content: "runtime",
		},
		{
			ID:      "kernel.identity",
			Layer:   PromptLayerKernel,
			Slot:    PromptSlotIdentity,
			Source:  PromptSource{ID: PromptSourceKernel},
			Content: "kernel",
		},
		{
			ID:      "capability.skill",
			Layer:   PromptLayerCapability,
			Slot:    PromptSlotActiveSkill,
			Source:  PromptSource{ID: "skill:test"},
			Content: "skill",
		},
		{
			ID:      "instruction.workspace",
			Layer:   PromptLayerInstruction,
			Slot:    PromptSlotWorkspace,
			Source:  PromptSource{ID: PromptSourceWorkspace},
			Content: "workspace",
		},
	}

	got := renderPromptPartsLegacy(parts)
	want := strings.Join([]string{"kernel", "workspace", "skill", "runtime"}, "\n\n---\n\n")
	if got != want {
		t.Fatalf("renderPromptPartsLegacy() = %q, want %q", got, want)
	}
}

func TestPromptSizeBreakdown_UsesRenderedOrderAndSeparators(t *testing.T) {
	parts := []PromptPart{
		{
			ID:      "context.runtime",
			Layer:   PromptLayerContext,
			Slot:    PromptSlotRuntime,
			Source:  PromptSource{ID: PromptSourceRuntime},
			Content: "runtime",
		},
		{
			ID:      "kernel.identity",
			Layer:   PromptLayerKernel,
			Slot:    PromptSlotIdentity,
			Source:  PromptSource{ID: PromptSourceKernel},
			Content: "kernel",
		},
		{
			ID:      "empty.part",
			Layer:   PromptLayerInstruction,
			Slot:    PromptSlotWorkspace,
			Source:  PromptSource{ID: PromptSourceWorkspace},
			Content: "   ",
		},
	}

	breakdown := promptSizeBreakdown(parts)
	wantTotal := len("kernel") + len("runtime") + len("\n\n---\n\n")
	if breakdown.TotalChars != wantTotal {
		t.Fatalf("TotalChars = %d, want %d", breakdown.TotalChars, wantTotal)
	}
	if len(breakdown.Parts) != 2 {
		t.Fatalf("parts len = %d, want 2", len(breakdown.Parts))
	}
	if breakdown.Parts[0].ID != "kernel.identity" || breakdown.Parts[1].ID != "context.runtime" {
		t.Fatalf("parts order = %#v, want rendered order", breakdown.Parts)
	}
	if breakdown.Parts[0].TokenApprox != 3 {
		t.Fatalf("kernel TokenApprox = %d, want 3", breakdown.Parts[0].TokenApprox)
	}
}

func TestPromptSizeBreakdown_LogFieldsSanitizePromptKeys(t *testing.T) {
	breakdown := promptSizeBreakdown([]PromptPart{{
		ID:      "capability.mcp.github",
		Layer:   PromptLayerCapability,
		Slot:    PromptSlotMCP,
		Source:  PromptSource{ID: "mcp:github-server"},
		Content: "connected",
	}})

	fields := breakdown.LogFields("system_prompt")
	if fields["system_prompt_chars"] != len("connected") {
		t.Fatalf("system_prompt_chars = %#v, want %d", fields["system_prompt_chars"], len("connected"))
	}
	if fields["system_prompt_capability_mcp_mcp_github_server_chars"] != len("connected") {
		t.Fatalf("sanitized part chars missing from fields: %#v", fields)
	}
}

func TestBuildMessagesFromPrompt_IncludesSystemPromptOverlay(t *testing.T) {
	t.Setenv("PICOCLAW_BUILTIN_SKILLS", t.TempDir())
	cb := NewContextBuilder(t.TempDir())

	messages := cb.BuildMessagesFromPrompt(PromptBuildRequest{
		CurrentMessage: "do child task",
		Overlays: promptOverlaysForOptions(processOptions{
			SystemPromptOverride: "Use child-only system instructions.",
		}),
	})

	if len(messages) < 2 {
		t.Fatalf("messages len = %d, want at least 2", len(messages))
	}
	if messages[0].Role != "system" {
		t.Fatalf("messages[0].Role = %q, want system", messages[0].Role)
	}
	if !strings.Contains(messages[0].Content, "Use child-only system instructions.") {
		t.Fatalf("system prompt missing overlay: %q", messages[0].Content)
	}
	last := messages[len(messages)-1]
	if last.Role != "user" || last.Content != "do child task" {
		t.Fatalf("last message = %#v, want user task", last)
	}
}

func TestBuildMessagesFromPrompt_AttachesInternalPromptMetadata(t *testing.T) {
	t.Setenv("PICOCLAW_BUILTIN_SKILLS", t.TempDir())
	cb := NewContextBuilder(t.TempDir())

	messages := cb.BuildMessagesFromPrompt(PromptBuildRequest{
		CurrentMessage: "hello",
		Summary:        "prior context",
	})
	if len(messages) != 3 {
		t.Fatalf("messages len = %d, want 3", len(messages))
	}

	system := messages[0]
	if len(system.SystemParts) != 1 {
		t.Fatalf("system parts len = %d, want identity only", len(system.SystemParts))
	}
	if system.SystemParts[0].PromptLayer != string(PromptLayerKernel) ||
		system.SystemParts[0].PromptSlot != string(PromptSlotIdentity) ||
		system.SystemParts[0].PromptSource != string(PromptSourceKernel) {
		t.Fatalf("static system metadata = %#v, want kernel identity", system.SystemParts[0])
	}

	preset := presetContextMessage(t, messages)
	if !strings.Contains(preset.Content, "## Current Time") {
		t.Fatalf("preset context missing runtime context: %q", preset.Content)
	}
	if !strings.Contains(preset.Content, "prior context") {
		t.Fatalf("preset context missing summary context: %q", preset.Content)
	}

	user := messages[2]
	if user.PromptLayer != string(PromptLayerTurn) ||
		user.PromptSlot != string(PromptSlotMessage) ||
		user.PromptSource != string(PromptSourceUserMessage) {
		t.Fatalf("user message metadata = %#v, want turn message", user)
	}

	data, err := json.Marshal(messages)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(data), "PromptSource") ||
		strings.Contains(string(data), "PromptLayer") ||
		strings.Contains(string(data), "PromptSlot") {
		t.Fatalf("internal prompt metadata leaked into JSON: %s", data)
	}

	breakdown := promptSizeBreakdownFromContentBlocks(system.SystemParts)
	if breakdown.TotalChars <= 0 {
		t.Fatal("system prompt size breakdown should include prompt content")
	}
	if len(breakdown.Parts) != len(system.SystemParts) {
		t.Fatalf("breakdown parts len = %d, want %d", len(breakdown.Parts), len(system.SystemParts))
	}
}

func TestContextBuilder_CollectsToolDiscoveryContributor(t *testing.T) {
	t.Setenv("PICOCLAW_BUILTIN_SKILLS", t.TempDir())
	cb := NewContextBuilder(t.TempDir()).WithToolDiscovery(true, false)

	messages := cb.BuildMessagesFromPrompt(PromptBuildRequest{CurrentMessage: "hello"})
	preset := presetContextMessage(t, messages)
	if !strings.Contains(preset.Content, "tool_search_tool_bm25") {
		t.Fatalf("preset context missing tool discovery rule: %q", preset.Content)
	}
	if strings.Contains(messages[0].Content, "tool_search_tool_bm25") {
		t.Fatalf("system prompt includes tool discovery rule: %q", messages[0].Content)
	}
}

func TestContextBuilder_SuppressesToolDiscoveryContributorWhenToolsUnavailable(t *testing.T) {
	t.Setenv("PICOCLAW_BUILTIN_SKILLS", t.TempDir())
	cb := NewContextBuilder(t.TempDir()).WithToolDiscovery(true, false)

	messages := cb.BuildMessagesFromPrompt(PromptBuildRequest{
		CurrentMessage:      "hello",
		SuppressToolUseRule: true,
	})
	preset := presetContextMessage(t, messages)
	if strings.Contains(preset.Content, "tool_search_tool_bm25") {
		t.Fatalf("preset context includes tool discovery despite tools being unavailable: %q", preset.Content)
	}
}

func TestContextBuilder_SuppressesToolReferencesWhenToolsUnavailable(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("PICOCLAW_BUILTIN_SKILLS", t.TempDir())
	writeTurnProfileSkill(
		t,
		workspace,
		"research",
		"---\ndescription: research skill\n---\n# research\n\nResearch carefully.",
	)
	cb := NewContextBuilder(workspace)

	messages := cb.BuildMessagesFromPrompt(PromptBuildRequest{
		CurrentMessage:      "hello",
		SuppressToolUseRule: true,
	})
	preset := presetContextMessage(t, messages)
	if strings.Contains(preset.Content, "When using tools") ||
		strings.Contains(preset.Content, "read_file tool") ||
		strings.Contains(preset.Content, "update "+workspace+"/memory/MEMORY.md") {
		t.Fatalf("preset context includes tool references despite tools being unavailable: %q", preset.Content)
	}
	if !strings.Contains(preset.Content, "<name>research</name>") {
		t.Fatalf("preset context should keep non-tool skill catalog context, got: %q", preset.Content)
	}
}

func TestContextBuilder_CustomToolAllowListSuppressesReadFileSkillInstruction(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("PICOCLAW_BUILTIN_SKILLS", t.TempDir())
	writeTurnProfileSkill(
		t,
		workspace,
		"research",
		"---\ndescription: research skill\n---\n# research\n\nResearch carefully.",
	)
	cb := NewContextBuilder(workspace)

	messages := cb.BuildMessagesFromPrompt(PromptBuildRequest{
		CurrentMessage: "hello",
		AllowedTools:   []string{"web_search"},
	})
	preset := presetContextMessage(t, messages)
	if strings.Contains(preset.Content, "read_file tool") {
		t.Fatalf("preset context includes read_file skill instruction without read_file permission: %q", preset.Content)
	}
	if !strings.Contains(preset.Content, "<name>research</name>") {
		t.Fatalf("preset context should keep skill catalog context, got: %q", preset.Content)
	}
}

func TestContextBuilder_DefaultContextLoadingKeepsBootstrapAndMemory(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("PICOCLAW_BUILTIN_SKILLS", t.TempDir())
	writePromptTestFile(t, workspace, "AGENT.md", "Always answer with workspace instructions.")
	writePromptTestFile(t, workspace, filepath.Join("memory", "MEMORY.md"), "User likes concise responses.")
	cb := NewContextBuilder(workspace)

	messages := cb.BuildMessagesFromPrompt(PromptBuildRequest{CurrentMessage: "hello"})
	preset := presetContextMessage(t, messages)
	if !strings.Contains(preset.Content, "Always answer with workspace instructions.") {
		t.Fatalf("preset context missing eager bootstrap content: %q", preset.Content)
	}
	if !strings.Contains(preset.Content, "User likes concise responses.") {
		t.Fatalf("preset context missing eager memory content: %q", preset.Content)
	}
	if strings.Contains(messages[0].Content, "Always answer with workspace instructions.") ||
		strings.Contains(messages[0].Content, "User likes concise responses.") {
		t.Fatalf("system prompt includes variable context: %q", messages[0].Content)
	}
}

func TestContextBuilder_DeferredContextLoadingSkipsBootstrapAndMemory(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("PICOCLAW_BUILTIN_SKILLS", t.TempDir())
	writePromptTestFile(t, workspace, "AGENT.md", "Always answer with workspace instructions.")
	writePromptTestFile(t, workspace, filepath.Join("memory", "MEMORY.md"), "User likes concise responses.")
	cb := NewContextBuilder(workspace)

	messages := cb.BuildMessagesFromPrompt(PromptBuildRequest{
		CurrentMessage: "hello",
		ContextLoading: PromptContextLoadingPolicy{
			Bootstrap: PromptContextLoadingDeferred,
			Memory:    PromptContextLoadingDeferred,
		},
	})
	preset := presetContextMessage(t, messages)
	if strings.Contains(preset.Content, "Always answer with workspace instructions.") {
		t.Fatalf("preset context includes deferred bootstrap content: %q", preset.Content)
	}
	if strings.Contains(preset.Content, "User likes concise responses.") {
		t.Fatalf("preset context includes deferred memory content: %q", preset.Content)
	}
	if !strings.Contains(preset.Content, "# Deferred Workspace Context") ||
		!strings.Contains(preset.Content, "AGENT.md") {
		t.Fatalf("preset context missing deferred workspace manifest: %q", preset.Content)
	}
	if !strings.Contains(preset.Content, "# Deferred Memory Context") ||
		!strings.Contains(preset.Content, "memory/MEMORY.md") {
		t.Fatalf("preset context missing deferred memory manifest: %q", preset.Content)
	}
	if !strings.Contains(messages[0].Content, "Act like a helpful assistant.") {
		t.Fatalf("system prompt should keep identity content: %q", messages[0].Content)
	}
	last := messages[len(messages)-1]
	if last.Role != "user" || last.Content != "hello" {
		t.Fatalf("last message = %#v, want user hello", last)
	}
}

func TestContextBuilder_CollectsMCPServerContributor(t *testing.T) {
	t.Setenv("PICOCLAW_BUILTIN_SKILLS", t.TempDir())
	cb := NewContextBuilder(t.TempDir())
	err := cb.RegisterPromptContributor(mcpServerPromptContributor{
		serverName: "GitHub Server",
		toolCount:  3,
		deferred:   true,
	})
	if err != nil {
		t.Fatalf("RegisterPromptContributor() error = %v", err)
	}

	messages := cb.BuildMessagesFromPrompt(PromptBuildRequest{CurrentMessage: "hello"})
	preset := presetContextMessage(t, messages)
	if !strings.Contains(preset.Content, "MCP server `GitHub Server` is connected") {
		t.Fatalf("preset context missing MCP contributor content: %q", preset.Content)
	}
	if strings.Contains(messages[0].Content, "MCP server `GitHub Server` is connected") {
		t.Fatalf("system prompt includes MCP contributor content: %q", messages[0].Content)
	}
}

func writePromptTestFile(t *testing.T, workspace, name, body string) {
	t.Helper()
	path := filepath.Join(workspace, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir prompt test file: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write prompt test file: %v", err)
	}
}

func minimalPromptTemplateYAML(identity string) string {
	return fmt.Sprintf(`identity:
  document: %q
  rules:
    accuracy: accuracy
    accuracy_with_tools: accuracy with tools
    context_summaries: context summaries
    memory: memory
tool_discovery_rule: tool discovery {{tool_names}}
skill_catalog:
  intro: skills
  intro_with_read_file: skills read file
  document: "# Skills\n\n{{intro}}\n\n{{skills_summary}}"
memory:
  document: "# Memory\n\n{{memory_context}}"
multi_message:
  document: multi message
dynamic_context:
  document: "## Current Time\n{{now}}\n\n## Runtime\n{{runtime}}{{channel_block}}{{sender_block}}"
  sender_line_both: "Current sender: %%s (ID: %%s)"
  sender_line_name: "Current sender: %%s"
  sender_line_id: "Current sender: %%s"
summary:
  document: "CONTEXT_SUMMARY: {{summary}}"
  prefix: "CONTEXT_SUMMARY: "
active_skills:
  document: "# Active Skills\n\n{{content}}"
tool_use_fallback: tool use fallback
`, identity)
}

func minimalPromptTemplateJSON(t *testing.T, identity string) string {
	t.Helper()
	templates := map[string]any{
		"identity": map[string]any{
			"document": identity,
			"rules": map[string]string{
				"accuracy":            "accuracy",
				"accuracy_with_tools": "accuracy with tools",
				"context_summaries":   "context summaries",
				"memory":              "memory",
			},
		},
		"tool_discovery_rule": "tool discovery {{tool_names}}",
		"skill_catalog": map[string]string{
			"intro":                "skills",
			"intro_with_read_file": "skills read file",
			"document":             "# Skills\n\n{{intro}}\n\n{{skills_summary}}",
		},
		"memory": map[string]string{
			"document": "# Memory\n\n{{memory_context}}",
		},
		"multi_message": map[string]string{
			"document": "multi message",
		},
		"dynamic_context": map[string]string{
			"document":         "## Current Time\n{{now}}\n\n## Runtime\n{{runtime}}{{channel_block}}{{sender_block}}",
			"sender_line_both": "Current sender: %s (ID: %s)",
			"sender_line_name": "Current sender: %s",
			"sender_line_id":   "Current sender: %s",
		},
		"summary": map[string]string{
			"document": "CONTEXT_SUMMARY: {{summary}}",
			"prefix":   "CONTEXT_SUMMARY: ",
		},
		"active_skills": map[string]string{
			"document": "# Active Skills\n\n{{content}}",
		},
		"tool_use_fallback": "tool use fallback",
	}
	data, err := json.Marshal(templates)
	if err != nil {
		t.Fatalf("marshal minimal prompt template json: %v", err)
	}
	return string(data)
}

func TestContextBuilder_SuppressesMCPServerContributorWhenToolsUnavailable(t *testing.T) {
	t.Setenv("PICOCLAW_BUILTIN_SKILLS", t.TempDir())
	cb := NewContextBuilder(t.TempDir())
	err := cb.RegisterPromptContributor(mcpServerPromptContributor{
		serverName: "GitHub Server",
		toolCount:  3,
		deferred:   false,
	})
	if err != nil {
		t.Fatalf("RegisterPromptContributor() error = %v", err)
	}

	messages := cb.BuildMessagesFromPrompt(PromptBuildRequest{
		CurrentMessage:      "hello",
		SuppressToolUseRule: true,
	})
	preset := presetContextMessage(t, messages)
	if strings.Contains(preset.Content, "MCP server `GitHub Server` is connected") ||
		strings.Contains(preset.Content, "available as native tools") {
		t.Fatalf("preset context includes MCP tooling despite tools being unavailable: %q", preset.Content)
	}
}

func TestContextBuilder_SuppressesAgentDiscoveryContributorWhenToolsUnavailable(t *testing.T) {
	t.Setenv("PICOCLAW_BUILTIN_SKILLS", t.TempDir())
	cb := NewContextBuilder(t.TempDir()).WithAgentDiscovery(
		"main",
		func(agentID string) []AgentDescriptor {
			return []AgentDescriptor{{
				ID:          "helper",
				Name:        "Helper",
				Description: "Helps with tasks",
			}}
		},
	)

	messages := cb.BuildMessagesFromPrompt(PromptBuildRequest{
		CurrentMessage:      "hello",
		SuppressToolUseRule: true,
	})
	preset := presetContextMessage(t, messages)
	if strings.Contains(preset.Content, "Agent Discovery") ||
		strings.Contains(preset.Content, "calling spawn") {
		t.Fatalf("preset context includes agent discovery despite tools being unavailable: %q", preset.Content)
	}
}

func TestContextBuilder_CustomToolAllowListSuppressesUnallowedToolContributors(t *testing.T) {
	t.Setenv("PICOCLAW_BUILTIN_SKILLS", t.TempDir())
	cb := NewContextBuilder(t.TempDir()).
		WithToolDiscovery(true, true).
		WithAgentDiscovery(
			"main",
			func(agentID string) []AgentDescriptor {
				return []AgentDescriptor{{
					ID:          "helper",
					Name:        "Helper",
					Description: "Helps with tasks",
				}}
			},
		)
	err := cb.RegisterPromptContributor(mcpServerPromptContributor{
		serverName: "GitHub Server",
		toolCount:  3,
		deferred:   false,
	})
	if err != nil {
		t.Fatalf("RegisterPromptContributor() error = %v", err)
	}

	messages := cb.BuildMessagesFromPrompt(PromptBuildRequest{
		CurrentMessage: "hello",
		AllowedTools:   []string{"echo_text"},
	})
	preset := presetContextMessage(t, messages)
	blockedSnippets := []string{
		"tool_search_tool_bm25",
		"tool_search_tool_regex",
		"MCP server `GitHub Server` is connected",
		"Agent Discovery",
		"calling spawn",
	}
	for _, snippet := range blockedSnippets {
		if strings.Contains(preset.Content, snippet) {
			t.Fatalf("preset context includes unallowed tool contributor %q: %q", snippet, preset.Content)
		}
	}
}

type testPromptContributor struct {
	desc PromptSourceDescriptor
	part PromptPart
}

func (c testPromptContributor) PromptSource() PromptSourceDescriptor {
	return c.desc
}

func (c testPromptContributor) ContributePrompt(_ context.Context, _ PromptBuildRequest) ([]PromptPart, error) {
	return []PromptPart{c.part}, nil
}

func TestContextBuilder_CollectsRegisteredPromptContributors(t *testing.T) {
	t.Setenv("PICOCLAW_BUILTIN_SKILLS", t.TempDir())
	cb := NewContextBuilder(t.TempDir())

	sourceID := PromptSourceID("test:contributor")
	err := cb.RegisterPromptContributor(testPromptContributor{
		desc: PromptSourceDescriptor{
			ID:      sourceID,
			Owner:   "test",
			Allowed: []PromptPlacement{{Layer: PromptLayerCapability, Slot: PromptSlotMCP}},
		},
		part: PromptPart{
			ID:      "capability.mcp.test",
			Layer:   PromptLayerCapability,
			Slot:    PromptSlotMCP,
			Source:  PromptSource{ID: sourceID, Name: "test"},
			Content: "registered contributor prompt",
		},
	})
	if err != nil {
		t.Fatalf("RegisterPromptContributor() error = %v", err)
	}

	messages := cb.BuildMessagesFromPrompt(PromptBuildRequest{CurrentMessage: "hello"})
	preset := presetContextMessage(t, messages)
	if !strings.Contains(preset.Content, "registered contributor prompt") {
		t.Fatalf("preset context missing contributor content: %q", preset.Content)
	}
	if strings.Contains(messages[0].Content, "registered contributor prompt") {
		t.Fatalf("system prompt includes contributor content: %q", messages[0].Content)
	}
}

func presetContextMessage(t *testing.T, messages []providers.Message) providers.Message {
	t.Helper()
	for _, msg := range messages {
		if msg.Role == "user" && msg.PromptSource == string(PromptSourceRuntime) {
			return msg
		}
	}
	t.Fatalf("missing preset context message in %#v", messages)
	return providers.Message{}
}
