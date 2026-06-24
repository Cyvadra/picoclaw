package agent

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"gopkg.in/yaml.v3"
)

//go:embed system_prompt_templates.yaml
var embeddedDefaultPromptTemplates []byte

type PromptTemplates struct {
	Identity          IdentityPromptTemplate       `json:"identity" yaml:"identity"`
	ToolDiscoveryRule string                       `json:"tool_discovery_rule" yaml:"tool_discovery_rule"`
	SkillCatalog      SkillCatalogPromptTemplate   `json:"skill_catalog" yaml:"skill_catalog"`
	Memory            SectionPromptTemplate        `json:"memory" yaml:"memory"`
	MultiMessage      SectionPromptTemplate        `json:"multi_message" yaml:"multi_message"`
	DynamicContext    DynamicContextPromptTemplate `json:"dynamic_context" yaml:"dynamic_context"`
	Summary           SummaryPromptTemplate        `json:"summary" yaml:"summary"`
	ActiveSkills      ActiveSkillsPromptTemplate   `json:"active_skills" yaml:"active_skills"`
	ToolUseFallback   string                       `json:"tool_use_fallback" yaml:"tool_use_fallback"`
}

type IdentityPromptTemplate struct {
	Document string `json:"document" yaml:"document"`
	Rules    struct {
		Accuracy          string `json:"accuracy" yaml:"accuracy"`
		AccuracyWithTools string `json:"accuracy_with_tools" yaml:"accuracy_with_tools"`
		ContextSummaries  string `json:"context_summaries" yaml:"context_summaries"`
		Memory            string `json:"memory" yaml:"memory"`
	} `json:"rules" yaml:"rules"`
}

type SkillCatalogPromptTemplate struct {
	Intro             string `json:"intro" yaml:"intro"`
	IntroWithReadFile string `json:"intro_with_read_file" yaml:"intro_with_read_file"`
	Document          string `json:"document" yaml:"document"`
}

type SectionPromptTemplate struct {
	Document string `json:"document" yaml:"document"`
}

type DynamicContextPromptTemplate struct {
	Document       string `json:"document" yaml:"document"`
	SenderLineBoth string `json:"sender_line_both" yaml:"sender_line_both"`
	SenderLineName string `json:"sender_line_name" yaml:"sender_line_name"`
	SenderLineID   string `json:"sender_line_id" yaml:"sender_line_id"`
}

type SummaryPromptTemplate struct {
	Document string `json:"document" yaml:"document"`
	Prefix   string `json:"prefix" yaml:"prefix"`
}

type ActiveSkillsPromptTemplate struct {
	Document string `json:"document" yaml:"document"`
}

func workspacePromptTemplatesFilePath(workspace string) string {
	if strings.TrimSpace(workspace) == "" {
		return ""
	}
	return filepath.Join(workspace, "prompt_templates.yaml")
}

func globalPromptTemplatesFilePath() string {
	return filepath.Join(config.GetHome(), "prompt_templates.yaml")
}

func promptTemplateTrackedPaths(workspace string) []string {
	return uniquePaths(promptTemplateCandidatePaths(workspace))
}

func loadPromptTemplates(workspace string) PromptTemplates {
	for _, path := range promptTemplateCandidatePaths(workspace) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var templates PromptTemplates
		if err := decodePromptTemplates(path, data, &templates); err != nil {
			logger.WarnCF("agent", "Failed to parse prompt templates", map[string]any{
				"path":  path,
				"error": err.Error(),
			})
			continue
		}
		return templates
	}

	var templates PromptTemplates
	if err := yaml.Unmarshal(embeddedDefaultPromptTemplates, &templates); err != nil {
		panic("embedded prompt templates invalid: " + err.Error())
	}
	return templates
}

func promptTemplateCandidatePaths(workspace string) []string {
	paths := []string{}
	if workspacePath := workspacePromptTemplatesFilePath(workspace); workspacePath != "" {
		paths = append(paths,
			workspacePath,
			strings.TrimSuffix(workspacePath, ".yaml")+".yml",
			strings.TrimSuffix(workspacePath, ".yaml")+".json",
		)
	}
	globalPath := globalPromptTemplatesFilePath()
	paths = append(paths,
		globalPath,
		strings.TrimSuffix(globalPath, ".yaml")+".yml",
		strings.TrimSuffix(globalPath, ".yaml")+".json",
	)
	return uniquePaths(paths)
}

func decodePromptTemplates(path string, data []byte, out *PromptTemplates) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return json.Unmarshal(data, out)
	default:
		return yaml.Unmarshal(data, out)
	}
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
