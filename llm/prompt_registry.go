// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package llm

import (
	"embed"
	"fmt"
	"path"
	"strings"
	"text/template"
	"time"
)

//go:embed prompts/*.tmpl
var promptFS embed.FS

var heartbeatTemplates map[HeartbeatVerbosity]*template.Template

func init() {
	funcs := template.FuncMap{
		"formatList":        tmplFormatList,
		"default":           tmplDefault,
		"weekday":           tmplWeekday,
		"formatEscalation":  tmplFormatEscalation,
		"formatVelocity":    tmplFormatVelocity,
		"formatUrgencyTier": tmplFormatUrgencyTier,
	}

	heartbeatTemplates = make(map[HeartbeatVerbosity]*template.Template)
	levels := []HeartbeatVerbosity{
		VerbosityConversational, VerbosityStandard,
		VerbosityDetailed, VerbosityDebug,
	}
	for _, level := range levels {
		filename := "prompts/heartbeat_" + string(level) + ".tmpl"
		t := template.Must(
			template.New(path.Base(filename)).Funcs(funcs).ParseFS(promptFS, filename),
		)
		heartbeatTemplates[level] = t
	}
}

// RenderHeartbeatPrompt renders the heartbeat analysis prompt for the given verbosity level.
func RenderHeartbeatPrompt(req HeartbeatAnalysisRequest) (string, error) {
	v := req.Verbosity
	if v == "" {
		v = VerbosityConversational
	}
	t, ok := heartbeatTemplates[v]
	if !ok {
		t = heartbeatTemplates[VerbosityConversational]
	}
	var buf strings.Builder
	if err := t.Execute(&buf, req); err != nil {
		return "", fmt.Errorf("render heartbeat prompt (%s): %w", v, err)
	}
	return buf.String(), nil
}

// --- Template functions ---

func tmplFormatList(items []string) string {
	if len(items) == 0 {
		return "(none)"
	}
	var b strings.Builder
	for _, item := range items {
		b.WriteString("- ")
		b.WriteString(item)
		b.WriteByte('\n')
	}
	return b.String()
}

func tmplDefault(fallback string, val string) string {
	if val == "" {
		return fallback
	}
	return val
}

func tmplWeekday() string {
	return time.Now().Weekday().String()
}

func tmplFormatEscalation(level int) string {
	if level <= 1 {
		return "1 (first mention — casual)"
	}
	return fmt.Sprintf("%d", level)
}

func tmplFormatVelocity(velocity int) string {
	if velocity <= 0 {
		return "(no new memories since last check)"
	}
	return fmt.Sprintf("%d new memories since last heartbeat action — a lot has happened, reference the new context", velocity)
}

func tmplFormatUrgencyTier(tier string, count int) string {
	if tier == "" {
		return "(not computed)"
	}
	return fmt.Sprintf("%s (%d active signals) — use this as the MINIMUM urgency floor", tier, count)
}
