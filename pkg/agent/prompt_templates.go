package agent

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

//go:embed system_prompt_templates.json
var embeddedDefaultPromptTemplates []byte

type PromptTemplates struct {
	Identity          IdentityPromptTemplate       `json:"identity"`
	ToolDiscoveryRule string                       `json:"tool_discovery_rule"`
	SkillCatalog      SkillCatalogPromptTemplate   `json:"skill_catalog"`
	Memory            SectionPromptTemplate        `json:"memory"`
	MultiMessage      SectionPromptTemplate        `json:"multi_message"`
	DynamicContext    DynamicContextPromptTemplate `json:"dynamic_context"`
	Summary           SummaryPromptTemplate        `json:"summary"`
	ActiveSkills      ActiveSkillsPromptTemplate   `json:"active_skills"`
	ToolUseFallback   string                       `json:"tool_use_fallback"`
}

type IdentityPromptTemplate struct {
	Document string `json:"document"`
	Rules    struct {
		Accuracy          string `json:"accuracy"`
		AccuracyWithTools string `json:"accuracy_with_tools"`
		ContextSummaries  string `json:"context_summaries"`
		Memory            string `json:"memory"`
	} `json:"rules"`
}

type SkillCatalogPromptTemplate struct {
	Intro             string `json:"intro"`
	IntroWithReadFile string `json:"intro_with_read_file"`
	Document          string `json:"document"`
}

type SectionPromptTemplate struct {
	Document string `json:"document"`
}

type DynamicContextPromptTemplate struct {
	Document       string `json:"document"`
	SenderLineBoth string `json:"sender_line_both"`
	SenderLineName string `json:"sender_line_name"`
	SenderLineID   string `json:"sender_line_id"`
}

type SummaryPromptTemplate struct {
	Document string `json:"document"`
	Prefix   string `json:"prefix"`
}

type ActiveSkillsPromptTemplate struct {
	Document string `json:"document"`
}

func workspacePromptTemplatesFilePath(workspace string) string {
	if strings.TrimSpace(workspace) == "" {
		return ""
	}
	return workspace + "/prompt_templates.json"
}

func globalPromptTemplatesFilePath() string {
	return config.GetHome() + "/prompt_templates.json"
}

func promptTemplateTrackedPaths(workspace string) []string {
	paths := []string{globalPromptTemplatesFilePath()}
	if workspacePath := workspacePromptTemplatesFilePath(workspace); workspacePath != "" {
		paths = append(paths, workspacePath)
	}
	return uniquePaths(paths)
}

func loadPromptTemplates(workspace string) PromptTemplates {
	paths := []string{}
	if workspacePath := workspacePromptTemplatesFilePath(workspace); workspacePath != "" {
		paths = append(paths, workspacePath)
	}
	paths = append(paths, globalPromptTemplatesFilePath())

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var templates PromptTemplates
		if err := json.Unmarshal(data, &templates); err != nil {
			logger.WarnCF("agent", "Failed to parse prompt templates", map[string]any{
				"path":  path,
				"error": err.Error(),
			})
			continue
		}
		return templates
	}

	var templates PromptTemplates
	if err := json.Unmarshal(embeddedDefaultPromptTemplates, &templates); err != nil {
		panic("embedded prompt templates invalid: " + err.Error())
	}
	return templates
}

func renderTemplate(template string, data map[string]string) string {
	replacerArgs := make([]string, 0, len(data)*2)
	for key, value := range data {
		replacerArgs = append(replacerArgs, "{{"+key+"}}", value)
	}
	return strings.NewReplacer(replacerArgs...).Replace(template)
}

func (pt PromptTemplates) identityPrompt(version, workspacePath, rules string) string {
	return renderTemplate(pt.Identity.Document, map[string]string{
		"version":        version,
		"workspace_path": workspacePath,
		"rules":          rules,
	})
}

func (pt PromptTemplates) skillCatalogPrompt(intro, skillsSummary string) string {
	return renderTemplate(pt.SkillCatalog.Document, map[string]string{
		"intro":          intro,
		"skills_summary": skillsSummary,
	})
}

func (pt PromptTemplates) memoryPrompt(memoryContext string) string {
	return renderTemplate(pt.Memory.Document, map[string]string{
		"memory_context": memoryContext,
	})
}

func (pt PromptTemplates) multiMessagePrompt() string {
	return pt.MultiMessage.Document
}

func (pt PromptTemplates) summaryPrompt(summary string) string {
	return renderTemplate(pt.Summary.Document, map[string]string{
		"summary": summary,
	})
}

func (pt PromptTemplates) activeSkillsPrompt(content string) string {
	return renderTemplate(pt.ActiveSkills.Document, map[string]string{
		"content": content,
	})
}

func (pt PromptTemplates) toolDiscoveryPrompt(toolNames string) string {
	return renderTemplate(pt.ToolDiscoveryRule, map[string]string{
		"tool_names": toolNames,
	})
}

func (pt PromptTemplates) toolUseRule() string {
	return strings.TrimSpace(pt.ToolUseFallback)
}

func (pt PromptTemplates) summaryPrefix() string {
	return pt.Summary.Prefix
}

func (pt PromptTemplates) formatSenderLine(senderID, senderDisplayName string) string {
	senderID = strings.TrimSpace(senderID)
	senderDisplayName = strings.TrimSpace(senderDisplayName)

	switch {
	case senderDisplayName != "" && senderID != "":
		return fmt.Sprintf(pt.DynamicContext.SenderLineBoth, senderDisplayName, senderID)
	case senderDisplayName != "":
		return fmt.Sprintf(pt.DynamicContext.SenderLineName, senderDisplayName)
	case senderID != "":
		return fmt.Sprintf(pt.DynamicContext.SenderLineID, senderID)
	default:
		return ""
	}
}